package forwardpolicy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/stretchr/testify/require"
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

		require.Equal(t, "curated", res.Status)
		require.NotContains(t, state.Forward.Headers, "x-unrelated")
		require.Equal(t, "bearer", state.Forward.Headers["authorization"])
		require.Equal(t, "true", state.Forward.Query["allow"])
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

		require.NotContains(t, state.Forward.Headers, "x-passctrl-deny")
		require.NotContains(t, state.Forward.Query, "deny")
		require.Equal(t, "token", state.Forward.Headers["authorization"])
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

		require.Equal(t, "static-value", state.Forward.Headers["x-static"])
		require.NotContains(t, state.Forward.Headers, "x-empty")
		require.Equal(t, "12345", state.Forward.Query["ticket"])
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

		require.Equal(t, "203.0.113.5", state.Forward.Headers["x-forwarded-for"])
		require.Equal(t, "https", state.Forward.Headers["x-forwarded-proto"])
		require.Equal(t, "/edge", state.Forward.Headers["x-forwarded-prefix"])
		require.Equal(t, "for=203.0.113.5;proto=https", state.Forward.Headers["forwarded"])
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

		require.NotContains(t, state.Forward.Headers, "authorization")
		require.Equal(t, "value", state.Forward.Headers["x-custom"])
		require.NotContains(t, state.Forward.Query, "ignore")
		require.Equal(t, "true", state.Forward.Query["keep"])
	})
}
