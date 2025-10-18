package admission

import (
	"context"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/l0p7/passctrl/internal/runtime/pipeline"
)

func mustPrefix(t *testing.T, cidr string) netip.Prefix {
	t.Helper()
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		t.Fatalf("parse prefix %q: %v", cidr, err)
	}
	return prefix
}

func TestAgentRejectsForwardedChainWithUntrustedProxy(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/auth", nil)
	req.RemoteAddr = "192.0.2.10:443"
	req.Header.Set("X-Forwarded-For", "198.51.100.5, 203.0.113.7, 192.0.2.10")

	agent := New([]netip.Prefix{mustPrefix(t, "192.0.2.0/24")}, false)
	state := pipeline.NewState(req, "endpoint", "cache", "corr")

	res := agent.Execute(context.Background(), req, state)

	if res.Status != "fail" {
		t.Fatalf("expected status fail, got %q", res.Status)
	}
	if state.Admission.Reason != "forwarded chain includes untrusted proxy" {
		t.Fatalf("expected reason for untrusted chain, got %q", state.Admission.Reason)
	}
	if state.Admission.ProxyNote != "forwarded headers stripped due to untrusted proxy chain" {
		t.Fatalf("expected proxy note about untrusted chain, got %q", state.Admission.ProxyNote)
	}
	if state.Admission.Authenticated {
		t.Fatalf("expected authentication to fail when proxies are untrusted")
	}
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

	if res.Status != "pass" {
		t.Fatalf("expected status pass, got %q", res.Status)
	}
	if !state.Admission.TrustedProxy {
		t.Fatalf("expected trusted proxy to be recorded")
	}
	if state.Admission.ClientIP != "198.51.100.5" {
		t.Fatalf("expected client ip from forwarded chain, got %q", state.Admission.ClientIP)
	}
	if state.Admission.Decision != "pass" {
		t.Fatalf("expected admission decision pass, got %q", state.Admission.Decision)
	}
}

func TestAgentDevelopmentModeStripsInvalidForwarded(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/auth", nil)
	req.RemoteAddr = "192.0.2.10:80"
	req.Header.Set("X-Forwarded-For", "not an ip")
	req.Header.Set("Authorization", "Bearer dev")

	agent := New([]netip.Prefix{mustPrefix(t, "192.0.2.0/24")}, true)
	state := pipeline.NewState(req, "endpoint", "cache", "corr")

	res := agent.Execute(context.Background(), req, state)

	if res.Status != "pass" {
		t.Fatalf("expected status pass in development mode, got %q", res.Status)
	}
	if !state.Admission.ProxyStripped {
		t.Fatalf("expected forwarded headers to be stripped in development mode")
	}
	if state.Admission.ForwardedFor != "" || req.Header.Get("X-Forwarded-For") != "" {
		t.Fatalf("expected forwarded for header to be cleared")
	}
	if state.Admission.ProxyNote == "" {
		t.Fatalf("expected proxy note to explain stripping")
	}
}

func TestPrepareForwardedMetadataMismatch(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/auth", nil)
	req.Header.Set("Forwarded", `for=203.0.113.7, for=198.51.100.5`)
	req.Header.Set("X-Forwarded-For", "198.51.100.5, 203.0.113.7")

	agent := New(nil, false)
	state := pipeline.NewState(req, "endpoint", "cache", "corr")
	state.Admission.Forwarded = req.Header.Get("Forwarded")
	state.Admission.ForwardedFor = req.Header.Get("X-Forwarded-For")

	if _, err := agent.prepareForwardedMetadata(req, state); err == nil || err != errForwardedMetadata {
		t.Fatalf("expected errForwardedMetadata mismatch, got %v", err)
	}
}

func TestParseRFC7239Forwarded(t *testing.T) {
	header := `for="2001:db8::1"; proto=https; host=example.com, for=203.0.113.7:443`
	addrs, sanitized, err := parseRFC7239Forwarded(header)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(addrs) != 2 {
		t.Fatalf("expected two addresses, got %v", addrs)
	}
	if !strings.Contains(sanitized, `for="2001:db8::1"`) || !strings.Contains(sanitized, "proto=https") {
		t.Fatalf("unexpected sanitized header: %q", sanitized)
	}
}

func TestParseRFC7239ForwardedMissingFor(t *testing.T) {
	if _, _, err := parseRFC7239Forwarded("proto=https"); err != errForwardedDirectiveEmpty {
		t.Fatalf("expected errForwardedDirectiveEmpty, got %v", err)
	}
}

func TestParseForwardedChainAndEntry(t *testing.T) {
	addrs, err := parseForwardedChain("198.51.100.5, 2001:db8::1, 203.0.113.7:80")
	if err != nil {
		t.Fatalf("unexpected chain parse error: %v", err)
	}
	if len(addrs) != 3 {
		t.Fatalf("expected three entries, got %d", len(addrs))
	}
	if addrs[1].String() != "2001:db8::1" {
		t.Fatalf("expected ipv6 normalization, got %s", addrs[1].String())
	}
	if _, err := parseForwardedEntry("not-an-ip"); err == nil {
		t.Fatalf("expected invalid entry error")
	}
}

func TestJoinForwardedAddrs(t *testing.T) {
	addrs := []netip.Addr{
		netip.MustParseAddr("198.51.100.5"),
		netip.MustParseAddr("203.0.113.7"),
	}
	if joined := joinForwardedAddrs(addrs); joined != "198.51.100.5, 203.0.113.7" {
		t.Fatalf("unexpected join result: %q", joined)
	}
	if joined := joinForwardedAddrs(nil); joined != "" {
		t.Fatalf("expected empty join for nil slice, got %q", joined)
	}
}

func TestForwardedChainsEqual(t *testing.T) {
	a := []netip.Addr{netip.MustParseAddr("198.51.100.5")}
	b := []netip.Addr{netip.MustParseAddr("198.51.100.5")}
	if !forwardedChainsEqual(a, b) {
		t.Fatalf("expected slices to be equal")
	}
	b = append(b, netip.MustParseAddr("203.0.113.7"))
	if forwardedChainsEqual(a, b) {
		t.Fatalf("expected slices of different length to differ")
	}
}

func TestAnnotateReason(t *testing.T) {
	if got := annotateReason("base reason", ""); got != "base reason" {
		t.Fatalf("unexpected reason without note: %q", got)
	}
	if got := annotateReason("base reason", " note "); got != "base reason (note)" {
		t.Fatalf("unexpected reason with note: %q", got)
	}
}

func TestRemoteHostAndParseRemoteIP(t *testing.T) {
	if host := remoteHost("203.0.113.7:8080"); host != "203.0.113.7" {
		t.Fatalf("unexpected remote host: %q", host)
	}
	if host := remoteHost("missing-port"); host != "missing-port" {
		t.Fatalf("expected raw value when port parsing fails, got %q", host)
	}
	if _, err := parseRemoteIP(""); err == nil {
		t.Fatalf("expected error when remote address empty")
	}
	if _, err := parseRemoteIP("not-an-ip:80"); err == nil {
		t.Fatalf("expected error for invalid ip")
	}
}

func TestParseCIDRs(t *testing.T) {
	input := []string{"192.0.2.0/24", "invalid", " 203.0.113.0/24 "}
	prefixes := ParseCIDRs(input)
	if len(prefixes) != 2 {
		t.Fatalf("expected two valid prefixes, got %d", len(prefixes))
	}
	if prefixes[0].String() != "192.0.2.0/24" {
		t.Fatalf("unexpected prefix order, got %s", prefixes[0])
	}
}

func TestStripForwardedHeaders(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/auth", nil)
	req.Header.Set("Forwarded", "for=198.51.100.5")
	req.Header.Set("X-Forwarded-For", "198.51.100.5")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Other", "value")

	stripForwardedHeaders(req)

	if req.Header.Get("Forwarded") != "" || req.Header.Get("X-Forwarded-For") != "" || req.Header.Get("X-Forwarded-Proto") != "" {
		t.Fatalf("expected forwarded headers to be stripped")
	}
	if req.Header.Get("X-Other") != "value" {
		t.Fatalf("expected unrelated headers to remain")
	}
}
