// Package mainmw contains the main middleware of AdGuard DNS.  It processes
// filtering, debug DNS API, query logging, as well as statistics.
package mainmw

import (
	"context"

	"github.com/AdguardTeam/AdGuardDNS/internal/agd"
	"github.com/AdguardTeam/AdGuardDNS/internal/agdnet"
	"github.com/AdguardTeam/AdGuardDNS/internal/billstat"
	"github.com/AdguardTeam/AdGuardDNS/internal/dnsmsg"
	"github.com/AdguardTeam/AdGuardDNS/internal/dnsserver"
	"github.com/AdguardTeam/AdGuardDNS/internal/dnssvc/internal"
	"github.com/AdguardTeam/AdGuardDNS/internal/errcoll"
	"github.com/AdguardTeam/AdGuardDNS/internal/filter"
	"github.com/AdguardTeam/AdGuardDNS/internal/geoip"
	"github.com/AdguardTeam/AdGuardDNS/internal/optlog"
	"github.com/AdguardTeam/AdGuardDNS/internal/querylog"
	"github.com/AdguardTeam/AdGuardDNS/internal/rulestat"
	"github.com/AdguardTeam/golibs/errors"
	"github.com/AdguardTeam/golibs/syncutil"
	"github.com/miekg/dns"
)

// Middleware is the main middleware of AdGuard DNS.
type Middleware struct {
	messages   *dnsmsg.Constructor
	cloner     *dnsmsg.Cloner
	fltCtxPool *syncutil.Pool[filteringContext]
	metrics    Metrics
	billStat   billstat.Recorder
	errColl    errcoll.Interface
	fltStrg    filter.Storage
	geoIP      geoip.Interface
	queryLog   querylog.Interface
	ruleStat   rulestat.Interface
}

// Config is the configuration structure for the main middleware.  All fields
// must be non-nil.
type Config struct {
	// Metrics is used to collect the statistics.
	Metrics Metrics

	// Messages is the message constructor used to create blocked and other
	// messages for this middleware.
	Messages *dnsmsg.Constructor

	// Cloner is used to clone messages more efficiently by disposing of parts
	// of DNS responses for later reuse.
	//
	// TODO(a.garipov): Use.
	Cloner *dnsmsg.Cloner

	// BillStat is used to collect billing statistics.
	BillStat billstat.Recorder

	// ErrColl is the error collector that is used to collect critical and
	// non-critical errors.
	ErrColl errcoll.Interface

	// FilterStorage is the storage of all filters.
	FilterStorage filter.Storage

	// GeoIP is the GeoIP database used to detect geographic data about IP
	// addresses in requests and responses.
	GeoIP geoip.Interface

	// QueryLog is used to write the logs into.
	QueryLog querylog.Interface

	// RuleStat is used to collect statistics about matched filtering rules and
	// rule lists.
	RuleStat rulestat.Interface
}

// New returns a new main middleware.  c must not be nil.
func New(c *Config) (mw *Middleware) {
	return &Middleware{
		metrics:  c.Metrics,
		messages: c.Messages,
		cloner:   c.Cloner,
		fltCtxPool: syncutil.NewPool(func() (v *filteringContext) {
			return &filteringContext{}
		}),
		billStat: c.BillStat,
		errColl:  c.ErrColl,
		fltStrg:  c.FilterStorage,
		geoIP:    c.GeoIP,
		queryLog: c.QueryLog,
		ruleStat: c.RuleStat,
	}
}

// type check
var _ dnsserver.Middleware = (*Middleware)(nil)

// Wrap implements the [dnsserver.Middleware] interface for *Middleware
//
// TODO(a.garipov): Refactor and lower gocognit to 10 or below.
func (mw *Middleware) Wrap(next dnsserver.Handler) (wrapped dnsserver.Handler) {
	f := func(ctx context.Context, rw dnsserver.ResponseWriter, req *dns.Msg) (err error) {
		defer func() { err = errors.Annotate(err, "main mw: %w") }()

		fctx := mw.newFilteringContext(req)
		defer mw.fltCtxPool.Put(fctx)

		ri := agd.MustRequestInfoFromContext(ctx)
		optlog.Debug2("processing request %q from %s", ri.ID, ri.RemoteIP)
		defer optlog.Debug2("finished processing request %q from %s", ri.ID, ri.RemoteIP)

		flt := mw.fltStrg.FilterFromContext(ctx, ri)
		mw.filterRequest(ctx, fctx, flt, ri)

		// Check the context error here, since the context could have already
		// been canceled during filtering, e.g. while resolving a safe-search
		// replacement domain.
		err = ctx.Err()
		if err != nil {
			return afterFilteringError{err: err}
		}

		nwrw := internal.MakeNonWriter(rw)
		err = next.ServeDNS(mw.nextParams(ctx, fctx, nwrw, ri))
		if err != nil {
			return err
		}

		fctx.originalResponse = nwrw.Msg()
		mw.filterResponse(ctx, fctx, flt, ri)

		mw.reportMetrics(ctx, fctx, ri)

		mw.setFilteredResponse(ctx, fctx, ri)

		if fctx.isDebug {
			return mw.writeDebugResponse(ctx, fctx, rw)
		}

		err = rw.WriteMsg(ctx, fctx.originalRequest, fctx.filteredResponse)
		if err != nil {
			// Don't wrap the error, because it's informative enough as is.
			return err
		}

		mw.recordQueryInfo(ctx, fctx, ri)

		if fctx.filteredResponse != fctx.originalResponse {
			mw.cloner.Dispose(fctx.originalResponse)
		}

		return nil
	}

	return dnsserver.HandlerFunc(f)
}

// nextParams is a helper that returns the parameters to call the next handler
// with taking the filtering context into account.
func (mw *Middleware) nextParams(
	parent context.Context,
	fctx *filteringContext,
	origRW dnsserver.ResponseWriter,
	ri *agd.RequestInfo,
) (ctx context.Context, rw dnsserver.ResponseWriter, req *dns.Msg) {
	ctx = parent

	modReq := fctx.modifiedRequest
	if modReq == nil {
		return ctx, origRW, fctx.originalRequest
	}

	// Modified request is set only if the request was modified by a CNAME
	// rewrite rule, so resolve the request as if it was for the rewritten name.
	//
	// Clone the request information and replace the host name with the
	// rewritten one, since the request information from current context must
	// only be accessed for reading, see [agd.RequestInfo].  Shallow copy is
	// enough, because we only change the [agd.RequestInfo.Host] field, which is
	// a string.
	modReqInfo := &agd.RequestInfo{}
	*modReqInfo = *ri
	modReqInfo.Host = agdnet.NormalizeDomain(modReq.Question[0].Name)

	ctx = agd.ContextWithRequestInfo(ctx, modReqInfo)

	optlog.Debug2(
		"mainmw: request for %q rewritten to %q by CNAME rewrite rule",
		ri.Host,
		modReqInfo.Host,
	)

	return ctx, origRW, modReq
}

// reportMetrics extracts filtering metrics data from the context and reports it
// to Prometheus.
func (mw *Middleware) reportMetrics(
	ctx context.Context,
	fctx *filteringContext,
	ri *agd.RequestInfo,
) {
	var ctry, cont string
	var asn uint32
	if l := ri.Location; l != nil {
		ctry, cont = string(l.Country), string(l.Continent)
		asn = uint32(l.ASN)
	}

	id, _, isBlocked := filteringData(fctx)
	p, _ := ri.DeviceData()

	mw.metrics.OnRequest(ctx, &RequestMetrics{
		RemoteIP:          ri.RemoteIP,
		Continent:         cont,
		Country:           ctry,
		FilterListID:      string(id),
		FilteringDuration: fctx.elapsed,
		ASN:               asn,
		IsAnonymous:       p == nil,
		IsBlocked:         isBlocked,
	})
}

// reportf is a helper method for reporting non-critical errors.
func (mw *Middleware) reportf(ctx context.Context, format string, args ...any) {
	errcoll.Collectf(ctx, mw.errColl, "mainmw: "+format, args...)
}
