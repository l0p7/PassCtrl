package forwardpolicy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/templates"
	"github.com/stretchr/testify/require"
)

func newTestState(req *http.Request) *pipeline.State {
	return pipeline.NewState(req, "test", "test|key", "")
}

func TestAgentExecute(t *testing.T) {
	tests := []struct {
		name   string
		cfg    Config
		req    func() *http.Request
		expect func(t *testing.T, res pipeline.Result, state *pipeline.State)
	}{
		{
			name: "curates default allow lists",
			cfg:  DefaultConfig(),
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "http://example.com/auth?allow=true&ignore=nope", http.NoBody)
				req.Header.Set("Authorization", "bearer")
				req.Header.Set("X-Unrelated", "value")
				return req
			},
			expect: func(t *testing.T, res pipeline.Result, state *pipeline.State) {
				require.Equal(t, "curated", res.Status)
				require.NotContains(t, state.Forward.Headers, "x-unrelated")
				require.Equal(t, "bearer", state.Forward.Headers["authorization"])
				require.Equal(t, "true", state.Forward.Query["allow"])
			},
		},
		{
			name: "strips configured headers and query params",
			cfg: Config{
				Headers: CategoryConfig{
					Allow: []string{"authorization", "x-passctrl-deny"},
					Strip: []string{"x-passctrl-deny"},
				},
				Query: CategoryConfig{
					Allow: []string{"allow", "deny"},
					Strip: []string{"deny"},
				},
			},
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "http://example.com/auth?allow=true&deny=true", http.NoBody)
				req.Header.Set("Authorization", "token")
				req.Header.Set("X-PassCtrl-Deny", "true")
				return req
			},
			expect: func(t *testing.T, _ pipeline.Result, state *pipeline.State) {
				require.NotContains(t, state.Forward.Headers, "x-passctrl-deny")
				require.NotContains(t, state.Forward.Query, "deny")
				require.Equal(t, "token", state.Forward.Headers["authorization"])
			},
		},
		{
			name: "custom entries override curated view",
			cfg: Config{
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
			},
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
			},
			expect: func(t *testing.T, _ pipeline.Result, state *pipeline.State) {
				require.Equal(t, "static-value", state.Forward.Headers["x-static"])
				require.NotContains(t, state.Forward.Headers, "x-empty")
				require.Equal(t, "12345", state.Forward.Query["ticket"])
			},
		},
		{
			name: "forwards proxy headers when enabled",
			cfg:  Config{ForwardProxyHeaders: true},
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
				req.Header.Set("X-Forwarded-For", "203.0.113.5")
				req.Header.Set("X-Forwarded-Proto", "https")
				req.Header.Set("X-Forwarded-Prefix", "/edge")
				req.Header.Set("Forwarded", "for=203.0.113.5;proto=https")
				return req
			},
			expect: func(t *testing.T, _ pipeline.Result, state *pipeline.State) {
				require.Equal(t, "203.0.113.5", state.Forward.Headers["x-forwarded-for"])
				require.Equal(t, "https", state.Forward.Headers["x-forwarded-proto"])
				require.Equal(t, "/edge", state.Forward.Headers["x-forwarded-prefix"])
				require.Equal(t, "for=203.0.113.5;proto=https", state.Forward.Headers["forwarded"])
			},
		},
		{
			name: "wildcard allow applies before strips",
			cfg: Config{
				Headers: CategoryConfig{
					Allow: []string{"*"},
					Strip: []string{"authorization"},
				},
				Query: CategoryConfig{
					Allow: []string{"*"},
					Strip: []string{"ignore"},
				},
			},
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "http://example.com/auth?keep=true&ignore=true", http.NoBody)
				req.Header.Set("Authorization", "token")
				req.Header.Set("X-Custom", "value")
				return req
			},
			expect: func(t *testing.T, _ pipeline.Result, state *pipeline.State) {
				require.NotContains(t, state.Forward.Headers, "authorization")
				require.Equal(t, "value", state.Forward.Headers["x-custom"])
				require.NotContains(t, state.Forward.Query, "ignore")
				require.Equal(t, "true", state.Forward.Query["keep"])
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req()
			state := newTestState(req)
			agent, err := New(tc.cfg, nil, nil)
			require.NoError(t, err)

			res := agent.Execute(req.Context(), req, state)

			tc.expect(t, res, state)
		})
	}
}

func TestAgentExecuteWithTemplates(t *testing.T) {
	tests := []struct {
		name   string
		cfg    Config
		req    func() *http.Request
		expect func(t *testing.T, res pipeline.Result, state *pipeline.State)
	}{
		{
			name: "renders custom header templates",
			cfg: Config{
				Headers: CategoryConfig{
					Custom: map[string]string{
						"X-Trace-ID": `{{ index .raw.Headers "x-request-id" }}`,
						"X-User":     `{{ index .raw.Headers "authorization" | replace "Bearer " "" }}`,
					},
				},
			},
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
				req.Header.Set("X-Request-ID", "req-123")
				req.Header.Set("Authorization", "Bearer token-456")
				return req
			},
			expect: func(t *testing.T, _ pipeline.Result, state *pipeline.State) {
				require.Equal(t, "req-123", state.Forward.Headers["x-trace-id"])
				require.Equal(t, "token-456", state.Forward.Headers["x-user"])
			},
		},
		{
			name: "renders custom query templates",
			cfg: Config{
				Query: CategoryConfig{
					Custom: map[string]string{
						"token": `{{ index .raw.Headers "authorization" | replace "Bearer " "" }}`,
						"page":  `{{ index .raw.Query "offset" | default "1" }}`,
					},
				},
			},
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "http://example.com/auth?offset=5", http.NoBody)
				req.Header.Set("Authorization", "Bearer my-token")
				return req
			},
			expect: func(t *testing.T, _ pipeline.Result, state *pipeline.State) {
				require.Equal(t, "my-token", state.Forward.Query["token"])
				require.Equal(t, "5", state.Forward.Query["page"])
			},
		},
		{
			name: "handles empty template results",
			cfg: Config{
				Headers: CategoryConfig{
					Custom: map[string]string{
						"X-Missing": `{{ index .raw.Headers "non-existent" }}`,
					},
				},
			},
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
			},
			expect: func(t *testing.T, _ pipeline.Result, state *pipeline.State) {
				// Empty template results should be stripped
				require.NotContains(t, state.Forward.Headers, "x-missing")
			},
		},
		{
			name: "falls back to static value on template error",
			cfg: Config{
				Headers: CategoryConfig{
					Custom: map[string]string{
						"X-Static": "fallback-value",
					},
				},
			},
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
			},
			expect: func(t *testing.T, _ pipeline.Result, state *pipeline.State) {
				// Should use static value if no template is configured
				require.Equal(t, "fallback-value", state.Forward.Headers["x-static"])
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req()
			state := newTestState(req)

			// Create template renderer
			renderer := templates.NewRenderer(nil)

			agent, err := New(tc.cfg, renderer, nil)
			require.NoError(t, err)

			res := agent.Execute(req.Context(), req, state)

			tc.expect(t, res, state)
		})
	}
}
