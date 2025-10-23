package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/l0p7/passctrl/internal/config"
	"github.com/l0p7/passctrl/internal/metrics"
	metricsmocks "github.com/l0p7/passctrl/internal/mocks/metrics"
	"github.com/l0p7/passctrl/internal/runtime/cache"
	"github.com/l0p7/passctrl/internal/server"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The /auth response is intentionally near-empty by default; tests should not
// depend on a JSON payload structure.

type explainPayload struct {
	Status             string                  `json:"status"`
	Endpoint           string                  `json:"endpoint"`
	UsingFallback      bool                    `json:"usingFallback"`
	RuleSources        []string                `json:"ruleSources"`
	SkippedDefinitions []config.DefinitionSkip `json:"skippedDefinitions"`
	AvailableEndpoints []string                `json:"availableEndpoints"`
}

func TestPipelineEndpointSelectionAndRules(t *testing.T) {
	opts := PipelineOptions{
		Cache: cache.NewMemory(1 * time.Minute),
		Endpoints: map[string]config.EndpointConfig{
			"allow": {
				Authentication: config.EndpointAuthenticationConfig{
					Allow: config.EndpointAuthAllowConfig{Authorization: []string{"bearer"}},
				},
				ForwardRequestPolicy: config.EndpointForwardRequestPolicyConfig{
					Query: config.ForwardRuleCategoryConfig{Allow: []string{"allow"}},
				},
				Rules: []config.EndpointRuleReference{{Name: "allow-rule"}},
			},
			"deny": {
				Authentication: config.EndpointAuthenticationConfig{
					Allow: config.EndpointAuthAllowConfig{Authorization: []string{"bearer"}},
				},
				ForwardRequestPolicy: config.EndpointForwardRequestPolicyConfig{
					Query: config.ForwardRuleCategoryConfig{Allow: []string{"deny"}},
				},
				Rules: []config.EndpointRuleReference{{Name: "deny-rule"}},
			},
		},
		Rules: map[string]config.RuleConfig{
			"allow-rule": {
				Conditions: config.RuleConditionConfig{
					Pass: []string{`forward.query["allow"] == "true"`},
				},
			},
			"deny-rule": {
				Conditions: config.RuleConditionConfig{
					Fail: []string{`forward.query["deny"] == "true"`},
				},
				Responses: config.RuleResponsesConfig{
					Fail: config.RuleResponseConfig{Body: "denied by rule"},
				},
			},
		},
	}
	opts.CorrelationHeader = "X-Request-ID"

	pipe := NewPipeline(nil, opts)
	handler := server.NewPipelineHandler(pipe)

	t.Run("deny endpoint applies fail condition", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/deny/auth?deny=true", http.NoBody)
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("X-Request-ID", "deny-123")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusForbidden, rec.Code)
		require.Equal(t, "deny-123", rec.Header().Get("X-Request-ID"))

		// Minimal response: only the body when intentionally constructed.
		require.Equal(t, "denied by rule", strings.TrimSpace(rec.Body.String()))
		require.Equal(t, "deny-123", rec.Header().Get("X-Request-ID"))
	})

	t.Run("allow endpoint passes when query matches", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/allow/auth?allow=true", http.NoBody)
		req.Header.Set("Authorization", "Bearer token")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.NotEmpty(t, rec.Header().Get("X-Request-ID"), "expected generated correlation header when request omitted it")

		// Default pass has no body unless intentionally constructed.
		require.Empty(t, strings.TrimSpace(rec.Body.String()))
		require.NotEmpty(t, rec.Header().Get("X-Request-ID"))
	})

	t.Run("missing endpoint requires selector when multiple configured", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusBadRequest, rec.Code)

		var payload map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
		require.NotEmpty(t, payload["error"])
	})

	t.Run("unknown endpoint path yields not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/unknown/auth", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("aggregate health available at canonical path", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/health", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("scoped health requires known endpoint", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/deny/healthz", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("scoped health accepts health alias", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/deny/health", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("unknown endpoint health returns not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/missing/healthz", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("unknown endpoint health alias returns not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/missing/health", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusNotFound, rec.Code)
	})
}

func TestPipelineLogsIncludeCorrelationID(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	opts := PipelineOptions{
		Cache: cache.NewMemory(1 * time.Minute),
		Endpoints: map[string]config.EndpointConfig{
			"default": {
				Authentication: config.EndpointAuthenticationConfig{
					Allow: config.EndpointAuthAllowConfig{Authorization: []string{"bearer"}},
				},
				ForwardRequestPolicy: config.EndpointForwardRequestPolicyConfig{
					Query: config.ForwardRuleCategoryConfig{Allow: []string{"allow"}},
				},
				Rules: []config.EndpointRuleReference{{Name: "allow-rule"}},
			},
		},
		Rules: map[string]config.RuleConfig{
			"allow-rule": {
				Conditions: config.RuleConditionConfig{
					Pass: []string{`forward.query["allow"] == "true"`},
				},
			},
		},
		CorrelationHeader: "X-Request-ID",
	}

	pipe := NewPipeline(logger, opts)
	handler := server.NewPipelineHandler(pipe)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/default/auth?allow=true", http.NoBody)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Request-ID", "log-correlation")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	lines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	require.NotEmpty(t, lines, "expected log entries to be recorded")

	foundCorrelation := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), "failed to decode log entry")
		if entry["correlation_id"] == "log-correlation" {
			foundCorrelation = true
			require.Contains(t, entry, "latency_ms", "expected latency_ms field in correlation log entry")
		}
	}

	require.True(t, foundCorrelation, "expected at least one log entry to include the correlation id")
}

func TestPipelineRecordsMetrics(t *testing.T) {
	metricsMock := metricsmocks.NewMockRecorder(t)
	metricsMock.EXPECT().ObserveCacheLookup("default", metrics.CacheLookupMiss, mock.AnythingOfType("time.Duration")).Once()
	metricsMock.EXPECT().ObserveAuth("default", "pass", http.StatusOK, false, mock.AnythingOfType("time.Duration")).Once()
	metricsMock.EXPECT().ObserveCacheStore("default", metrics.CacheStoreStored, mock.AnythingOfType("time.Duration")).Once()

	opts := PipelineOptions{
		Cache: cache.NewMemory(1 * time.Minute),
		Endpoints: map[string]config.EndpointConfig{
			"default": {
				Authentication: config.EndpointAuthenticationConfig{
					Allow: config.EndpointAuthAllowConfig{Authorization: []string{"bearer"}},
				},
				ForwardRequestPolicy: config.EndpointForwardRequestPolicyConfig{
					Query: config.ForwardRuleCategoryConfig{Allow: []string{"allow"}},
				},
				Rules: []config.EndpointRuleReference{{Name: "allow-rule"}},
			},
		},
		Rules: map[string]config.RuleConfig{
			"allow-rule": {
				Conditions: config.RuleConditionConfig{
					Pass: []string{`forward.query["allow"] == "true"`},
				},
			},
		},
		Metrics: metricsMock,
	}

	pipe := NewPipeline(nil, opts)
	handler := server.NewPipelineHandler(pipe)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/default/auth?allow=true", http.NoBody)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestPipelineSingleEndpointDefaults(t *testing.T) {
	opts := PipelineOptions{
		Cache: cache.NewMemory(1 * time.Minute),
		Endpoints: map[string]config.EndpointConfig{
			"solo": {
				Authentication: config.EndpointAuthenticationConfig{
					Allow: config.EndpointAuthAllowConfig{Authorization: []string{"bearer"}},
				},
				ForwardRequestPolicy: config.EndpointForwardRequestPolicyConfig{
					Query: config.ForwardRuleCategoryConfig{Allow: []string{"allow"}},
				},
				Rules: []config.EndpointRuleReference{{Name: "solo-rule"}},
			},
		},
		Rules: map[string]config.RuleConfig{
			"solo-rule": {
				Conditions: config.RuleConditionConfig{
					Pass: []string{`forward.query["allow"] == "true"`},
				},
			},
		},
	}

	pipe := NewPipeline(nil, opts)
	handler := server.NewPipelineHandler(pipe)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/solo/auth?allow=true", http.NoBody)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, strings.TrimSpace(rec.Body.String()), "expected empty body on pass by default")
}

func TestPipelineExplainReflectsMetadata(t *testing.T) {
	opts := PipelineOptions{
		Cache: cache.NewMemory(1 * time.Minute),
		Endpoints: map[string]config.EndpointConfig{
			"solo": {
				Rules: []config.EndpointRuleReference{{Name: "solo-rule"}},
			},
		},
		Rules: map[string]config.RuleConfig{
			"solo-rule": {
				Conditions: config.RuleConditionConfig{
					Pass: []string{"true"},
				},
			},
		},
		RuleSources: []string{"inline-config", "rules/a.yaml"},
		SkippedDefinitions: []config.DefinitionSkip{{
			Kind:    "rule",
			Name:    "orphan",
			Reason:  "duplicate definition",
			Sources: []string{"rules/b.yaml"},
		}},
	}

	pipe := NewPipeline(nil, opts)
	handler := server.NewPipelineHandler(pipe)

	t.Run("aggregate explain reports metadata", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/explain", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)

		var payload explainPayload
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
		require.Equal(t, "degraded", payload.Status)
		require.Len(t, payload.RuleSources, 2)
		require.Len(t, payload.SkippedDefinitions, 1)
		require.NotEmpty(t, payload.AvailableEndpoints)
	})

	t.Run("scoped explain validates endpoint", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/solo/explain", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)

		var payload explainPayload
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
		require.Equal(t, "solo", payload.Endpoint)
	})

	t.Run("unknown endpoint explain returns not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/missing/explain", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusNotFound, rec.Code)
	})
}

func TestPipelineReloadInvalidatesCache(t *testing.T) {
	ctx := context.Background()
	cacheBackend := cache.NewMemory(5 * time.Minute)
	opts := PipelineOptions{
		Cache: cacheBackend,
		Endpoints: map[string]config.EndpointConfig{
			"solo": {
				Authentication: config.EndpointAuthenticationConfig{
					Allow: config.EndpointAuthAllowConfig{Authorization: []string{"bearer"}},
				},
				Rules: []config.EndpointRuleReference{{Name: "solo-rule"}},
			},
		},
		Rules: map[string]config.RuleConfig{
			"solo-rule": {
				Conditions: config.RuleConditionConfig{
					Pass: []string{"true"},
				},
				Responses: config.RuleResponsesConfig{
					Pass: config.RuleResponseConfig{Body: "allowed"},
				},
			},
		},
		RuleSources: []string{"initial"},
	}

	pipe := NewPipeline(nil, opts)
	handler := server.NewPipelineHandler(pipe)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/solo/auth", http.NoBody)
	req.Header.Set("Authorization", "Bearer token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Reload with a failing rule to ensure cached decision is purged.
	bundle := config.RuleBundle{
		Endpoints: map[string]config.EndpointConfig{
			"solo": {
				Authentication: config.EndpointAuthenticationConfig{
					Allow: config.EndpointAuthAllowConfig{Authorization: []string{"bearer"}},
				},
				Rules: []config.EndpointRuleReference{{Name: "solo-rule"}},
			},
		},
		Rules: map[string]config.RuleConfig{
			"solo-rule": {
				Conditions: config.RuleConditionConfig{
					Fail: []string{"true"},
				},
				Responses: config.RuleResponsesConfig{
					Fail: config.RuleResponseConfig{Body: "denied"},
				},
			},
		},
		Sources: []string{"updated"},
	}

	pipe.Reload(ctx, bundle)

	req = httptest.NewRequest(http.MethodGet, "http://example.com/solo/auth", http.NoBody)
	req.Header.Set("Authorization", "Bearer token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// Minimal body; just verify status code reflects fail after reload.

	// Verify explain metadata now reflects the new source.
	explainReq := httptest.NewRequest(http.MethodGet, "http://example.com/explain", http.NoBody)
	explainRec := httptest.NewRecorder()
	handler.ServeHTTP(explainRec, explainReq)
	require.Equal(t, http.StatusOK, explainRec.Code)
	var explain explainPayload
	require.NoError(t, json.Unmarshal(explainRec.Body.Bytes(), &explain))
	require.Equal(t, []string{"updated"}, explain.RuleSources)
}

func TestAuthResponseIsMinimal(t *testing.T) {
	opts := PipelineOptions{
		Cache: cache.NewMemory(1 * time.Minute),
		Endpoints: map[string]config.EndpointConfig{
			"solo": {
				Authentication: config.EndpointAuthenticationConfig{
					Allow: config.EndpointAuthAllowConfig{Authorization: []string{"bearer"}},
				},
				ForwardRequestPolicy: config.EndpointForwardRequestPolicyConfig{
					Query: config.ForwardRuleCategoryConfig{Allow: []string{"allow"}},
				},
				Rules: []config.EndpointRuleReference{{Name: "solo-rule"}},
			},
		},
		Rules: map[string]config.RuleConfig{
			"solo-rule": {Conditions: config.RuleConditionConfig{Pass: []string{`forward.query["allow"] == "true"`}}},
		},
		CorrelationHeader: "X-Request-ID",
	}

	pipe := NewPipeline(nil, opts)
	handler := server.NewPipelineHandler(pipe)

	// Pass case
	req := httptest.NewRequest(http.MethodGet, "http://example.com/solo/auth?allow=true", http.NoBody)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Request-ID", "minimal-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, strings.TrimSpace(rec.Body.String()), "expected empty response body on pass")

	// Fail case in a fresh pipeline to avoid cache influence
	failOpts := PipelineOptions{
		Cache: cache.NewMemory(1 * time.Minute),
		Endpoints: map[string]config.EndpointConfig{
			"solo": {
				Authentication: config.EndpointAuthenticationConfig{
					Allow: config.EndpointAuthAllowConfig{Authorization: []string{"bearer"}},
				},
				ForwardRequestPolicy: config.EndpointForwardRequestPolicyConfig{
					Query: config.ForwardRuleCategoryConfig{Allow: []string{"deny"}},
				},
				Rules: []config.EndpointRuleReference{{Name: "solo-rule"}},
			},
		},
		Rules: map[string]config.RuleConfig{
			"solo-rule": {Conditions: config.RuleConditionConfig{Fail: []string{`forward.query["deny"] == "true"`}}},
		},
		CorrelationHeader: "X-Request-ID",
	}
	failPipe := NewPipeline(nil, failOpts)
	failHandler := server.NewPipelineHandler(failPipe)
	req = httptest.NewRequest(http.MethodGet, "http://example.com/solo/auth?deny=true", http.NoBody)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Request-ID", "minimal-2")
	rec = httptest.NewRecorder()
	failHandler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Empty(t, strings.TrimSpace(rec.Body.String()), "expected empty response body on fail")
}

func TestEndpointResponsePolicyBodiesAndHeaders(t *testing.T) {
	// Endpoint defines pass/fail bodies and custom header with templating.
	opts := PipelineOptions{
		Cache: cache.NewMemory(1 * time.Minute),
		Endpoints: map[string]config.EndpointConfig{
			"e": {
				Authentication: config.EndpointAuthenticationConfig{
					Allow: config.EndpointAuthAllowConfig{Authorization: []string{"bearer"}},
				},
				ResponsePolicy: config.EndpointResponsePolicyConfig{
					Pass: config.EndpointResponseConfig{
						Body: "Okay",
						Headers: config.ForwardRuleCategoryConfig{
							Custom: map[string]string{
								"X-Custom":     "ep-{{ .endpoint }}",
								"Content-Type": "application/json",
							},
						},
					},
					Fail: config.EndpointResponseConfig{Body: "Denied"},
				},
				Rules: []config.EndpointRuleReference{{Name: "r"}},
			},
		},
		Rules: map[string]config.RuleConfig{
			"r": {Conditions: config.RuleConditionConfig{Pass: []string{"true"}}},
		},
		CorrelationHeader: "X-Request-ID",
	}
	pipe := NewPipeline(nil, opts)
	handler := server.NewPipelineHandler(pipe)

	// Pass path returns configured body and header
	req := httptest.NewRequest(http.MethodGet, "http://example.com/e/auth", http.NoBody)
	req.Header.Set("Authorization", "Bearer token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "Okay", strings.TrimSpace(rec.Body.String()))
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.Equal(t, "ep-e", rec.Header().Get("X-Custom"))

	// Reload rules to force fail outcome
	bundle := config.RuleBundle{
		Endpoints: map[string]config.EndpointConfig{
			"e": {
				Authentication: opts.Endpoints["e"].Authentication,
				ResponsePolicy: opts.Endpoints["e"].ResponsePolicy,
				Rules:          []config.EndpointRuleReference{{Name: "r"}},
			},
		},
		Rules: map[string]config.RuleConfig{"r": {Conditions: config.RuleConditionConfig{Fail: []string{"true"}}}},
	}
	pipe.Reload(context.Background(), bundle)

	req = httptest.NewRequest(http.MethodGet, "http://example.com/e/auth", http.NoBody)
	req.Header.Set("Authorization", "Bearer token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "Denied", strings.TrimSpace(rec.Body.String()))
	require.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
}
