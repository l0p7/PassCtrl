package resultcaching

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/l0p7/passctrl/internal/metrics"
	"github.com/l0p7/passctrl/internal/runtime/cache"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
)

const DefaultTTL = 30 * time.Second

// Agent persists successful rule outcomes for future requests.
type Agent struct {
	cache   cache.DecisionCache
	ttl     time.Duration
	logger  *slog.Logger
	metrics *metrics.Recorder
}

// Config controls the cache behavior for the agent.
type Config struct {
	Cache   cache.DecisionCache
	TTL     time.Duration
	Logger  *slog.Logger
	Metrics *metrics.Recorder
}

// New constructs a result caching agent with the supplied configuration.
func New(cfg Config) *Agent {
	return &Agent{cache: cfg.Cache, ttl: cfg.TTL, logger: cfg.Logger, metrics: cfg.Metrics}
}

// Name identifies the result caching agent for logging.
func (a *Agent) Name() string { return "result_caching" }

// Execute records successful rule outcomes in the cache for future
// short-circuiting unless the result originated from an error path.
func (a *Agent) Execute(ctx context.Context, _ *http.Request, state *pipeline.State) pipeline.Result {
	if state.Cache.Hit {
		return pipeline.Result{
			Name:    a.Name(),
			Status:  "hit",
			Details: "decision retrieved from cache",
		}
	}
	if state.Rule.Outcome == "" {
		return pipeline.Result{Name: a.Name(), Status: "skipped"}
	}
	if state.Rule.Outcome == "error" {
		return pipeline.Result{
			Name:    a.Name(),
			Status:  "bypassed",
			Details: "error outcomes are never cached",
		}
	}

	storedAt := time.Now().UTC()
	ttl := a.ttl
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	entry := cache.Entry{
		Decision:  state.Rule.Outcome,
		Response:  ResponseToCache(state.Response),
		StoredAt:  storedAt,
		ExpiresAt: storedAt.Add(ttl),
	}
	storeStart := time.Now()
	err := a.cache.Store(ctx, state.CacheKey(), entry)
	if a.metrics != nil {
		endpoint := ""
		if state != nil {
			endpoint = state.Endpoint
		}
		outcome := metrics.CacheStoreStored
		if err != nil {
			outcome = metrics.CacheStoreError
		}
		a.metrics.ObserveCacheStore(endpoint, outcome, time.Since(storeStart))
	}
	if err != nil {
		logger := a.logger
		if logger == nil {
			logger = slog.Default()
		}
		logger = logger.With(slog.String("agent", a.Name()))
		if state != nil {
			if state.Endpoint != "" {
				logger = logger.With(slog.String("endpoint", state.Endpoint))
			}
			if state.CorrelationID != "" {
				logger = logger.With(slog.String("correlation_id", state.CorrelationID))
			}
		}
		logger.Error("cache store failed", slog.Any("error", err), slog.String("cache_key", state.CacheKey()))
		return pipeline.Result{
			Name:    a.Name(),
			Status:  "error",
			Details: "failed to persist decision cache entry",
		}
	}
	state.Cache.Stored = true
	state.Cache.Decision = entry.Decision
	state.Cache.StoredAt = entry.StoredAt
	state.Cache.ExpiresAt = entry.ExpiresAt
	return pipeline.Result{
		Name:    a.Name(),
		Status:  "stored",
		Details: "decision cached for subsequent requests",
	}
}

// ResponseFromCache converts a cached response payload into a pipeline state
// snapshot.
func ResponseFromCache(in cache.Response) pipeline.ResponseState {
	return pipeline.ResponseState{
		Status:  in.Status,
		Message: in.Message,
		Headers: cloneHeaders(in.Headers),
	}
}

// ResponseToCache projects the pipeline response state into a cache entry.
func ResponseToCache(in pipeline.ResponseState) cache.Response {
	return cache.Response{
		Status:  in.Status,
		Message: in.Message,
		Headers: cloneHeaders(in.Headers),
	}
}

func cloneHeaders(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
