package geoip_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/AdguardTeam/AdGuardDNS/internal/agdcache"
	"github.com/AdguardTeam/AdGuardDNS/internal/agdservice"
	"github.com/AdguardTeam/AdGuardDNS/internal/geoip"
	"github.com/AdguardTeam/golibs/logutil/slogutil"
	"github.com/AdguardTeam/golibs/netutil"
	"github.com/AdguardTeam/golibs/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// type check
var _ agdservice.Refresher = (*geoip.File)(nil)

// testTimeout is the common timeout for tests and contexts.
const testTimeout = 1 * time.Second

// newFile creates a new *geoip.File with the given configuration and refreshes
// it initially.
func newFile(tb testing.TB, conf *geoip.FileConfig) (g *geoip.File) {
	g = geoip.NewFile(conf)

	ctx := testutil.ContextWithTimeout(tb, testTimeout)
	require.NoError(tb, g.Refresh(ctx))

	return g
}

func TestFile_Data_cityDB(t *testing.T) {
	conf := &geoip.FileConfig{
		Logger:         slogutil.NewDiscardLogger(),
		CacheManager:   agdcache.EmptyManager{},
		ASNPath:        asnPath,
		CountryPath:    cityPath,
		HostCacheSize:  0,
		IPCacheSize:    1,
		AllTopASNs:     allTopASNs,
		CountryTopASNs: countryTopASNs,
	}

	g := newFile(t, conf)

	d, err := g.Data(testHost, testIPWithASN)
	require.NoError(t, err)

	assert.Equal(t, testASN, d.ASN)

	d, err = g.Data(testHost, testIPWithSubdiv)
	require.NoError(t, err)

	assert.Equal(t, testCtry, d.Country)
	assert.Equal(t, testCont, d.Continent)
	assert.Equal(t, testSubdiv, d.TopSubdivision)
}

func TestFile_Data_countryDB(t *testing.T) {
	conf := &geoip.FileConfig{
		Logger:         slogutil.NewDiscardLogger(),
		CacheManager:   agdcache.EmptyManager{},
		ASNPath:        asnPath,
		CountryPath:    countryPath,
		HostCacheSize:  0,
		IPCacheSize:    1,
		AllTopASNs:     allTopASNs,
		CountryTopASNs: countryTopASNs,
	}

	g := newFile(t, conf)

	d, err := g.Data(testHost, testIPWithASN)
	require.NoError(t, err)

	assert.Equal(t, testASN, d.ASN)

	d, err = g.Data(testHost, testIPWithSubdiv)
	require.NoError(t, err)

	assert.Equal(t, testCtry, d.Country)
	assert.Equal(t, testCont, d.Continent)
	assert.Empty(t, d.TopSubdivision)
}

func TestFile_Data_hostCache(t *testing.T) {
	conf := &geoip.FileConfig{
		Logger:         slogutil.NewDiscardLogger(),
		CacheManager:   agdcache.EmptyManager{},
		ASNPath:        asnPath,
		CountryPath:    cityPath,
		HostCacheSize:  1,
		IPCacheSize:    1,
		AllTopASNs:     allTopASNs,
		CountryTopASNs: countryTopASNs,
	}

	g := newFile(t, conf)

	d, err := g.Data(testHost, testIPWithASN)
	require.NoError(t, err)

	assert.Equal(t, testASN, d.ASN)

	d, err = g.Data(testHost, netip.Addr{})
	require.NoError(t, err)

	assert.Equal(t, testASN, d.ASN)

	d, err = g.Data(testOtherHost, netip.Addr{})
	require.NoError(t, err)

	assert.Nil(t, d)
}

func TestFile_SubnetByLocation(t *testing.T) {
	conf := &geoip.FileConfig{
		Logger:         slogutil.NewDiscardLogger(),
		CacheManager:   agdcache.EmptyManager{},
		ASNPath:        asnPath,
		CountryPath:    cityPath,
		HostCacheSize:  0,
		IPCacheSize:    1,
		AllTopASNs:     allTopASNs,
		CountryTopASNs: countryTopASNs,
	}

	g := newFile(t, conf)

	testCases := []struct {
		name    string
		subdiv  string
		country geoip.Country
		want    netip.Prefix
		asn     geoip.ASN
		fam     netutil.AddrFamily
	}{{
		name:    "by_asn",
		country: testIPv4SubnetCtry,
		subdiv:  "",
		asn:     countryTopASNs[testIPv4SubnetCtry],
		fam:     netutil.AddrFamilyIPv4,
		want:    testIPv4CountrySubnet,
	}, {
		name:    "from_top_countries_v4",
		country: testIPv4SubnetCtry,
		subdiv:  "",
		asn:     0,
		fam:     netutil.AddrFamilyIPv4,
		want:    testIPv4CountrySubnet,
	}, {
		name:    "from_top_countries_v6",
		country: testIPv6SubnetCtry,
		subdiv:  "",
		asn:     0,
		fam:     netutil.AddrFamilyIPv6,
		want:    testIPv6CountrySubnet,
	}, {
		name:    "from_countries_dict",
		country: geoip.CountryBT,
		subdiv:  "",
		asn:     0,
		fam:     netutil.AddrFamilyIPv4,
		want:    netip.MustParsePrefix("67.43.156.0/24"),
	}, {
		name:    "not_found",
		country: geoip.CountryFR,
		subdiv:  "",
		asn:     0,
		fam:     netutil.AddrFamilyIPv4,
		want:    netutil.ZeroPrefix(netutil.AddrFamilyIPv4),
	}}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrySubnet, err := g.SubnetByLocation(&geoip.Location{
				Country:        tc.country,
				Continent:      "",
				TopSubdivision: tc.subdiv,
				ASN:            tc.asn,
			}, tc.fam)
			require.NoError(t, err)

			assert.Equal(t, tc.want, ctrySubnet)
		})
	}
}

// Sinks for benchmarks.
var (
	errSink error
	locSink *geoip.Location
)

func BenchmarkFile_Data(b *testing.B) {
	conf := &geoip.FileConfig{
		Logger:         slogutil.NewDiscardLogger(),
		CacheManager:   agdcache.EmptyManager{},
		ASNPath:        asnPath,
		CountryPath:    cityPath,
		HostCacheSize:  0,
		IPCacheSize:    1,
		AllTopASNs:     geoip.DefaultTopASNs,
		CountryTopASNs: geoip.DefaultCountryTopASNs,
	}

	g := newFile(b, conf)

	ipCountry1 := testIPWithCountry

	// Change the eighth byte in testIPWithCountry to create a different address
	// in the same network.
	ipSlice := ipCountry1.AsSlice()
	ipSlice[7] = 1
	ipCountry2, ok := netip.AddrFromSlice(ipSlice)
	require.True(b, ok)

	b.Run("cache", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			locSink, errSink = g.Data(testHost, testIPWithASN)
		}

		assert.Equal(b, testASN, locSink.ASN)

		assert.NoError(b, errSink)
	})

	b.Run("no_cache", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := range b.N {
			// Alternate between the two IPs to force cache misses.
			if i%2 == 0 {
				locSink, errSink = g.Data(testHost, ipCountry1)
			} else {
				locSink, errSink = g.Data(testHost, ipCountry2)
			}
		}

		assert.NotNil(b, locSink)
		assert.NoError(b, errSink)
	})
}

func BenchmarkFile_Refresh(b *testing.B) {
	conf := &geoip.FileConfig{
		Logger:         slogutil.NewDiscardLogger(),
		CacheManager:   agdcache.EmptyManager{},
		ASNPath:        asnPath,
		CountryPath:    cityPath,
		HostCacheSize:  0,
		IPCacheSize:    1,
		AllTopASNs:     geoip.DefaultTopASNs,
		CountryTopASNs: geoip.DefaultCountryTopASNs,
	}

	ctx := context.Background()
	g := geoip.NewFile(conf)

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		errSink = g.Refresh(ctx)
	}

	assert.NoError(b, errSink)

	// Recent result on MBP 15:
	//
	// goos: darwin
	// goarch: amd64
	// pkg: github.com/AdguardTeam/AdGuardDNS/internal/geoip
	// cpu: Intel(R) Core(TM) i7-9750H CPU @ 2.60GHz
	// BenchmarkFile_Refresh-12		807		1405013 ns/op	585430 B/op		18141 allocs/op
}
