package admission

import (
	"context"
	"net/http/httptest"
	"net/netip"
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
