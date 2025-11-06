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
	tests := []struct {
		name   string
		cfg    Config
		req    func() *http.Request
		expect func(t *testing.T, res pipeline.Result, state *pipeline.State)
	}{
		{
			name: "default config initializes empty forward state",
			cfg:  DefaultConfig(),
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
				req.Header.Set("Authorization", "bearer token")
				req.Header.Set("X-Request-ID", "123")
				return req
			},
			expect: func(t *testing.T, res pipeline.Result, state *pipeline.State) {
				require.Equal(t, "ready", res.Status)
				require.Empty(t, state.Forward.Headers)
				require.Empty(t, state.Forward.Query)
			},
		},
		{
			name: "forwards proxy headers when enabled",
			cfg: Config{
				ForwardProxyHeaders: true,
			},
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
				req.Header.Set("X-Forwarded-For", "192.168.1.1")
				req.Header.Set("X-Forwarded-Proto", "https")
				req.Header.Set("X-Forwarded-Host", "example.com")
				req.Header.Set("Forwarded", "for=192.168.1.1;proto=https")
				req.Header.Set("Authorization", "bearer token")
				return req
			},
			expect: func(t *testing.T, res pipeline.Result, state *pipeline.State) {
				require.Equal(t, "ready", res.Status)
				require.Equal(t, "192.168.1.1", state.Forward.Headers["x-forwarded-for"])
				require.Equal(t, "https", state.Forward.Headers["x-forwarded-proto"])
				require.Equal(t, "example.com", state.Forward.Headers["x-forwarded-host"])
				require.Equal(t, "for=192.168.1.1;proto=https", state.Forward.Headers["forwarded"])
				require.NotContains(t, state.Forward.Headers, "authorization")
			},
		},
		{
			name: "skips proxy headers when disabled",
			cfg: Config{
				ForwardProxyHeaders: false,
			},
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
				req.Header.Set("X-Forwarded-For", "192.168.1.1")
				req.Header.Set("X-Forwarded-Proto", "https")
				return req
			},
			expect: func(t *testing.T, res pipeline.Result, state *pipeline.State) {
				require.Equal(t, "ready", res.Status)
				require.Empty(t, state.Forward.Headers)
			},
		},
		{
			name: "skips empty proxy headers",
			cfg: Config{
				ForwardProxyHeaders: true,
			},
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
				req.Header.Set("X-Forwarded-For", "")
				req.Header.Set("X-Forwarded-Proto", "   ")
				req.Header.Set("X-Forwarded-Host", "example.com")
				return req
			},
			expect: func(t *testing.T, res pipeline.Result, state *pipeline.State) {
				require.Equal(t, "ready", res.Status)
				require.NotContains(t, state.Forward.Headers, "x-forwarded-for")
				require.NotContains(t, state.Forward.Headers, "x-forwarded-proto")
				require.Equal(t, "example.com", state.Forward.Headers["x-forwarded-host"])
			},
		},
		{
			name: "normalizes header names to lowercase",
			cfg: Config{
				ForwardProxyHeaders: true,
			},
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
				req.Header.Set("X-FORWARDED-FOR", "192.168.1.1")
				return req
			},
			expect: func(t *testing.T, res pipeline.Result, state *pipeline.State) {
				require.Equal(t, "192.168.1.1", state.Forward.Headers["x-forwarded-for"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, err := New(tt.cfg, nil)
			require.NoError(t, err)

			req := tt.req()
			state := newTestState(req)
			res := agent.Execute(req.Context(), req, state)

			tt.expect(t, res, state)
		})
	}
}

func TestNew(t *testing.T) {
	t.Run("constructs agent with default config", func(t *testing.T) {
		agent, err := New(DefaultConfig(), nil)
		require.NoError(t, err)
		require.NotNil(t, agent)
	})

	t.Run("constructs agent with proxy headers enabled", func(t *testing.T) {
		agent, err := New(Config{ForwardProxyHeaders: true}, nil)
		require.NoError(t, err)
		require.NotNil(t, agent)
	})
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	require.False(t, cfg.ForwardProxyHeaders)
}
