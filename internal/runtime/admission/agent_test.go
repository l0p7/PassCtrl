package admission

import (
	"context"
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

func TestAgentRejectsForwardedChainWithUntrustedProxy(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/auth", nil)
	req.RemoteAddr = "192.0.2.10:443"
	req.Header.Set("X-Forwarded-For", "198.51.100.5, 203.0.113.7, 192.0.2.10")

	agent := New([]netip.Prefix{mustPrefix(t, "192.0.2.0/24")}, false)
	state := pipeline.NewState(req, "endpoint", "cache", "corr")

	res := agent.Execute(context.Background(), req, state)

	require.Equal(t, "fail", res.Status)
	require.Equal(t, "forwarded chain includes untrusted proxy", state.Admission.Reason)
	require.Equal(t, "forwarded headers stripped due to untrusted proxy chain", state.Admission.ProxyNote)
	require.False(t, state.Admission.Authenticated, "expected authentication to fail when proxies are untrusted")
}

func TestAgentAcceptsForwardedChainFromTrustedProxies(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/auth", nil)
	req.RemoteAddr = "192.0.2.10:80"
	req.Header.Set("X-Forwarded-For", "198.51.100.5, 203.0.113.7, 192.0.2.10")
	req.Header.Set("Authorization", "Bearer token")

	trusted := []netip.Prefix{
		mustPrefix(t, "192.0.2.0/24"),
		mustPrefix(t, "203.0.113.0/24"),
	}
	agent := New(trusted, false)
	state := pipeline.NewState(req, "endpoint", "cache", "corr")

	res := agent.Execute(context.Background(), req, state)

	require.Equal(t, "pass", res.Status)
	require.True(t, state.Admission.TrustedProxy, "expected trusted proxy to be recorded")
	require.Equal(t, "198.51.100.5", state.Admission.ClientIP)
	require.Equal(t, "pass", state.Admission.Decision)
}

func TestAgentDevelopmentModeStripsInvalidForwarded(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/auth", nil)
	req.RemoteAddr = "192.0.2.10:80"
	req.Header.Set("X-Forwarded-For", "not an ip")
	req.Header.Set("Authorization", "Bearer dev")

	agent := New([]netip.Prefix{mustPrefix(t, "192.0.2.0/24")}, true)
	state := pipeline.NewState(req, "endpoint", "cache", "corr")

	res := agent.Execute(context.Background(), req, state)

	require.Equal(t, "pass", res.Status)
	require.True(t, state.Admission.ProxyStripped, "expected forwarded headers to be stripped in development mode")
	require.Empty(t, state.Admission.ForwardedFor)
	require.Empty(t, req.Header.Get("X-Forwarded-For"))
	require.NotEmpty(t, state.Admission.ProxyNote)
}

func TestPrepareForwardedMetadataMismatch(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/auth", nil)
	req.Header.Set("Forwarded", `for=203.0.113.7, for=198.51.100.5`)
	req.Header.Set("X-Forwarded-For", "198.51.100.5, 203.0.113.7")

	agent := New(nil, false)
	state := pipeline.NewState(req, "endpoint", "cache", "corr")
	state.Admission.Forwarded = req.Header.Get("Forwarded")
	state.Admission.ForwardedFor = req.Header.Get("X-Forwarded-For")

	_, err := agent.prepareForwardedMetadata(req, state)
	require.ErrorIs(t, err, errForwardedMetadata)
}

func TestParseRFC7239Forwarded(t *testing.T) {
	header := `for="2001:db8::1"; proto=https; host=example.com, for=203.0.113.7:443`
	addrs, sanitized, err := parseRFC7239Forwarded(header)
	require.NoError(t, err)
	require.Len(t, addrs, 2)
	assert.Contains(t, sanitized, `for="2001:db8::1"`)
	assert.Contains(t, sanitized, "proto=https")
}

func TestParseRFC7239ForwardedMissingFor(t *testing.T) {
	_, _, err := parseRFC7239Forwarded("proto=https")
	require.ErrorIs(t, err, errForwardedDirectiveEmpty)
}

func TestParseForwardedChainAndEntry(t *testing.T) {
	addrs, err := parseForwardedChain("198.51.100.5, 2001:db8::1, 203.0.113.7:80")
	require.NoError(t, err)
	require.Len(t, addrs, 3)
	require.Equal(t, "2001:db8::1", addrs[1].String())
	_, err = parseForwardedEntry("not-an-ip")
	require.Error(t, err)
}

func TestJoinForwardedAddrs(t *testing.T) {
	addrs := []netip.Addr{
		netip.MustParseAddr("198.51.100.5"),
		netip.MustParseAddr("203.0.113.7"),
	}
	require.Equal(t, "198.51.100.5, 203.0.113.7", joinForwardedAddrs(addrs))
	require.Empty(t, joinForwardedAddrs(nil))
}

func TestForwardedChainsEqual(t *testing.T) {
	a := []netip.Addr{netip.MustParseAddr("198.51.100.5")}
	b := []netip.Addr{netip.MustParseAddr("198.51.100.5")}
	require.True(t, forwardedChainsEqual(a, b))
	b = append(b, netip.MustParseAddr("203.0.113.7"))
	require.False(t, forwardedChainsEqual(a, b))
}

func TestAnnotateReason(t *testing.T) {
	require.Equal(t, "base reason", annotateReason("base reason", ""))
	require.Equal(t, "base reason (note)", annotateReason("base reason", " note "))
}

func TestRemoteHostAndParseRemoteIP(t *testing.T) {
	require.Equal(t, "203.0.113.7", remoteHost("203.0.113.7:8080"))
	require.Equal(t, "missing-port", remoteHost("missing-port"))
	_, err := parseRemoteIP("")
	require.Error(t, err)
	_, err = parseRemoteIP("not-an-ip:80")
	require.Error(t, err)
}

func TestParseCIDRs(t *testing.T) {
	input := []string{"192.0.2.0/24", "invalid", " 203.0.113.0/24 "}
	prefixes := ParseCIDRs(input)
	require.Len(t, prefixes, 2)
	require.Equal(t, "192.0.2.0/24", prefixes[0].String())
}

func TestStripForwardedHeaders(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/auth", nil)
	req.Header.Set("Forwarded", "for=198.51.100.5")
	req.Header.Set("X-Forwarded-For", "198.51.100.5")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Other", "value")

	stripForwardedHeaders(req)

	require.Empty(t, req.Header.Get("Forwarded"))
	require.Empty(t, req.Header.Get("X-Forwarded-For"))
	require.Empty(t, req.Header.Get("X-Forwarded-Proto"))
	require.Equal(t, "value", req.Header.Get("X-Other"))
}
