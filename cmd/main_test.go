package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/l0p7/passctrl/internal/config"
	"github.com/l0p7/passctrl/internal/runtime/cache"
	"github.com/stretchr/testify/require"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestBuildDecisionCacheDefaultsToMemory(t *testing.T) {
	cache := buildDecisionCache(newTestLogger(), config.ServerCacheConfig{TTLSeconds: 1})
	require.NotNil(t, cache, "expected cache to be constructed")
	t.Cleanup(func() {
		require.NoError(t, cache.Close(context.Background()))
	})
}

func TestBuildDecisionCacheRedis(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	defer server.Close()

	cfg := config.ServerCacheConfig{
		Backend:    "redis",
		TTLSeconds: 1,
		Redis: config.ServerRedisCacheConfig{
			Address: server.Addr(),
		},
	}

	cache := buildDecisionCache(newTestLogger(), cfg)
	t.Cleanup(func() {
		require.NoError(t, cache.Close(context.Background()))
	})

	ctx := context.Background()
	entry := cacheEntry()
	require.NoError(t, cache.Store(ctx, "redis:test", entry))
	_, ok, err := cache.Lookup(ctx, "redis:test")
	require.NoError(t, err)
	require.True(t, ok, "expected lookup to succeed")
	time.Sleep(10 * time.Millisecond)
}

func cacheEntry() cache.Entry {
	now := time.Now().UTC()
	return cache.Entry{
		Decision:  "pass",
		Response:  cache.Response{Status: 200},
		StoredAt:  now,
		ExpiresAt: now.Add(100 * time.Millisecond),
	}
}
