package runtime

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	runtimemocks "github.com/l0p7/passctrl/internal/mocks/runtime"
	"github.com/l0p7/passctrl/internal/runtime/forwardpolicy"
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

	backendAgent := newBackendInteractionAgent(mockClient, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, nil, nil, 0, nil)
	state := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil), "endpoint", "cache-key", "")

	outcome, reason, _ := agent.evaluateRule(context.Background(), def, state)

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

	backendAgent := newBackendInteractionAgent(mockClient, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, nil, nil, 0, nil)
	state := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil), "endpoint", "cache-key", "")

	outcome, reason, _ := agent.evaluateRule(context.Background(), def, state)

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

	def := compileRuleWithAuth(t, []rulechain.AuthDirectiveSpec{{
		Match: []rulechain.AuthMatcherSpec{{Type: "bearer"}},
	}}, targetURL, []int{http.StatusOK})

	backendAgent := newBackendInteractionAgent(client, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, nil, nil, 0, nil)
	state := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil), "endpoint", "cache-key", "")
	state.Admission.Credentials = []pipeline.AdmissionCredential{{
		Type:   "bearer",
		Token:  "token-123",
		Source: "authorization",
	}}

	outcome, reason, _ := agent.evaluateRule(context.Background(), def, state)
	require.Equal(t, "pass", outcome)
	require.Equal(t, "rule evaluated without explicit outcome", reason)
	require.Equal(t, "bearer", state.Rule.Auth.Selected)
	bearerMap := state.Rule.Auth.Input["bearer"].(map[string]any)
	require.Equal(t, "token-123", bearerMap["token"])
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
			Match: []rulechain.AuthMatcherSpec{{
				Type: "header",
				Name: "X-Api-Token",
			}},
			ForwardAs: []rulechain.AuthForwardSpec{{
				Type:  "header",
				Name:  "Authorization",
				Value: `Bearer {{ index .auth.input.header "x-api-token" }}`,
			}},
		},
	}, targetURL, []int{http.StatusOK})

	backendAgent := newBackendInteractionAgent(client, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, templates.NewRenderer(nil), nil, 0, nil)
	state := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil), "endpoint", "cache-key", "")
	state.Admission.Credentials = []pipeline.AdmissionCredential{{
		Type:   "header",
		Name:   "X-Api-Token",
		Value:  "abc-123",
		Source: "header:X-Api-Token",
	}}

	outcome, _, _ := agent.evaluateRule(context.Background(), def, state)
	require.Equal(t, "pass", outcome)
	require.Equal(t, "header", state.Rule.Auth.Selected)
	headerMap := state.Rule.Auth.Input["header"].(map[string]string)
	require.Equal(t, "abc-123", headerMap["x-api-token"])
	require.Equal(t, "Authorization", state.Rule.Auth.Forward["name"])
}

func TestRuleExecutionAgentAuthFailsWhenNoMatch(t *testing.T) {
	const targetURL = "https://backend.test/auth"
	client := runtimemocks.NewMockHTTPDoer(t)

	def := compileRuleWithAuth(t, []rulechain.AuthDirectiveSpec{{
		Match: []rulechain.AuthMatcherSpec{{Type: "bearer"}},
	}}, targetURL, []int{http.StatusOK})

	backendAgent := newBackendInteractionAgent(client, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, nil, nil, 0, nil)
	state := pipeline.NewState(httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil), "endpoint", "cache-key", "")
	state.Admission.Credentials = []pipeline.AdmissionCredential{{
		Type:  "header",
		Name:  "X-Api-Token",
		Value: "abc-123",
	}}

	outcome, reason, _ := agent.evaluateRule(context.Background(), def, state)
	require.Equal(t, "fail", outcome)
	require.Contains(t, reason, "authentication")
	require.Empty(t, state.Rule.Auth.Selected)
}

func TestRuleExecutionAgentVariableScopes(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "vars-rule",
			Variables: rulechain.VariablesSpec{
				Global: map[string]rulechain.VariableSpec{
					"foo": {From: `"global"`},
				},
				Rule: map[string]rulechain.VariableSpec{
					"bar": {From: `vars.global.foo + "-rule"`},
				},
				Local: map[string]rulechain.VariableSpec{
					"baz": {From: `vars.rule.bar + "-local"`},
				},
			},
		},
	}, renderer)
	require.NoError(t, err)
	require.Len(t, defs, 1)
	def := defs[0]

	backendAgent := newBackendInteractionAgent(nil, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, nil, 0, nil)
	req := httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Admission.Authenticated = true
	state.Rule.ShouldExecute = true
	state.SetPlan(rulechain.ExecutionPlan{Rules: []rulechain.Definition{def}})

	result := agent.Execute(context.Background(), req, state)
	require.Equal(t, "pass", result.Status)
	require.Equal(t, "pass", state.Rule.Outcome)

	require.Equal(t, "global", state.Variables.Global["foo"])
	require.Equal(t, "global-rule", state.Rule.Variables.Rule["bar"])
	require.Equal(t, "global-rule-local", state.Rule.Variables.Local["baz"])
	require.Equal(t, "global-rule", state.Variables.Rules["vars-rule"]["bar"])
	require.Len(t, state.Rule.History, 1)
	require.Equal(t, "global-rule", state.Rule.History[0].Variables["bar"])

	ctx := state.TemplateContext()
	varsCtx, ok := ctx["variables"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "global", varsCtx["global"].(map[string]any)["foo"])
	require.Equal(t, "global-rule", varsCtx["rule"].(map[string]any)["bar"])
	require.Equal(t, "global-rule-local", varsCtx["local"].(map[string]any)["baz"])
	rulesCtx, ok := ctx["rules"].(map[string]any)
	require.True(t, ok)
	varsRule, ok := rulesCtx["vars-rule"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "global-rule", varsRule["variables"].(map[string]any)["bar"])

	chain, ok := ctx["chain"].([]pipeline.RuleHistoryEntry)
	require.True(t, ok)
	require.Len(t, chain, 1)
	require.Equal(t, "global-rule", chain[0].Variables["bar"])
}

func TestRuleExecutionAgentAppliesPassResponse(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "response-rule",
			Variables: rulechain.VariablesSpec{
				Global: map[string]rulechain.VariableSpec{
					"foo": {From: `"value"`},
				},
				Rule: map[string]rulechain.VariableSpec{
					"bar": {From: `vars.global.foo`},
				},
			},
			Responses: rulechain.ResponsesSpec{
				Pass: rulechain.ResponseSpec{
					Headers: forwardpolicy.CategoryConfig{
						Custom: map[string]string{
							"X-Custom": "{{ .variables.global.foo }}",
						},
					},
				},
			},
		},
	}, renderer)
	require.NoError(t, err)
	require.Len(t, defs, 1)
	def := defs[0]

	backendAgent := newBackendInteractionAgent(nil, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, nil, 0, nil)
	req := httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Admission.Authenticated = true
	state.Rule.ShouldExecute = true
	state.SetPlan(rulechain.ExecutionPlan{Rules: []rulechain.Definition{def}})
	state.Response.Headers["existing"] = "keep"

	result := agent.Execute(context.Background(), req, state)
	require.Equal(t, "pass", result.Status)
	require.Equal(t, "value", state.Response.Headers["X-Custom"])
	require.Equal(t, "keep", state.Response.Headers["existing"])
	require.Equal(t, "value", state.Rule.History[0].Variables["bar"])
}

func TestRuleExecutionAgentAppliesFailResponse(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "fail-response",
			Conditions: rulechain.ConditionSpec{
				Fail: []string{"true"},
			},
			Responses: rulechain.ResponsesSpec{
				Fail: rulechain.ResponseSpec{
					Headers: forwardpolicy.CategoryConfig{
						Custom: map[string]string{
							"X-Fail": "{{ .rule.Outcome }}",
						},
					},
				},
			},
		},
	}, renderer)
	require.NoError(t, err)
	require.Len(t, defs, 1)
	def := defs[0]

	backendAgent := newBackendInteractionAgent(nil, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, nil, 0, nil)
	req := httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Admission.Authenticated = true
	state.Rule.ShouldExecute = true
	state.SetPlan(rulechain.ExecutionPlan{Rules: []rulechain.Definition{def}})

	result := agent.Execute(context.Background(), req, state)
	require.Equal(t, "fail", result.Status)
	require.Equal(t, "fail", state.Rule.Outcome)
	require.Equal(t, "fail", state.Response.Headers["X-Fail"])
	require.Equal(t, "fail", state.Response.Headers["X-PassCtrl-Outcome"])
}

func TestRuleExecutionAgentAggregatesPassHeaders(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "rule-one",
			Responses: rulechain.ResponsesSpec{
				Pass: rulechain.ResponseSpec{
					Headers: forwardpolicy.CategoryConfig{
						Custom: map[string]string{"X-First": "one"},
					},
				},
			},
		},
		{
			Name: "rule-two",
			Responses: rulechain.ResponsesSpec{
				Pass: rulechain.ResponseSpec{
					Headers: forwardpolicy.CategoryConfig{
						Custom: map[string]string{"X-Second": "two"},
					},
				},
			},
		},
	}, renderer)
	require.NoError(t, err)
	require.Len(t, defs, 2)

	backendAgent := newBackendInteractionAgent(nil, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, nil, 0, nil)
	req := httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Admission.Authenticated = true
	state.Rule.ShouldExecute = true
	state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

	result := agent.Execute(context.Background(), req, state)
	require.Equal(t, "pass", result.Status)
	require.Equal(t, "one", state.Response.Headers["X-First"])
	require.Equal(t, "two", state.Response.Headers["X-Second"])
	require.Equal(t, "pass", state.Response.Headers["X-PassCtrl-Outcome"])
}

func TestRuleExecutionAgentAppliesErrorResponse(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "error-response",
			Conditions: rulechain.ConditionSpec{
				Error: []string{"true"},
			},
			Responses: rulechain.ResponsesSpec{
				Error: rulechain.ResponseSpec{
					Headers: forwardpolicy.CategoryConfig{
						Custom: map[string]string{
							"X-Error": "rule-{{ .rule.Outcome }}",
						},
					},
				},
			},
		},
	}, renderer)
	require.NoError(t, err)
	require.Len(t, defs, 1)
	def := defs[0]

	backendAgent := newBackendInteractionAgent(nil, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, nil, 0, nil)
	req := httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Admission.Authenticated = true
	state.Rule.ShouldExecute = true
	state.SetPlan(rulechain.ExecutionPlan{Rules: []rulechain.Definition{def}})

	result := agent.Execute(context.Background(), req, state)
	require.Equal(t, "error", result.Status)
	require.Equal(t, "error", state.Rule.Outcome)
	require.Equal(t, "rule-error", state.Response.Headers["X-Error"])
	require.Equal(t, "error", state.Response.Headers["X-PassCtrl-Outcome"])
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

func TestRuleExecutionAgentExportedVariables(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	mockClient := runtimemocks.NewMockHTTPDoer(t)
	mockClient.EXPECT().
		Do(mock.AnythingOfType("*http.Request")).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			headers := map[string]string{"Content-Type": "application/json"}
			return newBackendResponse(200, `{"userId":"123","email":"TEST@EXAMPLE.COM","tier":"premium"}`, headers), nil
		})
	backendAgent := newBackendInteractionAgent(mockClient, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, nil, 0, nil)

	def, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "test-rule",
			Backend: rulechain.BackendDefinitionSpec{
				URL:      "http://backend/validate",
				Method:   "GET",
				Accepted: []int{200},
			},
			Variables: rulechain.VariablesSpec{
				LocalV2: map[string]string{
					"raw_id":    "backend.body.userId",
					"raw_email": "backend.body.email",
				},
			},
			Responses: rulechain.ResponsesSpec{
				Pass: rulechain.ResponseSpec{
					Variables: map[string]string{
						"user_id": "variables.raw_id",
						"email":   "{{ .variables.raw_email | lower }}",
						"tier":    "backend.body.tier",
					},
				},
			},
			Conditions: rulechain.ConditionSpec{
				Pass: []string{"backend.status == 200"},
			},
		},
	}, renderer)
	require.NoError(t, err)
	require.Len(t, def, 1)

	req := httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil)
	state := pipeline.NewState(req, "test", "cache-key", "corr-123")
	state.Admission.Authenticated = true
	state.Rule.ShouldExecute = true
	state.SetPlan(rulechain.ExecutionPlan{Rules: def})

	ctx := context.Background()
	result := agent.Execute(ctx, req, state)

	require.Equal(t, "pass", result.Status, "Outcome: %s, Reason: %s", state.Rule.Outcome, state.Rule.Reason)
	require.Equal(t, "pass", state.Rule.Outcome)

	// Check exported variables were evaluated
	require.NotNil(t, state.Rule.Variables.Exported)
	require.Equal(t, "123", state.Rule.Variables.Exported["user_id"])
	require.Equal(t, "test@example.com", state.Rule.Variables.Exported["email"]) // Lowercased via template
	require.Equal(t, "premium", state.Rule.Variables.Exported["tier"])

	// Check exported variables are available in state.Variables.Rules
	require.NotNil(t, state.Variables.Rules)
	require.NotNil(t, state.Variables.Rules["test-rule"])
	require.Equal(t, "123", state.Variables.Rules["test-rule"]["user_id"])
	require.Equal(t, "test@example.com", state.Variables.Rules["test-rule"]["email"])
	require.Equal(t, "premium", state.Variables.Rules["test-rule"]["tier"])
}

func TestRuleExecutionAgentExportedVariablesOnFail(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	mockClient := runtimemocks.NewMockHTTPDoer(t)
	mockClient.EXPECT().
		Do(mock.AnythingOfType("*http.Request")).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			headers := map[string]string{"Content-Type": "application/json"}
			return newBackendResponse(403, `{"error":"forbidden"}`, headers), nil
		})
	backendAgent := newBackendInteractionAgent(mockClient, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, nil, 0, nil)

	def, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "test-rule",
			Backend: rulechain.BackendDefinitionSpec{
				URL:      "http://backend/validate",
				Method:   "GET",
				Accepted: []int{200},
			},
			Responses: rulechain.ResponsesSpec{
				Pass: rulechain.ResponseSpec{
					Variables: map[string]string{
						"status": "\"success\"",
					},
				},
				Fail: rulechain.ResponseSpec{
					Variables: map[string]string{
						"error_code": "backend.status",
						"error_msg":  "{{ .backend.body.error | upper }}",
					},
				},
			},
			Conditions: rulechain.ConditionSpec{
				Pass: []string{"backend.status == 200"},
			},
		},
	}, renderer)
	require.NoError(t, err)
	require.Len(t, def, 1)

	req := httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil)
	state := pipeline.NewState(req, "test", "cache-key", "corr-123")
	state.Admission.Authenticated = true
	state.Rule.ShouldExecute = true
	state.SetPlan(rulechain.ExecutionPlan{Rules: def})

	ctx := context.Background()
	result := agent.Execute(ctx, req, state)

	require.Equal(t, "fail", result.Status)
	require.Equal(t, "fail", state.Rule.Outcome)

	// Only fail outcome variables should be exported (not pass)
	require.NotNil(t, state.Rule.Variables.Exported)
	require.Equal(t, int64(403), state.Rule.Variables.Exported["error_code"])
	require.Equal(t, "FORBIDDEN", state.Rule.Variables.Exported["error_msg"])
	require.NotContains(t, state.Rule.Variables.Exported, "status") // Pass variable not exported

	// Check in state.Variables.Rules
	require.Equal(t, int64(403), state.Variables.Rules["test-rule"]["error_code"])
	require.Equal(t, "FORBIDDEN", state.Variables.Rules["test-rule"]["error_msg"])
}
