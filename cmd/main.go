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

type configLoader interface {
	Load(context.Context) (config.Config, error)
	WatchRules(context.Context, config.Config, func(config.RuleBundle), func(error)) (ruleWatcher, error)
}

type ruleWatcher interface {
	Stop()
}

type runnableServer interface {
	Run(context.Context) error
}

var (
	newConfigLoader = func(envPrefix, configFile string) configLoader {
		return &loaderAdapter{inner: config.NewLoader(envPrefix, configFile)}
	}
	newAppLogger       = logging.New
	newPromRegistry    = func() *prometheus.Registry { return prometheus.NewRegistry() }
	newMetricsRecorder = func(reg *prometheus.Registry) metrics.Recorder { return metrics.NewRecorder(reg) }
	newHTTPServer      = func(cfg config.Config, logger *slog.Logger, handler http.Handler) (runnableServer, error) {
		return server.New(cfg, logger, handler)
	}
	buildCache = buildDecisionCache
)

type loaderAdapter struct {
	inner *config.Loader
}

func (l *loaderAdapter) Load(ctx context.Context) (config.Config, error) {
	return l.inner.Load(ctx)
}

func (l *loaderAdapter) WatchRules(ctx context.Context, cfg config.Config, onChange func(config.RuleBundle), onError func(error)) (ruleWatcher, error) {
	return l.inner.WatchRules(ctx, cfg, onChange, onError)
}

func main() {
	var (
		configFile = flag.String("config", "", "path to server configuration file")
		envPrefix  = flag.String("env-prefix", "PASSCTRL", "environment variable prefix")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, *envPrefix, *configFile); err != nil {
		log.Fatalf("server startup failed: %v", err)
	}
}

func run(ctx context.Context, envPrefix, configPath string) error {
	loader := newConfigLoader(envPrefix, configPath)
	cfg, err := loader.Load(ctx)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	logger, err := newAppLogger(cfg.Server.Logging)
	if err != nil {
		return fmt.Errorf("configure logger: %w", err)
	}

	cacheLogger := logger.With(slog.String("agent", "cache_factory"))
	decisionCache := buildCache(cacheLogger, cfg.Server.Cache)
	cacheTTL := time.Duration(cfg.Server.Cache.TTLSeconds) * time.Second

	var templateSandbox *templates.Sandbox
	if folder := strings.TrimSpace(cfg.Server.Templates.TemplatesFolder); folder != "" {
		sandbox, err := templates.NewSandbox(folder)
		if err != nil {
			logger.Warn("template sandbox setup failed", slog.String("templates_folder", folder), slog.Any("error", err))
		} else {
			templateSandbox = sandbox
		}
	}

	promRegistry := newPromRegistry()
	metricsRecorder := newMetricsRecorder(promRegistry)

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
		LoadedEnvironment:  cfg.LoadedEnvironment,
		LoadedSecrets:      cfg.LoadedSecrets,
	})
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := pipe.Close(shutdownCtx); err != nil {
			logger.Error("cache shutdown failed", slog.Any("error", err))
		}
	}()

	var watcher ruleWatcher
	if cfg.Server.Rules.RulesFile != "" || cfg.Server.Rules.RulesFolder != "" {
		w, err := loader.WatchRules(ctx, cfg, func(bundle config.RuleBundle) {
			pipe.Reload(ctx, bundle)
		}, func(err error) {
			if err != nil {
				logger.Error("rules watcher error", slog.Any("error", err))
			}
		})
		if err != nil {
			logger.Error("rules watcher setup failed", slog.Any("error", err))
		} else {
			watcher = w
		}
	}
	if watcher != nil {
		defer watcher.Stop()
	}

	handler := server.NewPipelineHandler(pipe)
	mux := http.NewServeMux()
	mux.Handle("/metrics", metricsRecorder.Handler())
	mux.Handle("/", handler)

	srv, err := newHTTPServer(cfg, logger, mux)
	if err != nil {
		logger.Error("unable to construct server", slog.Any("error", err))
		return err
	}

	if err := srv.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("server terminated unexpectedly", slog.Any("error", err))
		fmt.Fprintln(os.Stderr, err)
		return err
	}

	logger.Info("server shutdown complete")
	return nil
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
