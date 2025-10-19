package metrics

import (
	"net/http/httptest"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestRecorderObserveAuth(t *testing.T) {
	rec := NewRecorder(nil)
	rec.ObserveAuth("alpha", "pass", 200, true, 250*time.Millisecond)

	families := gather(t, rec, "passctrl_auth_requests_total", "passctrl_auth_request_duration_seconds")

	tests := []struct {
		name   string
		metric string
		labels map[string]string
		assert func(t *testing.T, metric *dto.Metric)
	}{
		{
			name:   "increments counter",
			metric: "passctrl_auth_requests_total",
			labels: map[string]string{
				"endpoint":    "alpha",
				"outcome":     "pass",
				"status_code": "200",
				"from_cache":  "true",
			},
			assert: func(t *testing.T, metric *dto.Metric) {
				require.NotNil(t, metric.GetCounter(), "expected counter metric for auth requests")
				require.InDelta(t, 1, metric.GetCounter().GetValue(), 1e-9)
			},
		},
		{
			name:   "observes latency histogram",
			metric: "passctrl_auth_request_duration_seconds",
			labels: map[string]string{
				"endpoint": "alpha",
				"outcome":  "pass",
			},
			assert: func(t *testing.T, metric *dto.Metric) {
				hist := metric.GetHistogram()
				require.NotNil(t, hist, "expected histogram metric for auth latency")
				require.Equal(t, uint64(1), hist.GetSampleCount())
				want := 0.25
				require.InDelta(t, want, hist.GetSampleSum(), 0.001)
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			metric := findMetric(t, families[tc.metric], tc.labels)
			tc.assert(t, metric)
		})
	}
}

func TestRecorderObserveCacheOperations(t *testing.T) {
	rec := NewRecorder(nil)
	rec.ObserveCacheLookup("alpha", CacheLookupHit, 10*time.Millisecond)
	rec.ObserveCacheStore("alpha", CacheStoreStored, 5*time.Millisecond)

	families := gather(t, rec, "passctrl_cache_operations_total", "passctrl_cache_operation_duration_seconds")

	tests := []struct {
		name   string
		metric string
		labels map[string]string
		assert func(t *testing.T, metric *dto.Metric)
	}{
		{
			name:   "increments lookup counter",
			metric: "passctrl_cache_operations_total",
			labels: map[string]string{
				"endpoint":  "alpha",
				"operation": string(CacheOperationLookup),
				"result":    string(CacheLookupHit),
			},
			assert: func(t *testing.T, metric *dto.Metric) {
				require.NotNil(t, metric.GetCounter(), "expected counter metric for cache lookup")
				require.InDelta(t, 1, metric.GetCounter().GetValue(), 1e-9)
			},
		},
		{
			name:   "increments store counter",
			metric: "passctrl_cache_operations_total",
			labels: map[string]string{
				"endpoint":  "alpha",
				"operation": string(CacheOperationStore),
				"result":    string(CacheStoreStored),
			},
			assert: func(t *testing.T, metric *dto.Metric) {
				require.NotNil(t, metric.GetCounter(), "expected counter metric for cache store")
				require.InDelta(t, 1, metric.GetCounter().GetValue(), 1e-9)
			},
		},
		{
			name:   "records store latency",
			metric: "passctrl_cache_operation_duration_seconds",
			labels: map[string]string{
				"endpoint":  "alpha",
				"operation": string(CacheOperationStore),
				"result":    string(CacheStoreStored),
			},
			assert: func(t *testing.T, metric *dto.Metric) {
				hist := metric.GetHistogram()
				require.NotNil(t, hist, "expected histogram metric for cache store latency")
				require.Equal(t, uint64(1), hist.GetSampleCount())
				want := 0.005
				require.InDelta(t, want, hist.GetSampleSum(), 0.001)
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			metric := findMetric(t, families[tc.metric], tc.labels)
			tc.assert(t, metric)
		})
	}
}

func TestRecorderHandler(t *testing.T) {
	rec := NewRecorder(nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)

	t.Run("responds with metrics", func(t *testing.T) {
		rec.Handler().ServeHTTP(rr, req)
		require.Equal(t, 200, rr.Code)
		require.NotZero(t, rr.Body.Len(), "expected response body")
	})
}

func gather(t *testing.T, rec *Recorder, names ...string) map[string][]*dto.Metric {
	t.Helper()
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	families, err := rec.Gatherer().Gather()
	require.NoError(t, err)
	collected := make(map[string][]*dto.Metric, len(names))
	for _, mf := range families {
		if !wanted[mf.GetName()] {
			continue
		}
		collected[mf.GetName()] = append(collected[mf.GetName()], mf.GetMetric()...)
	}
	for _, name := range names {
		require.NotEmpty(t, collected[name], "metric %q not collected", name)
	}
	return collected
}

func findMetric(t *testing.T, metrics []*dto.Metric, labels map[string]string) *dto.Metric {
	t.Helper()
	for _, metric := range metrics {
		if matchLabels(metric, labels) {
			return metric
		}
	}
	require.Failf(t, "metric not found", "metric with labels %v not found", labels)
	return nil
}

func matchLabels(metric *dto.Metric, labels map[string]string) bool {
	if len(metric.GetLabel()) < len(labels) {
		return false
	}
	for key, expected := range labels {
		found := false
		for _, label := range metric.GetLabel() {
			if label.GetName() == key && label.GetValue() == expected {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
