package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cachemocks "github.com/l0p7/passctrl/internal/mocks/cache"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestPipelineCloseInvokesCache(t *testing.T) {
	cacheMock := cachemocks.NewMockDecisionCache(t)
	cacheMock.EXPECT().
		Close(mock.Anything).
		Return(nil)

	pipe := NewPipeline(nil, PipelineOptions{Cache: cacheMock})
	require.NoError(t, pipe.Close(context.Background()))
}

func TestPipelineFallbackEndpoint(t *testing.T) {
	pipe := NewPipeline(nil, PipelineOptions{})
	require.True(t, pipe.usingFallback, "expected fallback endpoint to be installed when no endpoints configured")
	require.NotNil(t, pipe.defaultEndpoint)
	require.Equal(t, "default", pipe.defaultEndpoint.name)
	require.True(t, pipe.EndpointExists("default"), "expected fallback endpoint to be discoverable")

	req := httptest.NewRequest(http.MethodGet, "http://example.com/auth?error=false", http.NoBody)
	req.Header.Set("Authorization", "token")
	req.Header.Set("X-PassCtrl-Deny", "false")
	rec := httptest.NewRecorder()
	pipe.ServeAuth(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestSummarizeRuleHistory(t *testing.T) {
	history := []pipeline.RuleHistoryEntry{
		{Name: "a", Outcome: "pass", Reason: "ok", Duration: 10 * time.Millisecond},
		{Name: "b", Outcome: "fail", Reason: "fail", Duration: 5 * time.Millisecond},
	}
	summary := summarizeRuleHistory(history)
	require.Len(t, summary, 2)
	require.Equal(t, "a", summary[0]["name"])
	require.Equal(t, "fail", summary[1]["outcome"])
}

type stubAgent struct {
	name string
}

func (s *stubAgent) Name() string { return s.name }
func (s *stubAgent) Execute(context.Context, *http.Request, *pipeline.State) pipeline.Result {
	return pipeline.Result{Name: s.name, Status: "ok"}
}

func TestInstrumentedAgentName(t *testing.T) {
	pipe := NewPipeline(nil, PipelineOptions{})
	agents := pipe.instrumentAgents("default", []pipeline.Agent{&stubAgent{name: "stub"}})
	require.Len(t, agents, 1)
	require.Equal(t, "stub", agents[0].Name())
}

func TestStateCacheKey(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
	state := pipeline.NewState(req, "endpoint", "cache-key", "corr-id")
	getter := (*pipeline.State).CacheKey
	require.Equal(t, "cache-key", getter(state))
}
