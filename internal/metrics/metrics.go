package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// CacheOperation identifies the cache method being instrumented.
type CacheOperation string

const (
	// CacheOperationLookup records decision cache lookup calls.
	CacheOperationLookup CacheOperation = "lookup"
	// CacheOperationStore records decision cache store attempts.
	CacheOperationStore CacheOperation = "store"
)

// CacheLookupOutcome captures the result of a cache lookup.
type CacheLookupOutcome string

const (
	// CacheLookupHit indicates the lookup reused a cached decision.
	CacheLookupHit CacheLookupOutcome = "hit"
	// CacheLookupMiss indicates no cached decision was present.
	CacheLookupMiss CacheLookupOutcome = "miss"
	// CacheLookupError indicates the lookup failed due to an error.
	CacheLookupError CacheLookupOutcome = "error"
)

// CacheStoreOutcome captures the result of a cache store attempt.
type CacheStoreOutcome string

const (
	// CacheStoreStored indicates the decision cache entry was persisted.
	CacheStoreStored CacheStoreOutcome = "stored"
	// CacheStoreError indicates the store operation failed.
	CacheStoreError CacheStoreOutcome = "error"
)

// Recorder publishes Prometheus metrics for pipeline activity.
type Recorder struct {
	gatherer prometheus.Gatherer
	handler  http.Handler

	authRequests *prometheus.CounterVec
	authLatency  *prometheus.HistogramVec

	cacheOperations *prometheus.CounterVec
	cacheLatency    *prometheus.HistogramVec
}

// NewRecorder constructs a Prometheus-backed Recorder. When reg is nil a dedicated
// registry is created so multiple recorders can coexist without conflicting with
// the global default registerer.
func NewRecorder(reg *prometheus.Registry) *Recorder {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}

	reg.MustRegister(
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewGoCollector(),
	)

	authRequests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "passctrl",
		Subsystem: "auth",
		Name:      "requests_total",
		Help:      "Total /auth requests processed by the pipeline.",
	}, []string{"endpoint", "outcome", "status_code", "from_cache"})

	authLatency := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "passctrl",
		Subsystem: "auth",
		Name:      "request_duration_seconds",
		Help:      "Latency distribution for completed /auth requests.",
		Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
	}, []string{"endpoint", "outcome"})

	cacheOperations := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "passctrl",
		Subsystem: "cache",
		Name:      "operations_total",
		Help:      "Decision cache operations executed by the pipeline.",
	}, []string{"endpoint", "operation", "result"})

	cacheLatency := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "passctrl",
		Subsystem: "cache",
		Name:      "operation_duration_seconds",
		Help:      "Latency distribution for decision cache operations.",
		Buckets:   []float64{0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5},
	}, []string{"endpoint", "operation", "result"})

	reg.MustRegister(authRequests, authLatency, cacheOperations, cacheLatency)

	handler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})

	return &Recorder{
		gatherer:        reg,
		handler:         handler,
		authRequests:    authRequests,
		authLatency:     authLatency,
		cacheOperations: cacheOperations,
		cacheLatency:    cacheLatency,
	}
}

// Handler exposes the Prometheus HTTP handler for the recorder's registry.
func (r *Recorder) Handler() http.Handler {
	if r == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
		})
	}
	return r.handler
}

// Gatherer returns the underlying Prometheus gatherer for tests and advanced
// integrations.
func (r *Recorder) Gatherer() prometheus.Gatherer {
	if r == nil {
		return prometheus.NewRegistry()
	}
	return r.gatherer
}

// ObserveAuth records the outcome and latency for a completed /auth request.
func (r *Recorder) ObserveAuth(endpoint, outcome string, statusCode int, fromCache bool, duration time.Duration) {
	if r == nil {
		return
	}
	endpointLabel := normalizeLabel(endpoint)
	outcomeLabel := normalizeLabel(outcome)
	statusLabel := strconv.Itoa(statusCode)
	if statusCode <= 0 {
		statusLabel = "unknown"
	}
	cacheLabel := "false"
	if fromCache {
		cacheLabel = "true"
	}
	r.authRequests.WithLabelValues(endpointLabel, outcomeLabel, statusLabel, cacheLabel).Inc()
	r.authLatency.WithLabelValues(endpointLabel, outcomeLabel).Observe(duration.Seconds())
}

// ObserveCacheLookup records the result of a cache lookup.
func (r *Recorder) ObserveCacheLookup(endpoint string, result CacheLookupOutcome, duration time.Duration) {
	if r == nil {
		return
	}
	endpointLabel := normalizeLabel(endpoint)
	resultLabel := string(result)
	if resultLabel == "" {
		resultLabel = string(CacheLookupMiss)
	}
	r.observeCache(endpointLabel, CacheOperationLookup, resultLabel, duration)
}

// ObserveCacheStore records the result of a cache store attempt.
func (r *Recorder) ObserveCacheStore(endpoint string, result CacheStoreOutcome, duration time.Duration) {
	if r == nil {
		return
	}
	endpointLabel := normalizeLabel(endpoint)
	resultLabel := string(result)
	if resultLabel == "" {
		resultLabel = string(CacheStoreError)
	}
	r.observeCache(endpointLabel, CacheOperationStore, resultLabel, duration)
}

func (r *Recorder) observeCache(endpoint string, operation CacheOperation, result string, duration time.Duration) {
	opLabel := string(operation)
	if opLabel == "" {
		opLabel = string(CacheOperationLookup)
	}
	resLabel := normalizeLabel(result)
	r.cacheOperations.WithLabelValues(endpoint, opLabel, resLabel).Inc()
	r.cacheLatency.WithLabelValues(endpoint, opLabel, resLabel).Observe(duration.Seconds())
}

func normalizeLabel(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "unknown"
	}
	return trimmed
}
