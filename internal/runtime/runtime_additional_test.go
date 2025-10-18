package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/l0p7/passctrl/internal/runtime/cache"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
)

type stubDecisionCache struct {
	closed bool
}

func (s *stubDecisionCache) Lookup(context.Context, string) (cache.Entry, bool, error) {
	return cache.Entry{}, false, nil
}
func (s *stubDecisionCache) Store(context.Context, string, cache.Entry) error { return nil }
func (s *stubDecisionCache) DeletePrefix(context.Context, string) error       { return nil }
func (s *stubDecisionCache) Size(context.Context) (int64, error)              { return 0, nil }
func (s *stubDecisionCache) Close(context.Context) error {
	s.closed = true
	return nil
}

func TestPipelineCloseInvokesCache(t *testing.T) {
	stub := &stubDecisionCache{}
	pipe := NewPipeline(nil, PipelineOptions{Cache: stub})
	if err := pipe.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !stub.closed {
		t.Fatalf("expected cache Close to be invoked")
	}
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

	req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
	req.Header.Set("Authorization", "token")
	rec := httptest.NewRecorder()
	pipe.ServeAuth(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected fallback pipeline to surface default error status, got %d", rec.Code)
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
