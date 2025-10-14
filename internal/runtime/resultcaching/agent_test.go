package resultcaching

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/l0p7/passctrl/internal/runtime/cache"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
)

type stubDecisionCache struct {
	storeErr error
	entries  map[string]cache.Entry
}

func (s *stubDecisionCache) Lookup(context.Context, string) (cache.Entry, bool, error) {
	return cache.Entry{}, false, nil
}

func (s *stubDecisionCache) Store(_ context.Context, key string, entry cache.Entry) error {
	if s.entries == nil {
		s.entries = make(map[string]cache.Entry)
	}
	if s.storeErr != nil {
		return s.storeErr
	}
	s.entries[key] = entry
	return nil
}

func (s *stubDecisionCache) DeletePrefix(context.Context, string) error { return nil }
func (s *stubDecisionCache) Size(context.Context) (int64, error)        { return int64(len(s.entries)), nil }
func (s *stubDecisionCache) Close(context.Context) error                { return nil }

func TestAgentExecuteSkipsWhenCached(t *testing.T) {
	cacheStub := &stubDecisionCache{}
	agent := New(Config{Cache: cacheStub, TTL: time.Minute})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/resource", http.NoBody)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Cache.Hit = true
	state.Cache.Decision = "pass"

	res := agent.Execute(context.Background(), nil, state)

	if res.Status != "hit" {
		t.Fatalf("expected hit status, got %s", res.Status)
	}

	if len(cacheStub.entries) != 0 {
		t.Fatalf("expected cache to remain untouched on hit")
	}
}

func TestAgentExecuteStoresSuccessfulOutcome(t *testing.T) {
	cacheStub := &stubDecisionCache{}
	agent := New(Config{Cache: cacheStub, TTL: time.Second})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/resource", http.NoBody)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Response = pipeline.ResponseState{Status: http.StatusOK, Message: "granted", Headers: map[string]string{"a": "b"}}
	state.Rule.Outcome = "pass"

	res := agent.Execute(context.Background(), nil, state)

	if res.Status != "stored" {
		t.Fatalf("expected stored status, got %s", res.Status)
	}

	entry, ok := cacheStub.entries[state.CacheKey()]
	if !ok {
		t.Fatalf("expected cache entry to be recorded")
	}

	if entry.Decision != "pass" {
		t.Fatalf("expected decision to be cached, got %q", entry.Decision)
	}

	if entry.Response.Headers["a"] != "b" {
		t.Fatalf("expected response headers to be cloned")
	}

	if !state.Cache.Stored || state.Cache.Decision != "pass" {
		t.Fatalf("expected pipeline cache state to reflect stored decision: %#v", state.Cache)
	}
}

func TestAgentExecuteSkipsOnErrorOutcome(t *testing.T) {
	cacheStub := &stubDecisionCache{}
	agent := New(Config{Cache: cacheStub})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/resource", http.NoBody)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Rule.Outcome = "error"

	res := agent.Execute(context.Background(), nil, state)

	if res.Status != "bypassed" {
		t.Fatalf("expected bypassed status, got %s", res.Status)
	}

	if len(cacheStub.entries) != 0 {
		t.Fatalf("expected no cache entry to be recorded for error outcomes")
	}
}

func TestAgentExecuteHandlesStoreFailure(t *testing.T) {
	cacheStub := &stubDecisionCache{storeErr: errors.New("boom")}
	agent := New(Config{Cache: cacheStub})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/resource", http.NoBody)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Rule.Outcome = "pass"

	res := agent.Execute(context.Background(), nil, state)

	if res.Status != "error" {
		t.Fatalf("expected error status, got %s", res.Status)
	}

	if state.Cache.Stored {
		t.Fatalf("expected pipeline cache state not to mark stored on failure")
	}
}

func TestAgentExecuteSkipsWhenOutcomeMissing(t *testing.T) {
	cacheStub := &stubDecisionCache{}
	agent := New(Config{Cache: cacheStub})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/resource", http.NoBody)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")

	res := agent.Execute(context.Background(), nil, state)

	if res.Status != "skipped" {
		t.Fatalf("expected skipped status when no outcome present, got %s", res.Status)
	}

	if len(cacheStub.entries) != 0 {
		t.Fatalf("expected cache to remain untouched when no outcome available")
	}
}

func TestAgentExecuteUsesDefaultTTL(t *testing.T) {
	cacheStub := &stubDecisionCache{}
	agent := New(Config{Cache: cacheStub})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/resource", http.NoBody)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Rule.Outcome = "pass"

	res := agent.Execute(context.Background(), nil, state)
	if res.Status != "stored" {
		t.Fatalf("expected stored status, got %s", res.Status)
	}

	entry := cacheStub.entries[state.CacheKey()]
	ttl := entry.ExpiresAt.Sub(entry.StoredAt)
	if ttl < DefaultTTL-10*time.Millisecond || ttl > DefaultTTL+time.Second {
		t.Fatalf("expected ttl to fall back to default, got %v", ttl)
	}
}

func TestResponseConversions(t *testing.T) {
	headers := map[string]string{"x": "1"}
	cacheResp := ResponseToCache(pipeline.ResponseState{Status: 200, Message: "ok", Headers: headers})
	if cacheResp.Headers["x"] != "1" {
		t.Fatalf("expected header to be copied")
	}

	headers["x"] = "2"
	pipelineResp := ResponseFromCache(cacheResp)
	if pipelineResp.Headers["x"] != "1" {
		t.Fatalf("expected pipeline response to be isolated from cache map")
	}
}
