package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
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

func TestBuildDecisionCache(t *testing.T) {
	tests := []struct {
		name   string
		cfg    func(t *testing.T) config.ServerCacheConfig
		verify func(t *testing.T, cache cache.DecisionCache)
	}{
		{
			name: "defaults to memory",
			cfg: func(t *testing.T) config.ServerCacheConfig {
				return config.ServerCacheConfig{TTLSeconds: 1}
			},
			verify: func(t *testing.T, cache cache.DecisionCache) {
				require.NotNil(t, cache, "expected cache to be constructed")
			},
		},
		{
			name: "constructs redis cache",
			cfg: func(t *testing.T) config.ServerCacheConfig {
				server, err := miniredis.Run()
				if err != nil {
					if strings.Contains(err.Error(), "operation not permitted") {
						t.Skip("miniredis unavailable in sandbox")
					}
					require.NoError(t, err)
				}
				t.Cleanup(server.Close)
				return config.ServerCacheConfig{
					Backend:    "redis",
					TTLSeconds: 1,
					Redis: config.ServerRedisCacheConfig{
						Address: server.Addr(),
					},
				}
			},
			verify: func(t *testing.T, cache cache.DecisionCache) {
				ctx := context.Background()
				entry := cacheEntry()
				require.NoError(t, cache.Store(ctx, "redis:test", entry))
				_, ok, err := cache.Lookup(ctx, "redis:test")
				require.NoError(t, err)
				require.True(t, ok, "expected lookup to succeed")
				time.Sleep(10 * time.Millisecond)
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg(t)
			cache := buildDecisionCache(newTestLogger(), cfg)
			t.Cleanup(func() {
				require.NoError(t, cache.Close(context.Background()))
			})

			tc.verify(t, cache)
		})
	}
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
