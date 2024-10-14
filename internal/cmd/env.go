package cmd

import (
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"

	"github.com/AdguardTeam/AdGuardDNS/internal/agdhttp"
	"github.com/AdguardTeam/AdGuardDNS/internal/debugsvc"
	"github.com/AdguardTeam/AdGuardDNS/internal/dnsdb"
	"github.com/AdguardTeam/AdGuardDNS/internal/errcoll"
	"github.com/AdguardTeam/AdGuardDNS/internal/version"
	"github.com/AdguardTeam/golibs/errors"
	"github.com/AdguardTeam/golibs/log"
	"github.com/AdguardTeam/golibs/logutil/slogutil"
	"github.com/AdguardTeam/golibs/netutil"
	"github.com/AdguardTeam/golibs/netutil/urlutil"
	"github.com/AdguardTeam/golibs/timeutil"
	"github.com/c2h5oh/datasize"
	"github.com/caarlos0/env/v7"
	"github.com/getsentry/sentry-go"
)

// environment represents the configuration that is kept in the environment.
type environment struct {
	AdultBlockingURL         *urlutil.URL `env:"ADULT_BLOCKING_URL"`
	BillStatURL              *urlutil.URL `env:"BILLSTAT_URL"`
	BlockedServiceIndexURL   *urlutil.URL `env:"BLOCKED_SERVICE_INDEX_URL"`
	ConsulAllowlistURL       *urlutil.URL `env:"CONSUL_ALLOWLIST_URL,notEmpty"`
	ConsulDNSCheckKVURL      *urlutil.URL `env:"CONSUL_DNSCHECK_KV_URL"`
	ConsulDNSCheckSessionURL *urlutil.URL `env:"CONSUL_DNSCHECK_SESSION_URL"`
	FilterIndexURL           *urlutil.URL `env:"FILTER_INDEX_URL,notEmpty"`
	GeneralSafeSearchURL     *urlutil.URL `env:"GENERAL_SAFE_SEARCH_URL"`
	LinkedIPTargetURL        *urlutil.URL `env:"LINKED_IP_TARGET_URL"`
	NewRegDomainsURL         *urlutil.URL `env:"NEW_REG_DOMAINS_URL"`
	ProfilesURL              *urlutil.URL `env:"PROFILES_URL"`
	RuleStatURL              *urlutil.URL `env:"RULESTAT_URL"`
	SafeBrowsingURL          *urlutil.URL `env:"SAFE_BROWSING_URL"`
	YoutubeSafeSearchURL     *urlutil.URL `env:"YOUTUBE_SAFE_SEARCH_URL"`

	BillStatAPIKey    string `env:"BILLSTAT_API_KEY"`
	ConfPath          string `env:"CONFIG_PATH" envDefault:"./config.yaml"`
	FilterCachePath   string `env:"FILTER_CACHE_PATH" envDefault:"./filters/"`
	GeoIPASNPath      string `env:"GEOIP_ASN_PATH" envDefault:"./asn.mmdb"`
	GeoIPCountryPath  string `env:"GEOIP_COUNTRY_PATH" envDefault:"./country.mmdb"`
	ProfilesAPIKey    string `env:"PROFILES_API_KEY"`
	ProfilesCachePath string `env:"PROFILES_CACHE_PATH" envDefault:"./profilecache.pb"`
	RedisAddr         string `env:"REDIS_ADDR"`
	RedisKeyPrefix    string `env:"REDIS_KEY_PREFIX" envDefault:"agdns"`
	QueryLogPath      string `env:"QUERYLOG_PATH" envDefault:"./querylog.jsonl"`
	SSLKeyLogFile     string `env:"SSL_KEY_LOG_FILE"`
	SentryDSN         string `env:"SENTRY_DSN" envDefault:"stderr"`
	WebStaticDir      string `env:"WEB_STATIC_DIR"`

	ListenAddr net.IP `env:"LISTEN_ADDR" envDefault:"127.0.0.1"`

	ProfilesMaxRespSize datasize.ByteSize `env:"PROFILES_MAX_RESP_SIZE" envDefault:"8MB"`

	RedisIdleTimeout timeutil.Duration `env:"REDIS_IDLE_TIMEOUT" envDefault:"30s"`

	RedisMaxActive int `env:"REDIS_MAX_ACTIVE" envDefault:"10"`
	RedisMaxIdle   int `env:"REDIS_MAX_IDLE" envDefault:"3"`

	ListenPort uint16 `env:"LISTEN_PORT" envDefault:"8181"`
	RedisPort  uint16 `env:"REDIS_PORT" envDefault:"6379"`

	Verbosity uint8 `env:"VERBOSE" envDefault:"0"`

	AdultBlockingEnabled     strictBool `env:"ADULT_BLOCKING_ENABLED" envDefault:"1"`
	LogTimestamp             strictBool `env:"LOG_TIMESTAMP" envDefault:"1"`
	NewRegDomainsEnabled     strictBool `env:"NEW_REG_DOMAINS_ENABLED" envDefault:"1"`
	SafeBrowsingEnabled      strictBool `env:"SAFE_BROWSING_ENABLED" envDefault:"1"`
	BlockedServiceEnabled    strictBool `env:"BLOCKED_SERVICE_ENABLED" envDefault:"1"`
	GeneralSafeSearchEnabled strictBool `env:"GENERAL_SAFE_SEARCH_ENABLED" envDefault:"1"`
	YoutubeSafeSearchEnabled strictBool `env:"YOUTUBE_SAFE_SEARCH_ENABLED" envDefault:"1"`
	WebStaticDirEnabled      strictBool `env:"WEB_STATIC_DIR_ENABLED" envDefault:"0"`
}

// parseEnvironment reads the configuration.
func parseEnvironment() (envs *environment, err error) {
	envs = &environment{}
	err = env.Parse(envs)
	if err != nil {
		return nil, fmt.Errorf("parsing environments: %w", err)
	}

	return envs, nil
}

// type check
var _ validator = (*environment)(nil)

// validate implements the [validator] interface for *environment.
func (envs *environment) validate() (err error) {
	// TODO(a.garipov):  Use a similar approach with errors.Join everywhere.
	var errs []error

	errs = envs.validateHTTPURLs(errs)

	if s := envs.FilterIndexURL.Scheme; s != agdhttp.SchemeFile && !agdhttp.CheckHTTPURLScheme(s) {
		errs = append(errs, fmt.Errorf(
			"env %s: not a valid http(s) url or file uri",
			"FILTER_INDEX_URL",
		))
	}

	err = envs.validateWebStaticDir()
	if err != nil {
		errs = append(errs, fmt.Errorf("env WEB_STATIC_DIR: %w", err))
	}

	_, err = slogutil.VerbosityToLevel(envs.Verbosity)
	if err != nil {
		errs = append(errs, fmt.Errorf("env VERBOSE: %w", err))
	}

	return errors.Join(errs...)
}

// urlEnvData is a helper struct for validation of URLs set in environment
// variables.
type urlEnvData struct {
	url        *urlutil.URL
	name       string
	isRequired bool
}

// validateHTTPURLs appends validation errors to the given errs if HTTP(S) URLs
// in environment variables are invalid.  All errors are appended to errs and
// returned as res.
func (envs *environment) validateHTTPURLs(errs []error) (res []error) {
	httpOnlyURLs := []*urlEnvData{{
		url:        envs.AdultBlockingURL,
		name:       "ADULT_BLOCKING_URL",
		isRequired: bool(envs.AdultBlockingEnabled),
	}, {
		url:        envs.BlockedServiceIndexURL,
		name:       "BLOCKED_SERVICE_INDEX_URL",
		isRequired: bool(envs.BlockedServiceEnabled),
	}, {
		url:        envs.ConsulAllowlistURL,
		name:       "CONSUL_ALLOWLIST_URL",
		isRequired: true,
	}, {
		url:        envs.ConsulDNSCheckKVURL,
		name:       "CONSUL_DNSCHECK_KV_URL",
		isRequired: envs.ConsulDNSCheckKVURL != nil,
	}, {
		url:        envs.ConsulDNSCheckSessionURL,
		name:       "CONSUL_DNSCHECK_SESSION_URL",
		isRequired: envs.ConsulDNSCheckSessionURL != nil,
	}, {
		url:        envs.GeneralSafeSearchURL,
		name:       "GENERAL_SAFE_SEARCH_URL",
		isRequired: bool(envs.GeneralSafeSearchEnabled),
	}, {
		url:        envs.LinkedIPTargetURL,
		name:       "LINKED_IP_TARGET_URL",
		isRequired: false,
	}, {
		url:        envs.NewRegDomainsURL,
		name:       "NEW_REG_DOMAINS_URL",
		isRequired: bool(envs.NewRegDomainsEnabled),
	}, {
		url:        envs.RuleStatURL,
		name:       "RULESTAT_URL",
		isRequired: false,
	}, {
		url:        envs.SafeBrowsingURL,
		name:       "SAFE_BROWSING_URL",
		isRequired: bool(envs.SafeBrowsingEnabled),
	}, {
		url:        envs.YoutubeSafeSearchURL,
		name:       "YOUTUBE_SAFE_SEARCH_URL",
		isRequired: bool(envs.YoutubeSafeSearchEnabled),
	}}

	res = errs
	for _, urlData := range httpOnlyURLs {
		if !urlData.isRequired {
			continue
		}

		u := urlData.url
		if u == nil {
			res = append(res, fmt.Errorf("env %s: %w", urlData.name, errors.ErrEmptyValue))

			continue
		}

		if !agdhttp.CheckHTTPURLScheme(u.Scheme) {
			res = append(res, fmt.Errorf("env %s: not a valid http(s) url", urlData.name))
		}
	}

	return res
}

// validateWebStaticDir returns an error if the WEB_STATIC_DIR environment
// variable contains an invalid value.
func (envs *environment) validateWebStaticDir() (err error) {
	if !envs.WebStaticDirEnabled {
		return nil
	}

	dir := envs.WebStaticDir
	if dir == "" {
		return errors.ErrEmptyValue
	}

	// Use a best-effort check to make sure the directory exists.
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}

	if !fi.IsDir() {
		return errors.Error("not a directory")
	}

	return nil
}

// validateFromValidConfig returns an error if environment variables that depend
// on configuration properties contain errors.  conf is expected to be valid.
func (envs *environment) validateFromValidConfig(conf *configuration) (err error) {
	err = envs.validateRedis(conf)
	if err != nil {
		// Don't wrap the error, because it's informative enough as is.
		return err
	}

	if !conf.isProfilesEnabled() {
		return nil
	}

	if envs.ProfilesMaxRespSize > math.MaxInt {
		return fmt.Errorf(
			"PROFILES_MAX_RESP_SIZE: %w: must be less than or equal to %s, got %s",
			errors.ErrOutOfRange,
			datasize.ByteSize(math.MaxInt),
			envs.ProfilesMaxRespSize,
		)
	}

	return envs.validateProfilesURLs()
}

// validateRedis returns an error if environment variables for Redis as a remote
// key-value store for DNS server checking contain errors.
func (envs *environment) validateRedis(conf *configuration) (err error) {
	if conf.Check.RemoteKV.Type != kvModeRedis {
		return nil
	}

	var errs []error
	if envs.RedisAddr == "" {
		errs = append(errs, fmt.Errorf("REDIS_ADDR: %q", errors.ErrEmptyValue))
	}

	if envs.RedisIdleTimeout.Duration <= 0 {
		errs = append(errs, newNotPositiveError("REDIS_IDLE_TIMEOUT", envs.RedisIdleTimeout))
	}

	if envs.RedisMaxActive < 0 {
		errs = append(errs, newNegativeError("REDIS_MAX_ACTIVE", envs.RedisMaxActive))
	}

	if envs.RedisMaxIdle < 0 {
		errs = append(errs, newNegativeError("REDIS_MAX_IDLE", envs.RedisMaxIdle))
	}

	return errors.Join(errs...)
}

// validateProfilesURLs appends validation errors to the given errs if profiles
// URLs in environment variables are invalid.  All errors are appended to errs
// and returned as res.
func (envs *environment) validateProfilesURLs() (err error) {
	grpcOnlyURLs := []*urlEnvData{{
		url:        envs.BillStatURL,
		name:       "BILLSTAT_URL",
		isRequired: true,
	}, {
		url:        envs.ProfilesURL,
		name:       "PROFILES_URL",
		isRequired: true,
	}}

	var res []error
	for _, urlData := range grpcOnlyURLs {
		if !urlData.isRequired {
			continue
		}

		if urlData.url == nil {
			res = append(res, fmt.Errorf("env %s: %w", urlData.name, errors.ErrEmptyValue))

			continue
		}

		if !agdhttp.CheckGRPCURLScheme(urlData.url.Scheme) {
			res = append(res, fmt.Errorf("env %s: not a valid grpc(s) url", urlData.name))
		}
	}

	return errors.Join(res...)
}

// configureLogs sets the configuration for the plain text logs.  It also
// returns a [slog.Logger] for code that uses it.
func (envs *environment) configureLogs() (slogLogger *slog.Logger) {
	var flags int
	if envs.LogTimestamp {
		flags = log.LstdFlags | log.Lmicroseconds
	}

	log.SetFlags(flags)

	lvl := errors.Must(slogutil.VerbosityToLevel(envs.Verbosity))
	if lvl < slog.LevelInfo {
		log.SetLevel(log.DEBUG)
	}

	return slogutil.New(&slogutil.Config{
		Output:       os.Stdout,
		Format:       slogutil.FormatAdGuardLegacy,
		Level:        lvl,
		AddTimestamp: bool(envs.LogTimestamp),
	})
}

// buildErrColl builds and returns an error collector from environment.
func (envs *environment) buildErrColl() (errColl errcoll.Interface, err error) {
	dsn := envs.SentryDSN
	if dsn == "stderr" {
		return errcoll.NewWriterErrorCollector(os.Stderr), nil
	}

	cli, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:              dsn,
		AttachStacktrace: true,
		Release:          version.Version(),
	})
	if err != nil {
		return nil, err
	}

	return errcoll.NewSentryErrorCollector(cli), nil
}

// debugConf returns a debug HTTP service configuration from environment.
func (envs *environment) debugConf(
	dnsDB dnsdb.Interface,
	logger *slog.Logger,
) (conf *debugsvc.Config) {
	// TODO(a.garipov): Simplify the config if these are guaranteed to always be
	// the same.
	addr := netutil.JoinHostPort(envs.ListenAddr.String(), envs.ListenPort)

	// TODO(a.garipov): Consider other ways of making the DNSDB API fully
	// optional.
	var dnsDBAddr string
	var dnsDBHdlr http.Handler
	if h, ok := dnsDB.(http.Handler); ok {
		dnsDBAddr = addr
		dnsDBHdlr = h
	} else {
		dnsDBAddr = ""
		dnsDBHdlr = http.HandlerFunc(http.NotFound)
	}

	conf = &debugsvc.Config{
		DNSDBHandler:   dnsDBHdlr,
		Logger:         logger.With(slogutil.KeyPrefix, "debugsvc"),
		DNSDBAddr:      dnsDBAddr,
		APIAddr:        addr,
		PprofAddr:      addr,
		PrometheusAddr: addr,
	}

	return conf
}

// strictBool is a type for booleans that are parsed from the environment more
// strictly than the usual bool.  It only accepts "0" and "1" as valid values.
type strictBool bool

// UnmarshalText implements the encoding.TextUnmarshaler interface for
// *strictBool.
func (sb *strictBool) UnmarshalText(b []byte) (err error) {
	if len(b) == 1 {
		switch b[0] {
		case '0':
			*sb = false

			return nil
		case '1':
			*sb = true

			return nil
		default:
			// Go on and return an error.
		}
	}

	return fmt.Errorf("invalid value %q, supported: %q, %q", b, "0", "1")
}
