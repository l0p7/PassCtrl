package metrics

import (
	"math"
	"net/http/httptest"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
)

func TestRecorderObserveAuth(t *testing.T) {
	rec := NewRecorder(nil)
	rec.ObserveAuth("alpha", "pass", 200, true, 250*time.Millisecond)

	families := gather(t, rec, "passctrl_auth_requests_total", "passctrl_auth_request_duration_seconds")

	counter := findMetric(t, families["passctrl_auth_requests_total"], map[string]string{
		"endpoint":    "alpha",
		"outcome":     "pass",
		"status_code": "200",
		"from_cache":  "true",
	})
	if counter.GetCounter() == nil {
		t.Fatalf("expected counter metric for auth requests")
	}
	if got := counter.GetCounter().GetValue(); got != 1 {
		t.Fatalf("expected counter value 1, got %v", got)
	}

	histMetric := findMetric(t, families["passctrl_auth_request_duration_seconds"], map[string]string{
		"endpoint": "alpha",
		"outcome":  "pass",
	})
	hist := histMetric.GetHistogram()
	if hist == nil {
		t.Fatalf("expected histogram metric for auth latency")
	}
	if hist.GetSampleCount() != 1 {
		t.Fatalf("expected histogram count 1, got %d", hist.GetSampleCount())
	}
	want := 0.25
	if diff := math.Abs(hist.GetSampleSum() - want); diff > 0.001 {
		t.Fatalf("expected histogram sum near %v, got %v", want, hist.GetSampleSum())
	}
}

func TestRecorderObserveCacheOperations(t *testing.T) {
	rec := NewRecorder(nil)
	rec.ObserveCacheLookup("alpha", CacheLookupHit, 10*time.Millisecond)
	rec.ObserveCacheStore("alpha", CacheStoreStored, 5*time.Millisecond)

	families := gather(t, rec, "passctrl_cache_operations_total", "passctrl_cache_operation_duration_seconds")

	lookupMetric := findMetric(t, families["passctrl_cache_operations_total"], map[string]string{
		"endpoint":  "alpha",
		"operation": string(CacheOperationLookup),
		"result":    string(CacheLookupHit),
	})
	if lookupMetric.GetCounter() == nil {
		t.Fatalf("expected counter metric for cache lookup")
	}
	if got := lookupMetric.GetCounter().GetValue(); got != 1 {
		t.Fatalf("expected lookup counter 1, got %v", got)
	}

	storeMetric := findMetric(t, families["passctrl_cache_operations_total"], map[string]string{
		"endpoint":  "alpha",
		"operation": string(CacheOperationStore),
		"result":    string(CacheStoreStored),
	})
	if storeMetric.GetCounter() == nil {
		t.Fatalf("expected counter metric for cache store")
	}
	if got := storeMetric.GetCounter().GetValue(); got != 1 {
		t.Fatalf("expected store counter 1, got %v", got)
	}

	latencyMetric := findMetric(t, families["passctrl_cache_operation_duration_seconds"], map[string]string{
		"endpoint":  "alpha",
		"operation": string(CacheOperationStore),
		"result":    string(CacheStoreStored),
	})
	hist := latencyMetric.GetHistogram()
	if hist == nil {
		t.Fatalf("expected histogram metric for cache store latency")
	}
	if hist.GetSampleCount() != 1 {
		t.Fatalf("expected histogram count 1, got %d", hist.GetSampleCount())
	}
	want := 0.005
	if diff := math.Abs(hist.GetSampleSum() - want); diff > 0.001 {
		t.Fatalf("expected histogram sum near %v, got %v", want, hist.GetSampleSum())
	}
}

func TestRecorderHandler(t *testing.T) {
	rec := NewRecorder(nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)

	rec.Handler().ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected 200 response, got %d", rr.Code)
	}
	if rr.Body.Len() == 0 {
		t.Fatalf("expected response body")
	}
}

func gather(t *testing.T, rec *Recorder, names ...string) map[string][]*dto.Metric {
	t.Helper()
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	families, err := rec.Gatherer().Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	collected := make(map[string][]*dto.Metric, len(names))
	for _, mf := range families {
		if !wanted[mf.GetName()] {
			continue
		}
		collected[mf.GetName()] = append(collected[mf.GetName()], mf.GetMetric()...)
	}
	for _, name := range names {
		if len(collected[name]) == 0 {
			t.Fatalf("metric %q not collected", name)
		}
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
	t.Fatalf("metric with labels %v not found", labels)
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
