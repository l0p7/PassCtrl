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
	if !pipe.usingFallback {
		t.Fatalf("expected fallback endpoint to be installed when no endpoints configured")
	}
	if pipe.defaultEndpoint == nil || pipe.defaultEndpoint.name != "default" {
		t.Fatalf("expected default endpoint to be set, got %#v", pipe.defaultEndpoint)
	}
	if !pipe.EndpointExists("default") {
		t.Fatalf("expected fallback endpoint to be discoverable")
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/auth?error=false", http.NoBody)
	req.Header.Set("Authorization", "token")
	req.Header.Set("X-PassCtrl-Deny", "false")
	rec := httptest.NewRecorder()
	pipe.ServeAuth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected fallback pipeline to allow requests when default rule passes, got status %d", rec.Code)
	}
}

func TestSummarizeRuleHistory(t *testing.T) {
	history := []pipeline.RuleHistoryEntry{
		{Name: "a", Outcome: "pass", Reason: "ok", Duration: 10 * time.Millisecond},
		{Name: "b", Outcome: "fail", Reason: "fail", Duration: 5 * time.Millisecond},
	}
	summary := summarizeRuleHistory(history)
	if len(summary) != 2 {
		t.Fatalf("expected two entries, got %#v", summary)
	}
	if summary[0]["name"] != "a" || summary[1]["outcome"] != "fail" {
		t.Fatalf("unexpected summary content: %#v", summary)
	}
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
	if len(agents) != 1 {
		t.Fatalf("expected one wrapped agent, got %d", len(agents))
	}
	if agents[0].Name() != "stub" {
		t.Fatalf("expected instrumented agent to report inner name, got %s", agents[0].Name())
	}
}

func TestStateCacheKey(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
	state := pipeline.NewState(req, "endpoint", "cache-key", "corr-id")
	getter := (*pipeline.State).CacheKey
	if getter(state) != "cache-key" {
		t.Fatalf("expected cache key accessor to return underlying key, got %q", state.CacheKey())
	}
}
