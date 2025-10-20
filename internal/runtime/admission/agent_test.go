package admission

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustPrefix(t *testing.T, cidr string) netip.Prefix {
	t.Helper()
	prefix, err := netip.ParsePrefix(cidr)
	require.NoErrorf(t, err, "parse prefix %q", cidr)
	return prefix
}

func mustPrefixes(t *testing.T, cidrs []string) []netip.Prefix {
	t.Helper()
	prefixes := make([]netip.Prefix, len(cidrs))
	for i, cidr := range cidrs {
		prefixes[i] = mustPrefix(t, cidr)
	}
	return prefixes
}

func TestAgentExecute(t *testing.T) {
	tests := []struct {
		name         string
		trustedCIDRs []string
		development  bool
		setup        func(t *testing.T) (*http.Request, *pipeline.State)
		expect       func(t *testing.T, res pipeline.Result, state *pipeline.State, req *http.Request)
	}{
		{
			name:         "rejects forwarded chain when proxy untrusted",
			trustedCIDRs: []string{"192.0.2.0/24"},
			setup: func(t *testing.T) (*http.Request, *pipeline.State) {
				req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", nil)
				req.RemoteAddr = "192.0.2.10:443"
				req.Header.Set("X-Forwarded-For", "198.51.100.5, 203.0.113.7, 192.0.2.10")
				state := pipeline.NewState(req, "endpoint", "cache", "corr")
				return req, state
			},
			expect: func(t *testing.T, res pipeline.Result, state *pipeline.State, _ *http.Request) {
				require.Equal(t, "fail", res.Status)
				require.Equal(t, "forwarded chain includes untrusted proxy", state.Admission.Reason)
				require.Equal(t, "forwarded headers stripped due to untrusted proxy chain", state.Admission.ProxyNote)
				require.False(t, state.Admission.Authenticated, "expected authentication to fail when proxies are untrusted")
			},
		},
		{
			name:         "accepts forwarded chain from trusted proxies",
			trustedCIDRs: []string{"192.0.2.0/24", "203.0.113.0/24"},
			setup: func(t *testing.T) (*http.Request, *pipeline.State) {
				req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", nil)
				req.RemoteAddr = "192.0.2.10:80"
				req.Header.Set("Authorization", "Bearer token")
				req.Header.Set("X-Forwarded-For", "198.51.100.5, 203.0.113.7, 192.0.2.10")
				state := pipeline.NewState(req, "endpoint", "cache", "corr")
				return req, state
			},
			expect: func(t *testing.T, res pipeline.Result, state *pipeline.State, _ *http.Request) {
				require.Equal(t, "pass", res.Status)
				require.True(t, state.Admission.TrustedProxy, "expected trusted proxy to be recorded")
				require.Equal(t, "198.51.100.5", state.Admission.ClientIP)
				require.Equal(t, "pass", state.Admission.Decision)
			},
		},
		{
			name:         "development mode strips invalid forwarded metadata",
			trustedCIDRs: []string{"192.0.2.0/24"},
			development:  true,
			setup: func(t *testing.T) (*http.Request, *pipeline.State) {
				req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", nil)
				req.RemoteAddr = "192.0.2.10:80"
				req.Header.Set("Authorization", "Bearer dev")
				req.Header.Set("X-Forwarded-For", "not an ip")
				state := pipeline.NewState(req, "endpoint", "cache", "corr")
				return req, state
			},
			expect: func(t *testing.T, res pipeline.Result, state *pipeline.State, req *http.Request) {
				require.Equal(t, "pass", res.Status)
				require.True(t, state.Admission.ProxyStripped, "expected forwarded headers to be stripped in development mode")
				require.Empty(t, state.Admission.ForwardedFor)
				require.Empty(t, req.Header.Get("X-Forwarded-For"))
				require.NotEmpty(t, state.Admission.ProxyNote)
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req, state := tc.setup(t)
			prefixes := mustPrefixes(t, tc.trustedCIDRs)
			agent := New(prefixes, tc.development)

			res := agent.Execute(context.Background(), req, state)

			tc.expect(t, res, state, req)
		})
	}
}

func TestPrepareForwardedMetadata(t *testing.T) {
	tests := []struct {
		name               string
		forwarded          string
		forwardedFor       string
		wantErr            error
		wantChain          []string
		expectForwarded    string
		expectForwardedFor string
	}{
		{
			name:         "mismatch between forwarded headers",
			forwarded:    `for=203.0.113.7, for=198.51.100.5`,
			forwardedFor: "198.51.100.5, 203.0.113.7",
			wantErr:      errForwardedMetadata,
		},
		{
			name:               "returns sanitized forwarded metadata when consistent",
			forwarded:          `for=203.0.113.7;proto=https`,
			forwardedFor:       "203.0.113.7",
			wantChain:          []string{"203.0.113.7"},
			expectForwarded:    `for=203.0.113.7; proto=https`,
			expectForwardedFor: "203.0.113.7",
		},
	}

	agent := New(nil, false)

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", nil)
			if tc.forwarded != "" {
				req.Header.Set("Forwarded", tc.forwarded)
			}
			if tc.forwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tc.forwardedFor)
			}

			state := pipeline.NewState(req, "endpoint", "cache", "corr")
			state.Admission.Forwarded = tc.forwarded
			state.Admission.ForwardedFor = tc.forwardedFor

			chain, err := agent.prepareForwardedMetadata(req, state)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}

			require.NoError(t, err)
			require.Len(t, chain, len(tc.wantChain))
			for i, addr := range tc.wantChain {
				require.Equal(t, addr, chain[i].String())
			}
			if tc.expectForwarded != "" {
				require.Equal(t, tc.expectForwarded, state.Admission.Forwarded)
				require.Equal(t, tc.expectForwarded, req.Header.Get("Forwarded"))
			}
			if tc.expectForwardedFor != "" {
				require.Equal(t, tc.expectForwardedFor, state.Admission.ForwardedFor)
				require.Equal(t, tc.expectForwardedFor, req.Header.Get("X-Forwarded-For"))
			}
		})
	}
}

func TestParseRFC7239Forwarded(t *testing.T) {
	tests := []struct {
		name          string
		header        string
		wantAddrs     []string
		wantFragments []string
		wantErr       error
	}{
		{
			name:          "parses valid header",
			header:        `for="2001:db8::1"; proto=https; host=example.com, for=203.0.113.7:443`,
			wantAddrs:     []string{"2001:db8::1", "203.0.113.7"},
			wantFragments: []string{`for="2001:db8::1"`, "proto=https"},
		},
		{
			name:    "errors when for directive missing",
			header:  "proto=https",
			wantErr: errForwardedDirectiveEmpty,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			addrs, sanitized, err := parseRFC7239Forwarded(tc.header)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}

			require.NoError(t, err)
			require.Len(t, addrs, len(tc.wantAddrs))
			for i, addr := range tc.wantAddrs {
				require.Equal(t, addr, addrs[i].String())
			}
			for _, fragment := range tc.wantFragments {
				assert.Contains(t, sanitized, fragment)
			}
		})
	}
}

func TestParseForwardedChain(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{
			name:  "parses IPv4 and IPv6 entries",
			input: "198.51.100.5, 2001:db8::1, 203.0.113.7:80",
			want:  []string{"198.51.100.5", "2001:db8::1", "203.0.113.7"},
		},
		{
			name:    "fails on invalid entry",
			input:   "not-an-ip",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			addrs, err := parseForwardedChain(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Len(t, addrs, len(tc.want))
			for i, addr := range tc.want {
				require.Equal(t, addr, addrs[i].String())
			}
		})
	}
}

func TestParseForwardedEntry(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "parses IPv4", input: "198.51.100.5", want: "198.51.100.5"},
		{name: "parses IPv6", input: "2001:db8::1", want: "2001:db8::1"},
		{name: "parses host with port", input: "203.0.113.7:8080", want: "203.0.113.7"},
		{name: "errors on invalid", input: "not-an-ip", wantErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			addr, err := parseForwardedEntry(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, addr.String())
		})
	}
}

func TestJoinForwardedAddrs(t *testing.T) {
	tests := []struct {
		name  string
		input []netip.Addr
		want  string
	}{
		{
			name:  "joins addresses",
			input: []netip.Addr{netip.MustParseAddr("198.51.100.5"), netip.MustParseAddr("203.0.113.7")},
			want:  "198.51.100.5, 203.0.113.7",
		},
		{
			name:  "handles empty slice",
			input: nil,
			want:  "",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, joinForwardedAddrs(tc.input))
		})
	}
}

func TestForwardedChainsEqual(t *testing.T) {
	tests := []struct {
		name string
		a    []netip.Addr
		b    []netip.Addr
		want bool
	}{
		{
			name: "equal chains",
			a:    []netip.Addr{netip.MustParseAddr("198.51.100.5")},
			b:    []netip.Addr{netip.MustParseAddr("198.51.100.5")},
			want: true,
		},
		{
			name: "different lengths",
			a:    []netip.Addr{netip.MustParseAddr("198.51.100.5")},
			b:    []netip.Addr{netip.MustParseAddr("198.51.100.5"), netip.MustParseAddr("203.0.113.7")},
			want: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, forwardedChainsEqual(tc.a, tc.b))
		})
	}
}

func TestAnnotateReason(t *testing.T) {
	tests := []struct {
		name string
		base string
		note string
		want string
	}{
		{name: "returns base when note empty", base: "base reason", note: "", want: "base reason"},
		{name: "adds note when present", base: "base reason", note: " note ", want: "base reason (note)"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, annotateReason(tc.base, tc.note))
		})
	}
}

func TestRemoteHostAndParseRemoteIP(t *testing.T) {
	hostTests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "extracts host", input: "203.0.113.7:8080", want: "203.0.113.7"},
		{name: "returns original when missing port", input: "missing-port", want: "missing-port"},
	}

	for _, tc := range hostTests {
		tc := tc
		t.Run("remote host:"+tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, remoteHost(tc.input))
		})
	}

	ipTests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "empty", input: "", wantErr: true},
		{name: "invalid", input: "not-an-ip:80", wantErr: true},
		{name: "valid", input: "203.0.113.7:443"},
	}

	for _, tc := range ipTests {
		tc := tc
		t.Run("parse remote ip:"+tc.name, func(t *testing.T) {
			addr, err := parseRemoteIP(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "203.0.113.7", addr.String())
		})
	}
}

func TestParseCIDRs(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "filters invalid entries",
			input: []string{"192.0.2.0/24", "invalid", " 203.0.113.0/24 "},
			want:  []string{"192.0.2.0/24", "203.0.113.0/24"},
		},
		{
			name:  "handles empty slice",
			input: nil,
			want:  nil,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			prefixes := ParseCIDRs(tc.input)
			require.Len(t, prefixes, len(tc.want))
			for i, prefix := range tc.want {
				require.Equal(t, prefix, prefixes[i].String())
			}
		})
	}
}

func TestStripForwardedHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", nil)
	forwardedHeaders := map[string]string{
		"Forwarded":          "for=198.51.100.5",
		"X-Forwarded-For":    "198.51.100.5",
		"X-Forwarded-Proto":  "https",
		"X-Forwarded-Host":   "example.com",
		"X-Forwarded-Port":   "443",
		"X-Forwarded-Prefix": "/edge",
		"X-Other":            "value",
	}
	for name, value := range forwardedHeaders {
		req.Header.Set(name, value)
	}

	stripForwardedHeaders(req)

	removalTests := []struct {
		name   string
		header string
		want   string
	}{
		{name: "forwarded", header: "Forwarded", want: ""},
		{name: "forwarded-for", header: "X-Forwarded-For", want: ""},
		{name: "forwarded-proto", header: "X-Forwarded-Proto", want: ""},
		{name: "forwarded-host", header: "X-Forwarded-Host", want: ""},
		{name: "forwarded-port", header: "X-Forwarded-Port", want: ""},
		{name: "forwarded-prefix", header: "X-Forwarded-Prefix", want: ""},
		{name: "other", header: "X-Other", want: "value"},
	}

	for _, tc := range removalTests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, req.Header.Get(tc.header))
		})
	}
}
