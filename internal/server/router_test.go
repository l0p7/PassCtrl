package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gavv/httpexpect/v2"
	servermocks "github.com/l0p7/passctrl/internal/mocks/server"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newPipelineExpect(t *testing.T, handler http.Handler) *httpexpect.Expect {
	t.Helper()
	return httpexpect.WithConfig(httpexpect.Config{
		Reporter: httpexpect.NewRequireReporter(t),
		Client: &http.Client{
			Transport: httpexpect.NewBinder(handler),
		},
	})
}

func TestParseEndpointRoute(t *testing.T) {
	cases := map[string]struct {
		path     string
		endpoint string
		route    string
		ok       bool
	}{
		"root auth":      {path: "/auth", endpoint: "", route: "auth", ok: true},
		"root health":    {path: "/health", endpoint: "", route: "healthz", ok: true},
		"root healthz":   {path: "/healthz", endpoint: "", route: "healthz", ok: true},
		"root explain":   {path: "/explain", endpoint: "", route: "explain", ok: true},
		"scoped auth":    {path: "/tenant/auth", endpoint: "tenant", route: "auth", ok: true},
		"scoped health":  {path: "/tenant/health", endpoint: "tenant", route: "healthz", ok: true},
		"scoped explain": {path: "/tenant/explain", endpoint: "tenant", route: "explain", ok: true},
		"double slash":   {path: "//tenant//auth//", ok: false},
		"unknown root":   {path: "/unknown", ok: false},
		"unknown scoped": {path: "/tenant/other", ok: false},
		"empty path":     {path: "/", ok: false},
		"blank path":     {path: "", ok: false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			endpoint, route, ok := parseEndpointRoute(tc.path)
			require.Equalf(t, tc.endpoint, endpoint, "endpoint mismatch for path %q", tc.path)
			require.Equalf(t, tc.route, route, "route mismatch for path %q", tc.path)
			require.Equalf(t, tc.ok, ok, "ok mismatch for path %q", tc.path)
		})
	}
}

func TestNewPipelineHandlerNilPipeline(t *testing.T) {
	handler := NewPipelineHandler(nil)
	expect := newPipelineExpect(t, handler)

	expect.GET("/auth").Expect().Status(http.StatusServiceUnavailable)
}

func TestPipelineHandlerDispatchesRoutes(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantStatus int
		setup      func(t *testing.T, m *servermocks.MockPipelineHTTP)
	}{
		{
			name:       "root auth",
			path:       "/auth",
			wantStatus: http.StatusOK,
			setup: func(t *testing.T, m *servermocks.MockPipelineHTTP) {
				m.EXPECT().
					ServeAuth(mock.Anything, requestWithHint(t, "")).
					Run(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusOK)
					}).
					Once()
			},
		},
		{
			name:       "root health alias",
			path:       "/health",
			wantStatus: http.StatusOK,
			setup: func(t *testing.T, m *servermocks.MockPipelineHTTP) {
				m.EXPECT().
					ServeHealth(mock.Anything, requestWithHint(t, "")).
					Run(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusOK)
					}).
					Once()
			},
		},
		{
			name:       "scoped auth uses hint",
			path:       "/tenant/auth",
			wantStatus: http.StatusOK,
			setup: func(t *testing.T, m *servermocks.MockPipelineHTTP) {
				m.EXPECT().
					RequestWithEndpointHint(mock.Anything, "tenant").
					RunAndReturn(cloneWithHint(t, "tenant")).
					Once()
				m.EXPECT().
					ServeAuth(mock.Anything, requestWithHint(t, "tenant")).
					Run(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusOK)
					}).
					Once()
			},
		},
		{
			name:       "scoped health uses hint",
			path:       "/tenant/healthz",
			wantStatus: http.StatusOK,
			setup: func(t *testing.T, m *servermocks.MockPipelineHTTP) {
				m.EXPECT().
					EndpointExists("tenant").
					Return(true).
					Once()
				m.EXPECT().
					RequestWithEndpointHint(mock.Anything, "tenant").
					RunAndReturn(cloneWithHint(t, "tenant")).
					Once()
				m.EXPECT().
					ServeHealth(mock.Anything, requestWithHint(t, "tenant")).
					Run(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusOK)
					}).
					Once()
			},
		},
		{
			name:       "scoped explain uses hint",
			path:       "/tenant/explain",
			wantStatus: http.StatusOK,
			setup: func(t *testing.T, m *servermocks.MockPipelineHTTP) {
				m.EXPECT().
					EndpointExists("tenant").
					Return(true).
					Once()
				m.EXPECT().
					RequestWithEndpointHint(mock.Anything, "tenant").
					RunAndReturn(cloneWithHint(t, "tenant")).
					Once()
				m.EXPECT().
					ServeExplain(mock.Anything, requestWithHint(t, "tenant")).
					Run(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusOK)
					}).
					Once()
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockPipeline := servermocks.NewMockPipelineHTTP(t)
			tc.setup(t, mockPipeline)

			handler := NewPipelineHandler(mockPipeline)
			resp := newPipelineExpect(t, handler).GET(tc.path).Expect()
			resp.Status(tc.wantStatus)
		})
	}
}

func TestPipelineHandlerMissingEndpoint(t *testing.T) {
	mockPipeline := servermocks.NewMockPipelineHTTP(t)
	mockPipeline.EXPECT().
		EndpointExists("missing").
		Return(false).
		Once()
	mockPipeline.EXPECT().
		WriteError(mock.Anything, http.StatusNotFound, mock.MatchedBy(func(msg string) bool {
			return strings.Contains(msg, "missing")
		})).
		Run(func(w http.ResponseWriter, status int, msg string) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(msg))
		}).
		Once()

	handler := NewPipelineHandler(mockPipeline)

	resp := newPipelineExpect(t, handler).GET("/missing/health").Expect()
	resp.Status(http.StatusNotFound)
	resp.Body().Contains("endpoint \"missing\" not found")
}

func TestPipelineHandlerNotFound(t *testing.T) {
	mockPipeline := servermocks.NewMockPipelineHTTP(t)
	handler := NewPipelineHandler(mockPipeline)

	newPipelineExpect(t, handler).GET("/unsupported/path").Expect().Status(http.StatusNotFound)

	// no pipeline methods should be invoked for unsupported routes; any unexpected call would fail via mock expectations.
}

func requestWithHint(t *testing.T, expected string) interface{} {
	t.Helper()
	return mock.MatchedBy(func(r *http.Request) bool {
		t.Helper()
		if r == nil {
			return false
		}
		return r.Header.Get("X-Endpoint-Hint") == expected
	})
}

func cloneWithHint(t *testing.T, expected string) func(*http.Request, string) *http.Request {
	return func(r *http.Request, endpoint string) *http.Request {
		t.Helper()
		require.Equal(t, expected, endpoint)
		cloned := r.Clone(r.Context())
		cloned.Header.Set("X-Endpoint-Hint", endpoint)
		return cloned
	}
}
