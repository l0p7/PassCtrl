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
	"github.com/l0p7/passctrl/internal/runtime/cache"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/server"
)

type authPayload struct {
	Outcome       string            `json:"outcome"`
	Message       string            `json:"message"`
	Agents        []pipeline.Result `json:"agents"`
	Endpoint      string            `json:"endpoint"`
	CorrelationID string            `json:"correlationId"`
}

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
				ForwardRequestPolicy: config.EndpointForwardRequestPolicyConfig{
					Query: config.ForwardRuleCategoryConfig{Allow: []string{"allow"}},
				},
				Rules: []config.EndpointRuleReference{{Name: "allow-rule"}},
			},
			"deny": {
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
		req.Header.Set("Authorization", "token")
		req.Header.Set("X-Request-ID", "deny-123")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected status 403, got %d", rec.Code)
		}
		if header := rec.Header().Get("X-Request-ID"); header != "deny-123" {
			t.Fatalf("expected correlation header to echo request id, got %q", header)
		}

		var payload authPayload
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if payload.Outcome != "fail" {
			t.Fatalf("expected fail outcome, got %s", payload.Outcome)
		}
		if payload.Message != "denied by rule" {
			t.Fatalf("expected message from rule, got %s", payload.Message)
		}
		if payload.Endpoint != "deny" {
			t.Fatalf("expected endpoint field to reflect selection, got %s", payload.Endpoint)
		}
		if payload.CorrelationID != "deny-123" {
			t.Fatalf("expected correlationId to be echoed, got %s", payload.CorrelationID)
		}
	})

	t.Run("allow endpoint passes when query matches", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/allow/auth?allow=true", http.NoBody)
		req.Header.Set("Authorization", "token")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if header := rec.Header().Get("X-Request-ID"); header == "" {
			t.Fatalf("expected generated correlation header when request omitted it")
		}

		var payload authPayload
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if payload.Outcome != "pass" {
			t.Fatalf("expected pass outcome, got %s", payload.Outcome)
		}
		if payload.CorrelationID == "" {
			t.Fatalf("expected response payload to include generated correlationId")
		}
	})

	t.Run("missing endpoint requires selector when multiple configured", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/auth", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", rec.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("failed to decode error payload: %v", err)
		}
		if payload["error"] == "" {
			t.Fatalf("expected error message in payload")
		}
	})

	t.Run("unknown endpoint path yields not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/unknown/auth", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", rec.Code)
		}
	})

	t.Run("aggregate health available at canonical path", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/health", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
	})

	t.Run("scoped health requires known endpoint", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/deny/healthz", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
	})

	t.Run("scoped health accepts health alias", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/deny/health", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
	})

	t.Run("unknown endpoint health returns not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/missing/healthz", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", rec.Code)
		}
	})

	t.Run("unknown endpoint health alias returns not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/missing/health", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", rec.Code)
		}
	})
}

func TestPipelineLogsIncludeCorrelationID(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	opts := PipelineOptions{
		Cache: cache.NewMemory(1 * time.Minute),
		Endpoints: map[string]config.EndpointConfig{
			"default": {
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
	req.Header.Set("Authorization", "token")
	req.Header.Set("X-Request-ID", "log-correlation")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	lines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	if len(lines) == 0 {
		t.Fatalf("expected log entries to be recorded")
	}

	foundCorrelation := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("failed to decode log entry: %v", err)
		}
		if entry["correlation_id"] == "log-correlation" {
			foundCorrelation = true
			if _, ok := entry["latency_ms"]; !ok {
				t.Fatalf("expected latency_ms field in correlation log entry")
			}
		}
	}

	if !foundCorrelation {
		t.Fatalf("expected at least one log entry to include the correlation id")
	}
}

func TestPipelineSingleEndpointDefaults(t *testing.T) {
	opts := PipelineOptions{
		Cache: cache.NewMemory(1 * time.Minute),
		Endpoints: map[string]config.EndpointConfig{
			"solo": {
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
	req.Header.Set("Authorization", "token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var payload authPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.Endpoint != "solo" {
		t.Fatalf("expected default endpoint to be applied, got %s", payload.Endpoint)
	}
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

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}

		var payload explainPayload
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("failed to decode explain payload: %v", err)
		}
		if payload.Status != "degraded" {
			t.Fatalf("expected degraded status when skips present, got %s", payload.Status)
		}
		if len(payload.RuleSources) != 2 {
			t.Fatalf("expected rule sources to be surfaced, got %v", payload.RuleSources)
		}
		if len(payload.SkippedDefinitions) != 1 {
			t.Fatalf("expected skipped definitions to be included, got %v", payload.SkippedDefinitions)
		}
		if len(payload.AvailableEndpoints) == 0 {
			t.Fatalf("expected available endpoints to be reported")
		}
	})

	t.Run("scoped explain validates endpoint", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/solo/explain", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}

		var payload explainPayload
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("failed to decode scoped explain: %v", err)
		}
		if payload.Endpoint != "solo" {
			t.Fatalf("expected endpoint hint to be echoed, got %s", payload.Endpoint)
		}
	})

	t.Run("unknown endpoint explain returns not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/missing/explain", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", rec.Code)
		}
	})
}

func TestPipelineReloadInvalidatesCache(t *testing.T) {
	ctx := context.Background()
	cacheBackend := cache.NewMemory(5 * time.Minute)
	opts := PipelineOptions{
		Cache: cacheBackend,
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
	req.Header.Set("Authorization", "token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected initial pass, got %d", rec.Code)
	}

	// Reload with a failing rule to ensure cached decision is purged.
	bundle := config.RuleBundle{
		Endpoints: map[string]config.EndpointConfig{
			"solo": {Rules: []config.EndpointRuleReference{{Name: "solo-rule"}}},
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
	req.Header.Set("Authorization", "token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected reload to force fresh evaluation, got %d", rec.Code)
	}

	var payload authPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode post-reload payload: %v", err)
	}
	if payload.Outcome != "fail" {
		t.Fatalf("expected fail outcome after reload, got %s", payload.Outcome)
	}

	// Verify explain metadata now reflects the new source.
	explainReq := httptest.NewRequest(http.MethodGet, "http://example.com/explain", http.NoBody)
	explainRec := httptest.NewRecorder()
	handler.ServeHTTP(explainRec, explainReq)
	if explainRec.Code != http.StatusOK {
		t.Fatalf("expected explain to succeed after reload, got %d", explainRec.Code)
	}
	var explain explainPayload
	if err := json.Unmarshal(explainRec.Body.Bytes(), &explain); err != nil {
		t.Fatalf("failed to decode explain payload after reload: %v", err)
	}
	if len(explain.RuleSources) != 1 || explain.RuleSources[0] != "updated" {
		t.Fatalf("expected rule sources to reflect reload, got %v", explain.RuleSources)
	}
}
