package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/runtime/rulechain"
	"github.com/l0p7/passctrl/internal/templates"
	"github.com/stretchr/testify/require"
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

	require.Equal(t, "fail", outcome)
	require.False(t, state.Backend.Accepted)
	require.Contains(t, reason, "status 500")
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

	require.Equal(t, "pass", outcome)
	require.True(t, state.Backend.Accepted)
	require.Equal(t, "rule evaluated without explicit outcome", reason)
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
	require.NoError(t, err)
	require.Len(t, defs, 1)
	return defs[0]
}
