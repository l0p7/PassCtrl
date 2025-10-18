package server

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

type stubPipeline struct {
	endpoints           map[string]bool
	serveAuthCalls      int
	serveHealthCalls    int
	serveExplainCalls   int
	hints               []string
	receivedHintHeaders []string
	writeErrorCalled    bool
	writeErrorStatus    int
	writeErrorMessage   string
}

func (s *stubPipeline) ServeAuth(w http.ResponseWriter, r *http.Request) {
	s.serveAuthCalls++
	s.receivedHintHeaders = append(s.receivedHintHeaders, r.Header.Get("X-Endpoint-Hint"))
	w.WriteHeader(http.StatusOK)
}

func (s *stubPipeline) ServeHealth(w http.ResponseWriter, r *http.Request) {
	s.serveHealthCalls++
	s.receivedHintHeaders = append(s.receivedHintHeaders, r.Header.Get("X-Endpoint-Hint"))
	w.WriteHeader(http.StatusOK)
}

func (s *stubPipeline) ServeExplain(w http.ResponseWriter, r *http.Request) {
	s.serveExplainCalls++
	s.receivedHintHeaders = append(s.receivedHintHeaders, r.Header.Get("X-Endpoint-Hint"))
	w.WriteHeader(http.StatusOK)
}

func (s *stubPipeline) EndpointExists(name string) bool {
	if s.endpoints == nil {
		return false
	}
	return s.endpoints[name]
}

func (s *stubPipeline) RequestWithEndpointHint(r *http.Request, endpoint string) *http.Request {
	s.hints = append(s.hints, endpoint)
	cloned := r.Clone(r.Context())
	cloned.Header.Set("X-Endpoint-Hint", endpoint)
	return cloned
}

func (s *stubPipeline) WriteError(w http.ResponseWriter, status int, message string) {
	s.writeErrorCalled = true
	s.writeErrorStatus = status
	s.writeErrorMessage = message
	w.WriteHeader(status)
	_, _ = w.Write([]byte(message))
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
			if endpoint != tc.endpoint || route != tc.route || ok != tc.ok {
				t.Fatalf("parseEndpointRoute(%q) = (%q, %q, %t), want (%q, %q, %t)",
					tc.path, endpoint, route, ok, tc.endpoint, tc.route, tc.ok)
			}
		})
	}
}

func TestNewPipelineHandlerNilPipeline(t *testing.T) {
	handler := NewPipelineHandler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth", http.NoBody)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503 when pipeline unavailable, got %d", rec.Code)
	}
}

func TestPipelineHandlerDispatchesRoutes(t *testing.T) {
	stub := &stubPipeline{endpoints: map[string]bool{"tenant": true}}
	handler := NewPipelineHandler(stub)

	tests := []struct {
		name             string
		path             string
		wantStatus       int
		wantAuthCalls    int
		wantHealthCalls  int
		wantExplainCalls int
		wantHints        []string
	}{
		{
			name:          "root auth",
			path:          "/auth",
			wantStatus:    http.StatusOK,
			wantAuthCalls: 1,
		},
		{
			name:            "root health alias",
			path:            "/health",
			wantStatus:      http.StatusOK,
			wantHealthCalls: 1,
		},
		{
			name:          "scoped auth uses hint",
			path:          "/tenant/auth",
			wantStatus:    http.StatusOK,
			wantAuthCalls: 1,
			wantHints:     []string{"tenant"},
		},
		{
			name:            "scoped health uses hint",
			path:            "/tenant/healthz",
			wantStatus:      http.StatusOK,
			wantHealthCalls: 1,
			wantHints:       []string{"tenant"},
		},
		{
			name:             "scoped explain uses hint",
			path:             "/tenant/explain",
			wantStatus:       http.StatusOK,
			wantExplainCalls: 1,
			wantHints:        []string{"tenant"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub.serveAuthCalls = 0
			stub.serveHealthCalls = 0
			stub.serveExplainCalls = 0
			stub.hints = nil
			stub.receivedHintHeaders = nil

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.path, http.NoBody)

			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, rec.Code)
			}

			if stub.serveAuthCalls != tc.wantAuthCalls {
				t.Fatalf("expected %d auth calls, got %d", tc.wantAuthCalls, stub.serveAuthCalls)
			}
			if stub.serveHealthCalls != tc.wantHealthCalls {
				t.Fatalf("expected %d health calls, got %d", tc.wantHealthCalls, stub.serveHealthCalls)
			}
			if stub.serveExplainCalls != tc.wantExplainCalls {
				t.Fatalf("expected %d explain calls, got %d", tc.wantExplainCalls, stub.serveExplainCalls)
			}
			if len(tc.wantHints) == 0 {
				if len(stub.hints) != 0 {
					t.Fatalf("expected no endpoint hints, got %v", stub.hints)
				}
			} else {
				if !reflect.DeepEqual(stub.hints, tc.wantHints) {
					t.Fatalf("expected hints %v, got %v", tc.wantHints, stub.hints)
				}
				for i, hint := range tc.wantHints {
					if stub.receivedHintHeaders[i] != hint {
						t.Fatalf("expected request to carry hint header %q, got %q", hint, stub.receivedHintHeaders[i])
					}
				}
			}
		})
	}
}

func TestPipelineHandlerMissingEndpoint(t *testing.T) {
	stub := &stubPipeline{endpoints: map[string]bool{}}
	handler := NewPipelineHandler(stub)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/missing/health", http.NoBody)

	handler.ServeHTTP(rec, req)

	if !stub.writeErrorCalled {
		t.Fatalf("expected WriteError to be invoked for unknown endpoint")
	}
	if stub.writeErrorStatus != http.StatusNotFound {
		t.Fatalf("expected WriteError to use 404, got %d", stub.writeErrorStatus)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected recorder to capture 404, got %d", rec.Code)
	}
	if stub.serveHealthCalls != 0 {
		t.Fatalf("expected ServeHealth not to be called on missing endpoint")
	}
}

func TestPipelineHandlerNotFound(t *testing.T) {
	stub := &stubPipeline{}
	handler := NewPipelineHandler(stub)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/unsupported/path", http.NoBody)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unsupported route, got %d", rec.Code)
	}
	if stub.serveAuthCalls+stub.serveHealthCalls+stub.serveExplainCalls != 0 {
		t.Fatalf("expected no pipeline calls for unsupported route")
	}
}
