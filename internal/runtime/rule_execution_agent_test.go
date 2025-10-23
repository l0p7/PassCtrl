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

func TestRuleExecutionAgentAuthForwardsBearer(t *testing.T) {
	const targetURL = "https://backend.test/auth"
	client := runtimemocks.NewMockHTTPDoer(t)
	client.EXPECT().
		Do(mock.AnythingOfType("*http.Request")).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			require.Equal(t, "Bearer token-123", req.Header.Get("Authorization"))
			return newBackendResponse(http.StatusOK, `{}`, map[string]string{}), nil
		})

	def := compileRuleWithAuth(t, []rulechain.AuthDirectiveSpec{{Type: "bearer"}}, targetURL, []int{http.StatusOK})

	agent := newRuleExecutionAgent(client, nil, nil)
	state := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil), "endpoint", "cache-key", "")
	state.Admission.Credentials = []pipeline.AdmissionCredential{{
		Type:   "bearer",
		Token:  "token-123",
		Source: "authorization",
	}}

	outcome, reason := agent.evaluateRule(context.Background(), def, state)
	require.Equal(t, "pass", outcome)
	require.Equal(t, "rule evaluated without explicit outcome", reason)
	require.Equal(t, "bearer", state.Rule.Auth.Selected)
	require.Equal(t, "token-123", state.Rule.Auth.Input["token"])
}

func TestRuleExecutionAgentAuthForwardAsHeaderTemplate(t *testing.T) {
	const targetURL = "https://backend.test/auth"
	client := runtimemocks.NewMockHTTPDoer(t)
	client.EXPECT().
		Do(mock.AnythingOfType("*http.Request")).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			require.Equal(t, "Bearer abc-123", req.Header.Get("Authorization"))
			return newBackendResponse(http.StatusOK, `{}`, map[string]string{}), nil
		})

	def := compileRuleWithAuth(t, []rulechain.AuthDirectiveSpec{
		{
			Type: "header",
			Name: "X-Api-Token",
			Forward: rulechain.AuthForwardSpec{
				Type:  "header",
				Name:  "Authorization",
				Value: "Bearer {{ .auth.input.value }}",
			},
		},
	}, targetURL, []int{http.StatusOK})

	agent := newRuleExecutionAgent(client, nil, templates.NewRenderer(nil))
	state := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil), "endpoint", "cache-key", "")
	state.Admission.Credentials = []pipeline.AdmissionCredential{{
		Type:   "header",
		Name:   "X-Api-Token",
		Value:  "abc-123",
		Source: "header:X-Api-Token",
	}}

	outcome, _ := agent.evaluateRule(context.Background(), def, state)
	require.Equal(t, "pass", outcome)
	require.Equal(t, "header", state.Rule.Auth.Selected)
	require.Equal(t, "abc-123", state.Rule.Auth.Input["value"])
	require.Equal(t, "Authorization", state.Rule.Auth.Forward["name"])
}

func TestRuleExecutionAgentAuthFailsWhenNoMatch(t *testing.T) {
	const targetURL = "https://backend.test/auth"
	client := runtimemocks.NewMockHTTPDoer(t)

	def := compileRuleWithAuth(t, []rulechain.AuthDirectiveSpec{{Type: "bearer"}}, targetURL, []int{http.StatusOK})

	agent := newRuleExecutionAgent(client, nil, nil)
	state := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil), "endpoint", "cache-key", "")
	state.Admission.Credentials = []pipeline.AdmissionCredential{{
		Type:  "header",
		Name:  "X-Api-Token",
		Value: "abc-123",
	}}

	outcome, reason := agent.evaluateRule(context.Background(), def, state)
	require.Equal(t, "fail", outcome)
	require.Contains(t, reason, "authentication")
	require.Empty(t, state.Rule.Auth.Selected)
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

func compileRuleWithAuth(t *testing.T, auth []rulechain.AuthDirectiveSpec, url string, accepted []int) rulechain.Definition {
	t.Helper()

	renderer := templates.NewRenderer(nil)
	spec := rulechain.DefinitionSpec{
		Name: "auth-rule",
		Auth: auth,
	}
	if strings.TrimSpace(url) != "" {
		spec.Backend = rulechain.BackendDefinitionSpec{
			URL:      url,
			Accepted: accepted,
		}
	}
	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{spec}, renderer)
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
