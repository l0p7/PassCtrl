package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"

	"github.com/l0p7/passctrl/internal/config"
	"github.com/l0p7/passctrl/internal/runtime/cache"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestBuildDecisionCacheDefaultsToMemory(t *testing.T) {
	cache := buildDecisionCache(newTestLogger(), config.ServerCacheConfig{TTLSeconds: 1})
	if cache == nil {
		t.Fatalf("expected cache to be constructed")
	}
	if err := cache.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestBuildDecisionCacheRedis(t *testing.T) {
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer server.Close()

	cfg := config.ServerCacheConfig{
		Backend:    "redis",
		TTLSeconds: 1,
		Redis: config.ServerRedisCacheConfig{
			Address: server.Addr(),
		},
	}

	cache := buildDecisionCache(newTestLogger(), cfg)
	defer cache.Close(context.Background())

	ctx := context.Background()
	entry := cacheEntry()
	if err := cache.Store(ctx, "redis:test", entry); err != nil {
		t.Fatalf("store: %v", err)
	}
	if _, ok, err := cache.Lookup(ctx, "redis:test"); err != nil || !ok {
		t.Fatalf("lookup: ok=%v err=%v", ok, err)
	}
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
