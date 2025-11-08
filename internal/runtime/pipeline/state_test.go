package pipeline

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewStateInitializesNormalization(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.com/foo/bar?Foo=bar&Zap=zazz", http.NoBody)
	req.Header.Set("X-Custom", "primary")
	req.Header.Add("X-Custom", "secondary")
	req.Header.Set("X-Another", "value")

	state := NewState(req, "demo-endpoint", "cache-key-1", "corr-123")

	require.Equal(t, "demo-endpoint", state.Endpoint)
	require.Equal(t, "cache-key-1", state.Cache.Key)
	require.Equal(t, "primary", state.Request.Headers["x-custom"])
	require.NotContains(t, state.Request.Headers, "X-Custom")
	require.Equal(t, "bar", state.Request.Query["foo"])
	require.NotContains(t, state.Request.Query, "Foo")
	require.NotNil(t, state.Forward.Headers)
	require.NotNil(t, state.Forward.Query)
	require.NotNil(t, state.Response.Headers)
	require.NotNil(t, state.Backend.Headers)
}

func TestPlanAccessors(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/demo", http.NoBody)
	state := NewState(req, "demo", "cache-key", "corr")

	require.Nil(t, state.Plan())
	state.SetPlan("plan-value")
	require.Equal(t, "plan-value", state.Plan())
	state.ClearPlan()
	require.Nil(t, state.Plan())
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

	require.Equal(t, "endpoint-a", ctx["endpoint"])
	require.Equal(t, "corr-id", ctx["correlationId"])
	require.Equal(t, state, ctx["state"])
	cache, ok := ctx["cache"].(CacheState)
	require.True(t, ok)
	require.Equal(t, "cache-key", cache.Key)
}

func TestTemplateContextNilState(t *testing.T) {
	var state *State
	ctx := state.TemplateContext()
	require.NotNil(t, ctx)
	require.Empty(t, ctx)
}

func TestStateCacheKeyAccessor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/demo", http.NoBody)
	state := NewState(req, "demo", "cache-key-test", "corr")
	require.Equal(t, "cache-key-test", state.CacheKey())
}
