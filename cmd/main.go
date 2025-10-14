package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/l0p7/passctrl/internal/config"
	"github.com/l0p7/passctrl/internal/logging"
	"github.com/l0p7/passctrl/internal/metrics"
	"github.com/l0p7/passctrl/internal/runtime"
	"github.com/l0p7/passctrl/internal/runtime/cache"
	"github.com/l0p7/passctrl/internal/server"
	"github.com/l0p7/passctrl/internal/templates"
	"github.com/prometheus/client_golang/prometheus"
)

func main() {
	var (
		configFile = flag.String("config", "", "path to server configuration file")
		envPrefix  = flag.String("env-prefix", "PASSCTRL", "environment variable prefix")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	loader := config.NewLoader(*envPrefix, *configFile)
	cfg, err := loader.Load(ctx)
	if err != nil {
		log.Fatalf("failed to load configuration: %v", err)
	}

	logger, err := logging.New(cfg.Server.Logging)
	if err != nil {
		log.Fatalf("failed to configure logger: %v", err)
	}

	cacheLogger := logger.With(slog.String("agent", "cache_factory"))
	decisionCache := buildDecisionCache(cacheLogger, cfg.Server.Cache)
	cacheTTL := time.Duration(cfg.Server.Cache.TTLSeconds) * time.Second

	var templateSandbox *templates.Sandbox
	if folder := strings.TrimSpace(cfg.Server.Templates.TemplatesFolder); folder != "" {
		sandbox, err := templates.NewSandbox(folder, cfg.Server.Templates.TemplatesAllowEnv, cfg.Server.Templates.TemplatesAllowedEnv)
		if err != nil {
			logger.Warn("template sandbox setup failed", slog.String("templates_folder", folder), slog.Any("error", err))
		} else {
			templateSandbox = sandbox
		}
	}

	promRegistry := prometheus.NewRegistry()
	metricsRecorder := metrics.NewRecorder(promRegistry)

	pipe := runtime.NewPipeline(logger, runtime.PipelineOptions{
		Cache:              decisionCache,
		CacheTTL:           cacheTTL,
		CacheEpoch:         cfg.Server.Cache.Epoch,
		CacheKeySalt:       cfg.Server.Cache.KeySalt,
		Endpoints:          cfg.Endpoints,
		Rules:              cfg.Rules,
		RuleSources:        cfg.RuleSources,
		SkippedDefinitions: cfg.SkippedDefinitions,
		TemplateSandbox:    templateSandbox,
		CorrelationHeader:  cfg.Server.Logging.CorrelationHeader,
		Metrics:            metricsRecorder,
	})
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := pipe.Close(shutdownCtx); err != nil {
			logger.Error("cache shutdown failed", slog.Any("error", err))
		}
	}()

	var rulesWatcher *config.RulesWatcher
	if cfg.Server.Rules.RulesFile != "" || cfg.Server.Rules.RulesFolder != "" {
		watcher, err := loader.WatchRules(ctx, cfg, func(bundle config.RuleBundle) {
			pipe.Reload(ctx, bundle)
		}, func(err error) {
			if err != nil {
				logger.Error("rules watcher error", slog.Any("error", err))
			}
		})
		if err != nil {
			logger.Error("rules watcher setup failed", slog.Any("error", err))
		} else {
			rulesWatcher = watcher
			defer rulesWatcher.Stop()
		}
	}
	handler := server.NewPipelineHandler(pipe)
	mux := http.NewServeMux()
	mux.Handle("/metrics", metricsRecorder.Handler())
	mux.Handle("/", handler)

	srv, err := server.New(cfg, logger, mux)
	if err != nil {
		logger.Error("unable to construct server", slog.Any("error", err))
		os.Exit(1)
	}

	if err := srv.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("server terminated unexpectedly", slog.Any("error", err))
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	logger.Info("server shutdown complete")
}

func buildDecisionCache(logger *slog.Logger, cfg config.ServerCacheConfig) cache.DecisionCache {
	ttl := time.Duration(cfg.TTLSeconds) * time.Second
	backend := strings.TrimSpace(strings.ToLower(cfg.Backend))
	switch backend {
	case "", "memory":
		if logger != nil {
			logger.Info("using memory decision cache", slog.Duration("ttl", ttl))
		}
		return cache.NewMemory(ttl)
	case "redis":
		redisCache, err := cache.NewRedis(cache.RedisConfig{
			Address:  cfg.Redis.Address,
			Username: cfg.Redis.Username,
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
			TLS: cache.RedisTLSConfig{
				Enabled: cfg.Redis.TLS.Enabled,
				CAFile:  cfg.Redis.TLS.CAFile,
			},
		})
		if err != nil {
			if logger != nil {
				logger.Error("redis cache initialization failed", slog.Any("error", err))
				logger.Info("falling back to memory cache")
			}
			return cache.NewMemory(ttl)
		}
		if logger != nil {
			logger.Info("using redis decision cache", slog.String("address", cfg.Redis.Address))
		}
		return redisCache
	default:
		if logger != nil {
			logger.Warn("unsupported cache backend, defaulting to memory", slog.String("backend", cfg.Backend))
		}
		return cache.NewMemory(ttl)
	}
}
