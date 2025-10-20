package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
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

func TestRunLoaderError(t *testing.T) {
	overrideConfigLoader(t, func(_, _ string) configLoader {
		return &fakeLoader{loadErr: errors.New("boom")}
	})

	err := run(context.Background(), "PASSCTRL", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "load configuration")
}

func TestRunServerConstructorError(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Rules.RulesFolder = ""
	cfg.Server.Rules.RulesFile = ""
	cfg.Server.Templates.TemplatesFolder = ""

	overrideConfigLoader(t, func(_, _ string) configLoader {
		return &fakeLoader{cfg: cfg}
	})

	overrideHTTPServer(t, func(config.Config, *slog.Logger, http.Handler) (runnableServer, error) {
		return nil, errors.New("construct failed")
	})

	err := run(context.Background(), "PASSCTRL", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "construct failed")
}

func TestRunServerRunError(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Rules.RulesFolder = ""
	cfg.Server.Rules.RulesFile = ""
	cfg.Server.Templates.TemplatesFolder = ""

	overrideConfigLoader(t, func(_, _ string) configLoader {
		return &fakeLoader{cfg: cfg}
	})

	overrideHTTPServer(t, func(config.Config, *slog.Logger, http.Handler) (runnableServer, error) {
		return &stubServer{err: errors.New("run failed")}, nil
	})

	err := run(context.Background(), "PASSCTRL", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "run failed")
}

func overrideConfigLoader(t *testing.T, fn func(string, string) configLoader) {
	original := newConfigLoader
	newConfigLoader = fn
	t.Cleanup(func() { newConfigLoader = original })
}

func overrideHTTPServer(t *testing.T, fn func(config.Config, *slog.Logger, http.Handler) (runnableServer, error)) {
	original := newHTTPServer
	newHTTPServer = fn
	t.Cleanup(func() { newHTTPServer = original })
}

type fakeLoader struct {
	cfg       config.Config
	loadErr   error
	watchErr  error
	watcher   ruleWatcher
	stopped   *bool
	watchSeen bool
}

func (f *fakeLoader) Load(context.Context) (config.Config, error) {
	if f.loadErr != nil {
		return config.Config{}, f.loadErr
	}
	return f.cfg, nil
}

func (f *fakeLoader) WatchRules(context.Context, config.Config, func(config.RuleBundle), func(error)) (ruleWatcher, error) {
	f.watchSeen = true
	if f.watchErr != nil {
		return nil, f.watchErr
	}
	if f.watcher != nil {
		return f.watcher, nil
	}
	return &noOpWatcher{stopped: f.stopped}, nil
}

type noOpWatcher struct {
	stopped *bool
}

func (n *noOpWatcher) Stop() {
	if n.stopped != nil {
		*n.stopped = true
	}
}

type stubServer struct {
	err error
}

func (s *stubServer) Run(context.Context) error {
	return s.err
}
