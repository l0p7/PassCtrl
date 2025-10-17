package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/runtime/rulechain"
	"github.com/l0p7/passctrl/internal/templates"
)

func TestRuleExecutionAgentBackendDefaultFailWhenNotAccepted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	t.Cleanup(server.Close)

	def := compileBackendOnlyRule(t, server.URL, []int{http.StatusOK})

	agent := newRuleExecutionAgent(server.Client(), nil, nil)
	state := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil), "endpoint", "cache-key", "")

	outcome, reason := agent.evaluateRule(context.Background(), def, state)

	if outcome != "fail" {
		t.Fatalf("expected fail outcome, got %q", outcome)
	}
	if state.Backend.Accepted {
		t.Fatalf("expected backend accepted to be false")
	}
	if !strings.Contains(reason, "status 500") {
		t.Fatalf("expected failure reason to mention backend status, got %q", reason)
	}
}

func TestRuleExecutionAgentBackendDefaultPassWhenAccepted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(server.Close)

	def := compileBackendOnlyRule(t, server.URL, []int{http.StatusOK})

	agent := newRuleExecutionAgent(server.Client(), nil, nil)
	state := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil), "endpoint", "cache-key", "")

	outcome, reason := agent.evaluateRule(context.Background(), def, state)

	if outcome != "pass" {
		t.Fatalf("expected pass outcome, got %q", outcome)
	}
	if !state.Backend.Accepted {
		t.Fatalf("expected backend accepted to be true")
	}
	if reason != "rule evaluated without explicit outcome" {
		t.Fatalf("unexpected pass reason %q", reason)
	}
}

func compileBackendOnlyRule(t *testing.T, url string, accepted []int) rulechain.Definition {
	t.Helper()

	renderer := templates.NewRenderer(nil)
	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
		Name: "backend-only",
		Backend: rulechain.BackendDefinitionSpec{
			URL:      url,
			Accepted: accepted,
		},
	}}, renderer)
	if err != nil {
		t.Fatalf("compile definitions: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(defs))
	}
	return defs[0]
}
