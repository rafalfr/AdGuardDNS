package dnsmsg_test

import (
	"net"
	"testing"

	"github.com/AdguardTeam/AdGuardDNS/internal/dnsmsg"
	"github.com/AdguardTeam/AdGuardDNS/internal/dnsserver/dnsservertest"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testClonerStat is a [dnsmsg.ClonerStat] implementation for tests.
type testClonerStat struct {
	// TODO(a.garipov): Consider better naming for methods of dnsmsg.ClonerStat.
	onOnClone func(isFull bool)
}

// type check
var _ dnsmsg.ClonerStat = (*testClonerStat)(nil)

// OnClone implements the [ClonerStat] interface for *testClonerStat.
func (s *testClonerStat) OnClone(isFull bool) {
	s.onOnClone(isFull)
}

// clonerTestCase is the type for the common test cases for the cloner tests and
// benchmarks.
type clonerTestCase struct {
	msg      *dns.Msg
	wantFull assert.BoolAssertionFunc
	name     string

	// handledByClone is true if [dnsmsg.Clone] is able to correctly clone the
	// message.  It is often false for the messages that contain nil slices,
	// because package github.com/miekg/dns often does not take nilness into
	// account.
	handledByClone bool
}

// clonerTestCases are the common test cases for the clone benchmarks.
var clonerTestCases = []clonerTestCase{{
	msg:            dnsservertest.NewReq(testFQDN, dns.TypeA, dns.ClassINET),
	name:           "req_a",
	wantFull:       assert.True,
	handledByClone: true,
}, {
	msg: dnsservertest.NewResp(
		dns.RcodeSuccess,
		dnsservertest.NewReq(testFQDN, dns.TypeA, dns.ClassINET),
		dnsservertest.SectionAnswer{
			dnsservertest.NewA(testFQDN, 10, testIPv4),
		},
	),
	name:           "resp_a",
	wantFull:       assert.True,
	handledByClone: true,
}, {
	msg: dnsservertest.NewResp(
		dns.RcodeSuccess,
		dnsservertest.NewReq(testFQDN, dns.TypeA, dns.ClassINET),
		dnsservertest.SectionAnswer{
			dnsservertest.NewA(testFQDN, 10, testIPv4),
			dnsservertest.NewA(testFQDN, 10, testIPv4.Next()),
		},
	),
	name:           "resp_a_many",
	wantFull:       assert.True,
	handledByClone: true,
}, {
	msg: dnsservertest.NewResp(
		dns.RcodeSuccess,
		dnsservertest.NewReq(testFQDN, dns.TypeA, dns.ClassINET),
		dnsservertest.SectionAnswer{
			dnsservertest.NewA(testFQDN, 10, testIPv4),
		},
		dnsservertest.SectionNs{
			dnsservertest.NewSOA(testFQDN, 10, "ns.example.", "mbox.example."),
		},
	),
	name:           "resp_a_soa",
	wantFull:       assert.True,
	handledByClone: true,
}, {
	msg:            dnsservertest.NewReq(testFQDN, dns.TypeAAAA, dns.ClassINET),
	name:           "req_aaaa",
	wantFull:       assert.True,
	handledByClone: true,
}, {
	msg: dnsservertest.NewResp(
		dns.RcodeSuccess,
		dnsservertest.NewReq(testFQDN, dns.TypeAAAA, dns.ClassINET),
		dnsservertest.SectionAnswer{
			dnsservertest.NewAAAA(testFQDN, 10, testIPv6),
		},
	),
	name:           "resp_aaaa",
	wantFull:       assert.True,
	handledByClone: true,
}, {
	msg: dnsservertest.NewResp(
		dns.RcodeSuccess,
		dnsservertest.NewReq(testFQDN, dns.TypeA, dns.ClassINET),
		dnsservertest.SectionAnswer{
			dnsservertest.NewCNAME(testFQDN, 10, "cname.example."),
			dnsservertest.NewA("cname.example.", 10, testIPv4),
		},
	),
	name:           "resp_cname_a",
	wantFull:       assert.True,
	handledByClone: true,
}, {
	msg:            newMXResp(testFQDN, 10),
	name:           "resp_mx",
	wantFull:       assert.True,
	handledByClone: true,
}, {
	msg: dnsservertest.NewResp(
		dns.RcodeSuccess,
		dnsservertest.NewReq("4.3.2.1.in-addr.arpa", dns.TypePTR, dns.ClassINET),
		dnsservertest.SectionAnswer{
			dnsservertest.NewPTR("4.3.2.1.in-addr.arpa", 10, "ptr.example."),
		},
	),
	name:           "resp_ptr",
	wantFull:       assert.True,
	handledByClone: true,
}, {
	msg: dnsservertest.NewResp(
		dns.RcodeSuccess,
		dnsservertest.NewReq(testFQDN, dns.TypeTXT, dns.ClassINET),
		dnsservertest.SectionAnswer{
			dnsservertest.NewTXT(testFQDN, 10, "a", "b", "c"),
		},
	),
	name:           "resp_txt",
	wantFull:       assert.True,
	handledByClone: true,
}, {
	msg: dnsservertest.NewResp(
		dns.RcodeSuccess,
		dnsservertest.NewReq(testFQDN, dns.TypeSRV, dns.ClassINET),
		dnsservertest.SectionAnswer{
			dnsservertest.NewSRV(testFQDN, 10, "target.example.", 1, 1, 8080),
		},
	),
	name:           "resp_srv",
	wantFull:       assert.True,
	handledByClone: true,
}, {
	msg: dnsservertest.NewResp(
		dns.RcodeSuccess,
		dnsservertest.NewReq(testFQDN, dns.TypeDNSKEY, dns.ClassINET),
		dnsservertest.SectionAnswer{
			&dns.DNSKEY{},
		},
	),
	name:           "resp_not_full",
	wantFull:       assert.False,
	handledByClone: true,
}, {
	msg: newHTTPSResp([]dns.SVCBKeyValue{
		&dns.SVCBAlpn{Alpn: []string{"http/1.1", "h2", "h3"}},
		&dns.SVCBDoHPath{Template: "/dns-query"},
		&dns.SVCBECHConfig{ECH: []byte{0, 1, 2, 3}},
		&dns.SVCBIPv4Hint{Hint: []net.IP{
			testIPv4.AsSlice(),
			testIPv4.Next().AsSlice(),
		}},
		&dns.SVCBIPv6Hint{Hint: []net.IP{
			testIPv6.AsSlice(),
			testIPv6.Next().AsSlice(),
		}},
		&dns.SVCBLocal{KeyCode: dns.SVCBKey(1234), Data: []byte{3, 2, 1, 0}},
		&dns.SVCBMandatory{Code: []dns.SVCBKey{dns.SVCB_ALPN}},
		&dns.SVCBNoDefaultAlpn{},
		&dns.SVCBOhttp{},
		&dns.SVCBPort{Port: 443},
	}),
	name:           "resp_https",
	wantFull:       assert.True,
	handledByClone: true,
}, {
	msg: newHTTPSResp([]dns.SVCBKeyValue{
		&dns.SVCBIPv4Hint{Hint: []net.IP{}},
		&dns.SVCBIPv6Hint{Hint: []net.IP{}},
	}),
	name:           "resp_https_empty_hint",
	wantFull:       assert.True,
	handledByClone: true,
}, {
	msg: newHTTPSResp([]dns.SVCBKeyValue{
		&dns.SVCBMandatory{},
	}),
	name:           "resp_https_empty_mandatory",
	wantFull:       assert.True,
	handledByClone: true,
}, {
	msg: newHTTPSResp([]dns.SVCBKeyValue{
		&dns.SVCBNoDefaultAlpn{},
		&dns.SVCBOhttp{},
	}),
	name:           "resp_https_empty_values",
	wantFull:       assert.True,
	handledByClone: true,
}, {
	msg:            newHTTPSResp(nil),
	name:           "resp_https_nil_hint",
	wantFull:       assert.True,
	handledByClone: false,
}, {
	msg: newOPTResp(&dns.EDNS0_EDE{
		InfoCode:  dns.ExtendedErrorCodeFiltered,
		ExtraText: "",
	}),
	name:           "resp_a_ede",
	wantFull:       assert.True,
	handledByClone: true,
}, {
	msg: newOPTResp(&dns.EDNS0_SUBNET{
		Code:          dns.EDNS0SUBNET,
		Family:        1,
		SourceNetmask: 24,
		SourceScope:   24,
		Address:       net.IP{1, 2, 3, 0},
	}),
	name:           "resp_a_ecs",
	wantFull:       assert.True,
	handledByClone: true,
}, {
	msg:            newOPTResp(),
	name:           "resp_a_ecs_nil",
	wantFull:       assert.True,
	handledByClone: false,
}}

// newHTTPSResp is a helper that returns a response of type HTTPS with the given
// parameter values.
func newHTTPSResp(kv []dns.SVCBKeyValue) (resp *dns.Msg) {
	ans := &dns.HTTPS{
		SVCB: dns.SVCB{
			Hdr: dns.RR_Header{
				Name:   testFQDN,
				Rrtype: dns.TypeHTTPS,
				Class:  dns.ClassINET,
				Ttl:    10,
			},
			Priority: 10,
			Target:   testFQDN,
			Value:    kv,
		},
	}

	return dnsservertest.NewResp(
		dns.RcodeSuccess,
		dnsservertest.NewReq(testFQDN, dns.TypeHTTPS, dns.ClassINET),
		dnsservertest.SectionAnswer{ans},
	)
}

// newMXResp is a helper that returns a response of type MX with the given
// parameter values.
func newMXResp(mx string, pref uint16) (resp *dns.Msg) {
	ans := &dns.MX{
		Hdr: dns.RR_Header{
			Name:   testFQDN,
			Rrtype: dns.TypeMX,
			Class:  dns.ClassINET,
			Ttl:    10,
		},
		Preference: pref,
		Mx:         mx,
	}

	return dnsservertest.NewResp(
		dns.RcodeSuccess,
		dnsservertest.NewReq(testFQDN, dns.TypeMX, dns.ClassINET),
		dnsservertest.SectionAnswer{ans},
	)
}

// newOPTResp is a helper that returns a response of type OPT with the given
// parameter values.
func newOPTResp(opt ...dns.EDNS0) (resp *dns.Msg) {
	ex := &dns.OPT{
		Hdr: dns.RR_Header{
			Name:   testFQDN,
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET,
			Ttl:    10,
		},
		Option: opt,
	}

	return dnsservertest.NewResp(
		dns.RcodeSuccess,
		dnsservertest.NewReq(testFQDN, dns.TypeA, dns.ClassINET),
		dnsservertest.SectionAnswer{
			dnsservertest.NewA(testFQDN, 10, testIPv4),
		},
		dnsservertest.SectionExtra{ex},
	)
}

func TestCloner_Clone(t *testing.T) {
	for _, tc := range clonerTestCases {
		t.Run(tc.name, func(t *testing.T) {
			var gotIsFull bool
			c := dnsmsg.NewCloner(&testClonerStat{
				onOnClone: func(isFull bool) {
					gotIsFull = isFull
				},
			})

			clone := c.Clone(tc.msg)
			assert.NotSame(t, tc.msg, clone)
			assert.Equal(t, tc.msg, clone)
			tc.wantFull(t, gotIsFull)

			// Check again after disposing of it.
			c.Dispose(clone)

			clone = c.Clone(tc.msg)
			assert.NotSame(t, tc.msg, clone)
			assert.Equal(t, tc.msg, clone)
			tc.wantFull(t, gotIsFull)
		})
	}
}

// Sinks for benchmarks
var (
	msgSink *dns.Msg
)

func BenchmarkClone(b *testing.B) {
	for _, tc := range clonerTestCases {
		b.Run(tc.name, func(b *testing.B) {
			if !tc.handledByClone {
				b.Skip("not handled by dnsmsg.Clone, skipping")
			}

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				msgSink = dnsmsg.Clone(tc.msg)
			}

			require.Equal(b, tc.msg, msgSink)
		})
	}

	// Most recent results:
	//
	// goos: darwin
	// goarch: amd64
	// pkg: github.com/AdguardTeam/AdGuardDNS/internal/dnsmsg
	// cpu: Intel(R) Core(TM) i7-9750H CPU @ 2.60GHz
	// BenchmarkClone/req_a-12						9118897		121.4 ns/op		168 B/op	2 allocs/op
	// BenchmarkClone/resp_a-12						5334396		227.5 ns/op		256 B/op	5 allocs/op
	// BenchmarkClone/resp_a_many-12				4033717		292.6 ns/op		344 B/op	7 allocs/op
	// BenchmarkClone/resp_a_soa-12					4341570		277.6 ns/op		368 B/op	6 allocs/op
	// BenchmarkClone/req_aaaa-12					10129573	120.7 ns/op		168 B/op	2 allocs/op
	// BenchmarkClone/resp_aaaa-12					5004459		244.0 ns/op		264 B/op	5 allocs/op
	// BenchmarkClone/resp_cname_a-12				4403502		286.2 ns/op		320 B/op	6 allocs/op
	// BenchmarkClone/resp_mx-12					5897042		202.6 ns/op		248 B/op	4 allocs/op
	// BenchmarkClone/resp_ptr-12					6380095		202.3 ns/op		232 B/op	4 allocs/op
	// BenchmarkClone/resp_txt-12					4603141		252.0 ns/op		296 B/op	5 allocs/op
	// BenchmarkClone/resp_srv-12					6082226		196.2 ns/op		248 B/op	4 allocs/op
	// BenchmarkClone/resp_not_full-12				6113926		221.0 ns/op		248 B/op	4 allocs/op
	// BenchmarkClone/resp_https-12					1231680		993.9 ns/op		896 B/op	24 allocs/op
	// BenchmarkClone/resp_https_empty_hint-12		3256345		388.6 ns/op		424 B/op	8 allocs/op
	// BenchmarkClone/resp_https_empty_mandatory-12	3727749		327.3 ns/op		384 B/op	7 allocs/op
	// BenchmarkClone/resp_https_empty_values-12	4124431		291.7 ns/op		376 B/op	6 allocs/op
	// BenchmarkClone/resp_a_ede-12					3487443		362.5 ns/op		376 B/op	8 allocs/op
	// BenchmarkClone/resp_a_ecs-12					3461348		354.5 ns/op		384 B/op	8 allocs/op
}

func BenchmarkCloner_Clone(b *testing.B) {
	for _, tc := range clonerTestCases {
		b.Run(tc.name, func(b *testing.B) {
			var gotIsFull bool
			c := dnsmsg.NewCloner(&testClonerStat{
				onOnClone: func(isFull bool) {
					gotIsFull = isFull
				},
			})

			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				msgSink = c.Clone(tc.msg)
				if i < b.N-1 {
					// Don't dispose of the last one to be sure that we can
					// compare that one.
					c.Dispose(msgSink)
				}
			}

			require.Equal(b, tc.msg, msgSink)
			tc.wantFull(b, gotIsFull)
		})
	}

	// Most recent results:
	//
	// goos: darwin
	// goarch: amd64
	// pkg: github.com/AdguardTeam/AdGuardDNS/internal/dnsmsg
	// cpu: Intel(R) Core(TM) i7-9750H CPU @ 2.60GHz
	// BenchmarkCloner_Clone/req_a-12						31715055	38.49 ns/op		0 B/op		0 allocs/op
	// BenchmarkCloner_Clone/resp_a-12						18687192	63.54 ns/op		0 B/op		0 allocs/op
	// BenchmarkCloner_Clone/resp_a_many-12					11271697	106.1 ns/op		0 B/op		0 allocs/op
	// BenchmarkCloner_Clone/resp_a_soa-12					14223168	86.84 ns/op		0 B/op		0 allocs/op
	// BenchmarkCloner_Clone/req_aaaa-12					34427011	36.71 ns/op		0 B/op		0 allocs/op
	// BenchmarkCloner_Clone/resp_aaaa-12					17106201	70.00 ns/op		0 B/op		0 allocs/op
	// BenchmarkCloner_Clone/resp_cname_a-12				13871018	90.77 ns/op		0 B/op		0 allocs/op
	// BenchmarkCloner_Clone/resp_mx-12						19302285	60.72 ns/op		0 B/op		0 allocs/op
	// BenchmarkCloner_Clone/resp_ptr-12					16765956	61.46 ns/op		0 B/op		0 allocs/op
	// BenchmarkCloner_Clone/resp_txt-12					15391646	69.11 ns/op		0 B/op		0 allocs/op
	// BenchmarkCloner_Clone/resp_srv-12					18169714	63.77 ns/op		0 B/op		0 allocs/op
	// BenchmarkCloner_Clone/resp_not_full-12				11640886	96.60 ns/op		64 B/op		1 allocs/op
	// BenchmarkCloner_Clone/resp_https-12					2804174		423.1 ns/op		0 B/op		0 allocs/op
	// BenchmarkCloner_Clone/resp_https_empty_hint-12		9666168		123.2 ns/op		0 B/op		0 allocs/op
	// BenchmarkCloner_Clone/resp_https_empty_mandatory-12	12752148	90.44 ns/op		0 B/op		0 allocs/op
	// BenchmarkCloner_Clone/resp_https_empty_values-12		13501873	82.30 ns/op		0 B/op		0 allocs/op
	// BenchmarkCloner_Clone/resp_https_nil_hint-12			19396582	67.07 ns/op		0 B/op		0 allocs/op
	// BenchmarkCloner_Clone/resp_a_ede-12					10744064	114.9 ns/op		0 B/op		0 allocs/op
	// BenchmarkCloner_Clone/resp_a_ecs-12					10467579	113.8 ns/op		0 B/op		0 allocs/op
	// BenchmarkCloner_Clone/resp_a_ecs_nil-12				13105382	90.48 ns/op		0 B/op		0 allocs/op
}

func FuzzCloner_Clone(f *testing.F) {
	for _, tc := range clonerTestCases {
		b, err := tc.msg.Pack()
		require.NoError(f, err)

		f.Add(b)
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		msg := &dns.Msg{}
		err := msg.Unpack(input)
		if err != nil || len(msg.Question) != 1 {
			return
		}

		var gotIsFull bool
		c := dnsmsg.NewCloner(&testClonerStat{
			onOnClone: func(isFull bool) {
				gotIsFull = isFull
			},
		})

		clone := c.Clone(msg)
		if !gotIsFull {
			// TODO(a.garipov): Currently we cannot analyze partial clones,
			// because these may contain e.g. HTTPS records in Ns fields, which
			// [dns.Copy] doesn't clone properly due to nilness issues.
			// Consider changing the code to fix that.
			return
		}

		assert.Equal(t, msg, clone)

		c.Dispose(clone)
		clone = c.Clone(msg)

		require.True(t, gotIsFull)

		assert.Equal(t, msg, clone)
	})
}
