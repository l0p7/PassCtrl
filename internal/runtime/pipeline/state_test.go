package pipeline

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewStateInitializesNormalization(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.com/foo/bar?Foo=bar&Zap=zazz", http.NoBody)
	req.Header.Set("X-Custom", "primary")
	req.Header.Add("X-Custom", "secondary")
	req.Header.Set("X-Another", "value")

	state := NewState(req, "demo-endpoint", "cache-key-1", "corr-123")

	if state.Endpoint != "demo-endpoint" {
		t.Fatalf("expected endpoint to be captured, got %q", state.Endpoint)
	}
	if state.Cache.Key != "cache-key-1" {
		t.Fatalf("expected cache key to be propagated, got %q", state.Cache.Key)
	}
	if got := state.Raw.Headers["x-custom"]; got != "primary" {
		t.Fatalf("expected normalized header to keep first value, got %q", got)
	}
	if _, ok := state.Raw.Headers["X-Custom"]; ok {
		t.Fatalf("expected raw headers to store lowercase keys only")
	}
	if got := state.Raw.Query["foo"]; got != "bar" {
		t.Fatalf("expected query map to use lowercase key, got %q", got)
	}
	if _, ok := state.Raw.Query["Foo"]; ok {
		t.Fatalf("expected query map to store lowercase keys only")
	}
	if state.Forward.Headers == nil || state.Forward.Query == nil {
		t.Fatalf("expected forward maps to be initialized")
	}
	if state.Response.Headers == nil {
		t.Fatalf("expected response headers to be initialized")
	}
	if state.Backend.Headers == nil {
		t.Fatalf("expected backend headers to be initialized")
	}
}

func TestPlanAccessors(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/demo", http.NoBody)
	state := NewState(req, "demo", "cache-key", "corr")

	if state.Plan() != nil {
		t.Fatalf("expected initial plan to be nil")
	}
	state.SetPlan("plan-value")
	if got := state.Plan(); got != "plan-value" {
		t.Fatalf("expected plan getter to return stored value, got %#v", got)
	}
	state.ClearPlan()
	if state.Plan() != nil {
		t.Fatalf("expected plan to be cleared")
	}
}

func TestTemplateContextIncludesStateSnapshot(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/demo", http.NoBody)
	state := NewState(req, "endpoint-a", "cache-key", "corr-id")
	state.Server.PipelineReady = true
	state.Rule.Outcome = "pass"
	state.Rule.Reason = "allowed"
	state.Response.Status = http.StatusOK
	state.Response.Message = "ok"

	ctx := state.TemplateContext()

	if ctx["endpoint"] != "endpoint-a" {
		t.Fatalf("expected endpoint in template context, got %#v", ctx["endpoint"])
	}
	if ctx["correlationId"] != "corr-id" {
		t.Fatalf("expected correlation id in template context, got %#v", ctx["correlationId"])
	}
	if ctx["state"] != state {
		t.Fatalf("expected state to be embedded in template context")
	}
	if cache, ok := ctx["cache"].(CacheState); !ok || cache.Key != "cache-key" {
		t.Fatalf("expected cache snapshot with key, got %#v", ctx["cache"])
	}
}

func TestTemplateContextNilState(t *testing.T) {
	var state *State
	ctx := state.TemplateContext()
	if ctx == nil {
		t.Fatalf("expected nil state to return empty map, got nil")
	}
	if len(ctx) != 0 {
		t.Fatalf("expected empty context for nil state, got %#v", ctx)
	}
}

func TestStateCacheKeyAccessor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/demo", http.NoBody)
	state := NewState(req, "demo", "cache-key-test", "corr")
	if state.CacheKey() != "cache-key-test" {
		t.Fatalf("expected cache key accessor to return cache-key-test, got %q", state.CacheKey())
	}
}
