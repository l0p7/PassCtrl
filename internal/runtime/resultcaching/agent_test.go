package resultcaching

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cachemocks "github.com/l0p7/passctrl/internal/mocks/cache"
	"github.com/l0p7/passctrl/internal/runtime/cache"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestAgentExecuteSkipsWhenCached(t *testing.T) {
	cacheMock := cachemocks.NewMockDecisionCache(t)
	agent := New(Config{Cache: cacheMock, TTL: time.Minute})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/resource", http.NoBody)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Cache.Hit = true
	state.Cache.Decision = "pass"

	res := agent.Execute(context.Background(), nil, state)

	require.Equal(t, "hit", res.Status)
	cacheMock.AssertNotCalled(t, "Store", mock.Anything, mock.Anything, mock.Anything)
}

func TestAgentExecuteStoresSuccessfulOutcome(t *testing.T) {
	cacheMock := cachemocks.NewMockDecisionCache(t)
	agent := New(Config{Cache: cacheMock, TTL: time.Second})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/resource", http.NoBody)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Response = pipeline.ResponseState{
		Status:  http.StatusOK,
		Message: "granted",
		Headers: map[string]string{"a": "b"},
	}
	state.Rule.Outcome = "pass"

	var storedEntry cache.Entry
	cacheMock.EXPECT().
		Store(mock.Anything, state.CacheKey(), mock.Anything).
		Run(func(_ context.Context, key string, entry cache.Entry) {
			require.Equal(t, state.CacheKey(), key)
			storedEntry = entry
		}).
		Return(nil)

	res := agent.Execute(context.Background(), nil, state)

	require.Equal(t, "stored", res.Status)
	require.Equal(t, "pass", storedEntry.Decision)
	require.Equal(t, "b", storedEntry.Response.Headers["a"])
	require.True(t, state.Cache.Stored)
	require.Equal(t, "pass", state.Cache.Decision)
}

func TestAgentExecuteSkipsOnErrorOutcome(t *testing.T) {
	cacheMock := cachemocks.NewMockDecisionCache(t)
	agent := New(Config{Cache: cacheMock})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/resource", http.NoBody)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Rule.Outcome = "error"

	res := agent.Execute(context.Background(), nil, state)

	require.Equal(t, "bypassed", res.Status)
	cacheMock.AssertNotCalled(t, "Store", mock.Anything, mock.Anything, mock.Anything)
}

func TestAgentExecuteHandlesStoreFailure(t *testing.T) {
	cacheMock := cachemocks.NewMockDecisionCache(t)
	agent := New(Config{Cache: cacheMock})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/resource", http.NoBody)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Rule.Outcome = "pass"

	cacheMock.EXPECT().
		Store(mock.Anything, state.CacheKey(), mock.Anything).
		Return(errors.New("boom"))

	res := agent.Execute(context.Background(), nil, state)

	require.Equal(t, "error", res.Status)
	require.False(t, state.Cache.Stored)
}

func TestAgentExecuteSkipsWhenOutcomeMissing(t *testing.T) {
	cacheMock := cachemocks.NewMockDecisionCache(t)
	agent := New(Config{Cache: cacheMock})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/resource", http.NoBody)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")

	res := agent.Execute(context.Background(), nil, state)

	require.Equal(t, "skipped", res.Status)
	cacheMock.AssertNotCalled(t, "Store", mock.Anything, mock.Anything, mock.Anything)
}

func TestAgentExecuteUsesDefaultTTL(t *testing.T) {
	cacheMock := cachemocks.NewMockDecisionCache(t)
	agent := New(Config{Cache: cacheMock})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/resource", http.NoBody)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Rule.Outcome = "pass"

	var storedEntry cache.Entry
	cacheMock.EXPECT().
		Store(mock.Anything, state.CacheKey(), mock.Anything).
		Run(func(_ context.Context, key string, entry cache.Entry) {
			require.Equal(t, state.CacheKey(), key)
			storedEntry = entry
		}).
		Return(nil)

	res := agent.Execute(context.Background(), nil, state)

	require.Equal(t, "stored", res.Status)
	require.True(t, state.Cache.Stored)
	ttl := storedEntry.ExpiresAt.Sub(storedEntry.StoredAt)
	assert.Truef(t, ttl >= DefaultTTL-50*time.Millisecond && ttl <= DefaultTTL+time.Second, "expected ttl near default, got %v", ttl)
}

func TestResponseConversions(t *testing.T) {
	headers := map[string]string{"x": "1"}
	cacheResp := ResponseToCache(pipeline.ResponseState{Status: 200, Message: "ok", Headers: headers})
	require.Equal(t, "1", cacheResp.Headers["x"])

	headers["x"] = "2"
	pipelineResp := ResponseFromCache(cacheResp)
	require.Equal(t, "1", pipelineResp.Headers["x"])
}
