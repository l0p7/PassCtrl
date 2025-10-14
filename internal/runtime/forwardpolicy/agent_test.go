package forwardpolicy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/l0p7/passctrl/internal/runtime/pipeline"
)

func newTestState(req *http.Request) *pipeline.State {
	return pipeline.NewState(req, "test", "test|key", "")
}

func TestAgentExecute(t *testing.T) {
	t.Run("curates default allow lists", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth?allow=true&ignore=nope", http.NoBody)
		req.Header.Set("Authorization", "bearer")
		req.Header.Set("X-Unrelated", "value")

		state := newTestState(req)
		agent := New(DefaultConfig())

		res := agent.Execute(req.Context(), req, state)

		if res.Status != "curated" {
			t.Fatalf("unexpected status: %s", res.Status)
		}
		if _, ok := state.Forward.Headers["x-unrelated"]; ok {
			t.Fatalf("unexpected header leaked into forward state")
		}
		if state.Forward.Headers["authorization"] != "bearer" {
			t.Fatalf("authorization header missing from curated set")
		}
		if state.Forward.Query["allow"] != "true" {
			t.Fatalf("expected allow toggle to propagate into query state")
		}
	})

	t.Run("strips configured headers and query params", func(t *testing.T) {
		cfg := Config{
			Headers: CategoryConfig{
				Allow: []string{"authorization", "x-passctrl-deny"},
				Strip: []string{"x-passctrl-deny"},
			},
			Query: CategoryConfig{
				Allow: []string{"allow", "deny"},
				Strip: []string{"deny"},
			},
		}
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth?allow=true&deny=true", http.NoBody)
		req.Header.Set("Authorization", "token")
		req.Header.Set("X-PassCtrl-Deny", "true")

		state := newTestState(req)
		agent := New(cfg)
		agent.Execute(req.Context(), req, state)

		if _, ok := state.Forward.Headers["x-passctrl-deny"]; ok {
			t.Fatalf("expected strip list to remove deny header")
		}
		if _, ok := state.Forward.Query["deny"]; ok {
			t.Fatalf("expected strip list to remove deny query parameter")
		}
		if state.Forward.Headers["authorization"] != "token" {
			t.Fatalf("authorization header should remain after strip applied")
		}
	})

	t.Run("custom entries override curated view", func(t *testing.T) {
		cfg := Config{
			Headers: CategoryConfig{
				Custom: map[string]string{
					"X-Static": "static-value",
					"X-Empty":  "   ",
				},
			},
			Query: CategoryConfig{
				Custom: map[string]string{
					"ticket": " 12345 ",
				},
			},
		}
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)

		state := newTestState(req)
		agent := New(cfg)
		agent.Execute(req.Context(), req, state)

		if state.Forward.Headers["x-static"] != "static-value" {
			t.Fatalf("expected custom header to be injected")
		}
		if _, ok := state.Forward.Headers["x-empty"]; ok {
			t.Fatalf("blank custom header values should be omitted")
		}
		if state.Forward.Query["ticket"] != "12345" {
			t.Fatalf("expected custom query value to be trimmed")
		}
	})

	t.Run("forward proxy headers when enabled", func(t *testing.T) {
		cfg := Config{ForwardProxyHeaders: true}
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		req.Header.Set("X-Forwarded-For", "203.0.113.5")
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-Prefix", "/edge")
		req.Header.Set("Forwarded", "for=203.0.113.5;proto=https")

		state := newTestState(req)
		agent := New(cfg)
		agent.Execute(req.Context(), req, state)

		if state.Forward.Headers["x-forwarded-for"] != "203.0.113.5" {
			t.Fatalf("expected forwarded header to be exposed when toggle enabled")
		}
		if state.Forward.Headers["x-forwarded-proto"] != "https" {
			t.Fatalf("expected proto header to be exposed when toggle enabled")
		}
		if state.Forward.Headers["x-forwarded-prefix"] != "/edge" {
			t.Fatalf("expected prefix header to be exposed when toggle enabled")
		}
		if state.Forward.Headers["forwarded"] != "for=203.0.113.5;proto=https" {
			t.Fatalf("expected RFC7239 forwarded header to be exposed when toggle enabled")
		}
	})

	t.Run("wildcard allow applies before strips", func(t *testing.T) {
		cfg := Config{
			Headers: CategoryConfig{
				Allow: []string{"*"},
				Strip: []string{"authorization"},
			},
			Query: CategoryConfig{
				Allow: []string{"*"},
				Strip: []string{"ignore"},
			},
		}
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth?keep=true&ignore=true", http.NoBody)
		req.Header.Set("Authorization", "token")
		req.Header.Set("X-Custom", "value")

		state := newTestState(req)
		agent := New(cfg)
		agent.Execute(req.Context(), req, state)

		if _, ok := state.Forward.Headers["authorization"]; ok {
			t.Fatalf("strip should remove authorization even when wildcard allow is set")
		}
		if state.Forward.Headers["x-custom"] != "value" {
			t.Fatalf("wildcard allow should forward other headers")
		}
		if _, ok := state.Forward.Query["ignore"]; ok {
			t.Fatalf("strip should remove ignore query parameter")
		}
		if state.Forward.Query["keep"] != "true" {
			t.Fatalf("expected keep query to survive wildcard allow")
		}
	})
}
