package runtime

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	runtimemocks "github.com/l0p7/passctrl/internal/mocks/runtime"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/runtime/rulechain"
	"github.com/l0p7/passctrl/internal/templates"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRuleExecutionAgentBackendDefaultFailWhenNotAccepted(t *testing.T) {
	const targetURL = "https://backend.test/fail"
	mockClient := runtimemocks.NewMockHTTPDoer(t)
	mockClient.EXPECT().
		Do(mock.AnythingOfType("*http.Request")).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			require.Equal(t, targetURL, req.URL.String())
			return newBackendResponse(http.StatusInternalServerError, `{"error":"boom"}`, map[string]string{"Content-Type": "application/json"}), nil
		})

	def := compileBackendOnlyRule(t, targetURL, []int{http.StatusOK})

	agent := newRuleExecutionAgent(mockClient, nil, nil)
	state := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil), "endpoint", "cache-key", "")

	outcome, reason := agent.evaluateRule(context.Background(), def, state)

	require.Equal(t, "fail", outcome)
	require.False(t, state.Backend.Accepted)
	require.Contains(t, reason, "status 500")
}

func TestRuleExecutionAgentBackendDefaultPassWhenAccepted(t *testing.T) {
	const targetURL = "https://backend.test/pass"
	mockClient := runtimemocks.NewMockHTTPDoer(t)
	mockClient.EXPECT().
		Do(mock.AnythingOfType("*http.Request")).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			require.Equal(t, targetURL, req.URL.String())
			return newBackendResponse(http.StatusOK, `{"status":"ok"}`, map[string]string{"Content-Type": "application/json"}), nil
		})

	def := compileBackendOnlyRule(t, targetURL, []int{http.StatusOK})

	agent := newRuleExecutionAgent(mockClient, nil, nil)
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

func newBackendResponse(status int, body string, headers map[string]string) *http.Response {
	resp := &http.Response{
		StatusCode: status,
		Header:     make(http.Header, len(headers)),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	for key, value := range headers {
		resp.Header.Set(key, value)
	}
	return resp
}
