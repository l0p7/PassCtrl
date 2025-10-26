package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cachemocks "github.com/l0p7/passctrl/internal/mocks/cache"
	pipelinemocks "github.com/l0p7/passctrl/internal/mocks/pipeline"
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
	req.Header.Set("Authorization", "Bearer token")
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

func TestInstrumentedAgentName(t *testing.T) {
	agent := pipelinemocks.NewMockAgent(t)
	agent.EXPECT().Name().Return("stub").Times(2)

	pipe := NewPipeline(nil, PipelineOptions{})
	inst := pipe.instrumentAgents("default", []pipeline.Agent{agent})

	require.Len(t, inst, 1)
	require.Equal(t, "stub", inst[0].Name())
}

func TestInstrumentedAgentExecuteDelegates(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	agent := pipelinemocks.NewMockAgent(t)
	agent.EXPECT().Name().Return("stub").Once()
	agent.EXPECT().
		Execute(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ *http.Request, state *pipeline.State) pipeline.Result {
			state.Rule.Outcome = "pass"
			state.Rule.Executed = true
			return pipeline.Result{Name: "stub", Status: "pass", Details: "ok"}
		}).
		Once()

	pipe := NewPipeline(logger, PipelineOptions{})
	inst := pipe.instrumentAgents("default", []pipeline.Agent{agent})
	require.Len(t, inst, 1)
	req := httptest.NewRequest(http.MethodGet, "http://unit.test/auth", http.NoBody)
	state := pipeline.NewState(req, "default", "cache-key", "corr-123")

	result := inst[0].Execute(context.Background(), req, state)

	require.Equal(t, "pass", result.Status)
	lines := bytes.Split(bytes.TrimSpace(logBuf.Bytes()), []byte("\n"))
	require.NotEmpty(t, lines, "expected instrumented agent to emit a log line")
	var entry map[string]any
	require.NoError(t, json.Unmarshal(lines[len(lines)-1], &entry))
	require.Equal(t, "stub", entry["agent"])
	require.Equal(t, "default", entry["endpoint"])
	require.Equal(t, "pass", entry["outcome"])
	require.Equal(t, "corr-123", entry["correlation_id"])
}

func TestStateCacheKey(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
	state := pipeline.NewState(req, "endpoint", "cache-key", "corr-id")
	getter := (*pipeline.State).CacheKey
	require.Equal(t, "cache-key", getter(state))
}
