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

	backendAgent := newBackendInteractionAgent(mockClient, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, nil, nil, 0, nil, "")
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
	agent := newRuleExecutionAgent(backendAgent, nil, nil, nil, 0, nil, "")
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
	agent := newRuleExecutionAgent(backendAgent, nil, nil, nil, 0, nil, "")
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
	agent := newRuleExecutionAgent(backendAgent, nil, templates.NewRenderer(nil), nil, 0, nil, "")
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
	agent := newRuleExecutionAgent(backendAgent, nil, nil, nil, 0, nil, "")
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

func TestRuleExecutionAgentLocalVariables(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "vars-rule",
			Variables: rulechain.VariablesSpec{
				Variables: map[string]string{
					"user_id":      `backend.body.userId`,
					"display_name": `backend.body.displayName`,
					"cache_key":    `user:{{ .backend.body.userId }}`,
				},
			},
			Backend: rulechain.BackendDefinitionSpec{
				URL:      "http://backend/api",
				Accepted: []int{200},
			},
		},
	}, renderer)
	require.NoError(t, err)
	require.Len(t, defs, 1)
	def := defs[0]

	// Mock backend response
	mockClient := runtimemocks.NewMockHTTPDoer(t)
	mockClient.EXPECT().
		Do(mock.AnythingOfType("*http.Request")).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			return newBackendResponse(http.StatusOK, `{"userId":"123","displayName":"Alice"}`, map[string]string{"Content-Type": "application/json"}), nil
		})

	backendAgent := newBackendInteractionAgent(mockClient, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, nil, 0, nil, "")
	req := httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Admission.Authenticated = true
	state.Rule.ShouldExecute = true
	state.SetPlan(rulechain.ExecutionPlan{Rules: []rulechain.Definition{def}})

	result := agent.Execute(context.Background(), req, state)
	require.Equal(t, "pass", result.Status)
	require.Equal(t, "pass", state.Rule.Outcome)

	// Check local variables were evaluated
	require.Equal(t, "123", state.Rule.Variables.Local["user_id"])
	require.Equal(t, "Alice", state.Rule.Variables.Local["display_name"])
	require.Equal(t, "user:123", state.Rule.Variables.Local["cache_key"])
}

func TestRuleExecutionAgentAppliesPassResponse(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "response-rule",
			Variables: rulechain.VariablesSpec{
				Variables: map[string]string{
					"foo": `"value"`,
				},
			},
			Responses: rulechain.ResponsesSpec{
				Pass: rulechain.ResponseSpec{
					Variables: map[string]string{
						"custom_value": "{{ .variables.foo }}",
					},
				},
			},
		},
	}, renderer)
	require.NoError(t, err)
	require.Len(t, defs, 1)
	def := defs[0]

	backendAgent := newBackendInteractionAgent(nil, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, nil, 0, nil, "")
	req := httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Admission.Authenticated = true
	state.Rule.ShouldExecute = true
	state.SetPlan(rulechain.ExecutionPlan{Rules: []rulechain.Definition{def}})
	state.Response.Headers["existing"] = "keep"

	result := agent.Execute(context.Background(), req, state)
	require.Equal(t, "pass", result.Status)
	require.Equal(t, "value", state.Response.Variables["custom_value"])
	require.Equal(t, "keep", state.Response.Headers["existing"])
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
					Variables: map[string]string{
						"fail_reason": "{{ .rule.Outcome }}",
					},
				},
			},
		},
	}, renderer)
	require.NoError(t, err)
	require.Len(t, defs, 1)
	def := defs[0]

	backendAgent := newBackendInteractionAgent(nil, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, nil, 0, nil, "")
	req := httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Admission.Authenticated = true
	state.Rule.ShouldExecute = true
	state.SetPlan(rulechain.ExecutionPlan{Rules: []rulechain.Definition{def}})

	result := agent.Execute(context.Background(), req, state)
	require.Equal(t, "fail", result.Status)
	require.Equal(t, "fail", state.Rule.Outcome)
	require.Equal(t, "fail", state.Response.Variables["fail_reason"])
}

func TestRuleExecutionAgentAggregatesPassVariables(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "rule-one",
			Responses: rulechain.ResponsesSpec{
				Pass: rulechain.ResponseSpec{
					Variables: map[string]string{"first": "\"one\""},
				},
			},
		},
		{
			Name: "rule-two",
			Responses: rulechain.ResponsesSpec{
				Pass: rulechain.ResponseSpec{
					Variables: map[string]string{"second": "\"two\""},
				},
			},
		},
	}, renderer)
	require.NoError(t, err)
	require.Len(t, defs, 2)

	backendAgent := newBackendInteractionAgent(nil, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, nil, 0, nil, "")
	req := httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Admission.Authenticated = true
	state.Rule.ShouldExecute = true
	state.SetPlan(rulechain.ExecutionPlan{Rules: defs})

	result := agent.Execute(context.Background(), req, state)
	require.Equal(t, "pass", result.Status)
	require.Equal(t, "one", state.Response.Variables["first"])
	require.Equal(t, "two", state.Response.Variables["second"])
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
					Variables: map[string]string{
						"error_info": "rule-{{ .rule.Outcome }}",
					},
				},
			},
		},
	}, renderer)
	require.NoError(t, err)
	require.Len(t, defs, 1)
	def := defs[0]

	backendAgent := newBackendInteractionAgent(nil, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, nil, 0, nil, "")
	req := httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil)
	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Admission.Authenticated = true
	state.Rule.ShouldExecute = true
	state.SetPlan(rulechain.ExecutionPlan{Rules: []rulechain.Definition{def}})

	result := agent.Execute(context.Background(), req, state)
	require.Equal(t, "error", result.Status)
	require.Equal(t, "error", state.Rule.Outcome)
	require.Equal(t, "rule-error", state.Response.Variables["error_info"])
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
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, nil, 0, nil, "")

	def, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{
		{
			Name: "test-rule",
			Backend: rulechain.BackendDefinitionSpec{
				URL:      "http://backend/validate",
				Method:   "GET",
				Accepted: []int{200},
			},
			Variables: rulechain.VariablesSpec{
				Variables: map[string]string{
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
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, nil, 0, nil, "")

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

// TestCredentialStripping verifies that custom headers used as credentials
// are stripped from backend requests when using null-copy semantics.
func TestCredentialStripping_CustomHeaderStripped(t *testing.T) {
	const targetURL = "https://backend.test/api"
	client := runtimemocks.NewMockHTTPDoer(t)
	client.EXPECT().
		Do(mock.AnythingOfType("*http.Request")).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			// X-Api-Token should be stripped (not present)
			require.Empty(t, req.Header.Get("X-Api-Token"), "credential header should be stripped")
			// Authorization should be present (forwarded)
			require.Equal(t, "Bearer secret-token", req.Header.Get("Authorization"))
			// Other headers should pass through
			require.Equal(t, "value", req.Header.Get("X-Custom"))
			return newBackendResponse(http.StatusOK, `{}`, map[string]string{}), nil
		})

	renderer := templates.NewRenderer(nil)
	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
		Name: "credential-strip-test",
		Auth: []rulechain.AuthDirectiveSpec{{
			Match: []rulechain.AuthMatcherSpec{{
				Type: "header",
				Name: "X-Api-Token",
			}},
			ForwardAs: []rulechain.AuthForwardSpec{{
				Type:  "bearer",
				Token: "{{ index .auth.input.header \"x-api-token\" }}",
			}},
		}},
		Backend: rulechain.BackendDefinitionSpec{
			URL:      targetURL,
			Accepted: []int{http.StatusOK},
			Headers: map[string]*string{
				"x-api-token": nil, // null-copy: should be stripped
				"x-custom":    stringPtr("value"),
			},
		},
	}}, renderer)
	require.NoError(t, err)
	require.Len(t, defs, 1)

	backendAgent := newBackendInteractionAgent(client, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, nil, 0, nil, "")
	req := httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil)
	req.Header.Set("X-Api-Token", "secret-token")
	req.Header.Set("X-Custom", "value")

	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Admission.Credentials = []pipeline.AdmissionCredential{{
		Type:  "header",
		Name:  "X-Api-Token",
		Value: "secret-token",
	}}

	outcome, _, _ := agent.evaluateRule(context.Background(), defs[0], state)
	require.Equal(t, "pass", outcome)
}

// TestCredentialStripping_QueryParamStripped verifies that query parameters
// used as credentials are stripped from backend requests.
func TestCredentialStripping_QueryParamStripped(t *testing.T) {
	const targetURL = "https://backend.test/api"
	client := runtimemocks.NewMockHTTPDoer(t)
	client.EXPECT().
		Do(mock.AnythingOfType("*http.Request")).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			// api_key query param should be stripped
			require.Empty(t, req.URL.Query().Get("api_key"), "credential query param should be stripped")
			// Authorization header should be present (forwarded)
			require.Equal(t, "Bearer token-from-query", req.Header.Get("Authorization"))
			// Other query params should pass through
			require.Equal(t, "100", req.URL.Query().Get("limit"))
			return newBackendResponse(http.StatusOK, `{}`, map[string]string{}), nil
		})

	renderer := templates.NewRenderer(nil)
	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
		Name: "query-strip-test",
		Auth: []rulechain.AuthDirectiveSpec{{
			Match: []rulechain.AuthMatcherSpec{{
				Type: "query",
				Name: "api_key",
			}},
			ForwardAs: []rulechain.AuthForwardSpec{{
				Type:  "bearer",
				Token: "{{ index .auth.input.query \"api_key\" }}",
			}},
		}},
		Backend: rulechain.BackendDefinitionSpec{
			URL:      targetURL,
			Accepted: []int{http.StatusOK},
			Query: map[string]*string{
				"api_key": nil, // null-copy: should be stripped
				"limit":   stringPtr("100"),
			},
		},
	}}, renderer)
	require.NoError(t, err)
	require.Len(t, defs, 1)

	backendAgent := newBackendInteractionAgent(client, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, nil, 0, nil, "")
	req := httptest.NewRequest(http.MethodGet, "http://unit.test/request?api_key=token-from-query&limit=100", nil)

	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Admission.Credentials = []pipeline.AdmissionCredential{{
		Type:  "query",
		Name:  "api_key",
		Value: "token-from-query",
	}}
	state.Request.Query = map[string]string{
		"api_key": "token-from-query",
		"limit":   "100",
	}

	outcome, _, _ := agent.evaluateRule(context.Background(), defs[0], state)
	require.Equal(t, "pass", outcome)
}

// TestCredentialStripping_MultipleMatchGroups verifies that credentials from
// ALL auth match groups are stripped, not just the matched one.
func TestCredentialStripping_MultipleMatchGroups(t *testing.T) {
	const targetURL = "https://backend.test/api"
	client := runtimemocks.NewMockHTTPDoer(t)
	client.EXPECT().
		Do(mock.AnythingOfType("*http.Request")).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			// Both X-Api-Key and X-Token should be stripped (from different match groups)
			require.Empty(t, req.Header.Get("X-Api-Key"), "credential header from non-matched group should be stripped")
			require.Empty(t, req.Header.Get("X-Token"), "credential header from matched group should be stripped")
			// Authorization should be present (forwarded from matched group)
			require.Equal(t, "Bearer matched-token", req.Header.Get("Authorization"))
			return newBackendResponse(http.StatusOK, `{}`, map[string]string{}), nil
		})

	renderer := templates.NewRenderer(nil)
	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
		Name: "multi-group-test",
		Auth: []rulechain.AuthDirectiveSpec{
			{
				// First group - won't match (no X-Api-Key header)
				Match: []rulechain.AuthMatcherSpec{{
					Type: "header",
					Name: "X-Api-Key",
				}},
			},
			{
				// Second group - will match
				Match: []rulechain.AuthMatcherSpec{{
					Type: "header",
					Name: "X-Token",
				}},
				ForwardAs: []rulechain.AuthForwardSpec{{
					Type:  "bearer",
					Token: "{{ index .auth.input.header \"x-token\" }}",
				}},
			},
		},
		Backend: rulechain.BackendDefinitionSpec{
			URL:      targetURL,
			Accepted: []int{http.StatusOK},
			Headers: map[string]*string{
				"x-api-key": nil, // null-copy: should be stripped (from non-matched group)
				"x-token":   nil, // null-copy: should be stripped (from matched group)
			},
		},
	}}, renderer)
	require.NoError(t, err)
	require.Len(t, defs, 1)

	backendAgent := newBackendInteractionAgent(client, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, nil, 0, nil, "")
	req := httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil)
	req.Header.Set("X-Token", "matched-token")

	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Request.Headers = map[string]string{
		"x-token": "matched-token",
	}
	state.Admission.Credentials = []pipeline.AdmissionCredential{{
		Type:  "header",
		Name:  "X-Token",
		Value: "matched-token",
	}}

	outcome, _, _ := agent.evaluateRule(context.Background(), defs[0], state)
	require.Equal(t, "pass", outcome)
}

// TestCredentialStripping_PassThroughMode verifies that even in pass-through mode
// (no forwardAs), credentials are still stripped from null-copy headers.
func TestCredentialStripping_PassThroughMode(t *testing.T) {
	const targetURL = "https://backend.test/api"
	client := runtimemocks.NewMockHTTPDoer(t)
	client.EXPECT().
		Do(mock.AnythingOfType("*http.Request")).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			// X-Secret should be stripped (used as credential)
			require.Empty(t, req.Header.Get("X-Secret"), "credential header should be stripped even in pass-through mode")
			// Authorization should be present (pass-through forward)
			require.NotEmpty(t, req.Header.Get("Authorization"))
			return newBackendResponse(http.StatusOK, `{}`, map[string]string{}), nil
		})

	renderer := templates.NewRenderer(nil)
	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
		Name: "pass-through-test",
		Auth: []rulechain.AuthDirectiveSpec{
			{
				// Match on custom header
				Match: []rulechain.AuthMatcherSpec{{
					Type: "header",
					Name: "X-Secret",
				}},
				// No forwardAs - triggers pass-through mode
			},
			{
				// Also match bearer for pass-through
				Match: []rulechain.AuthMatcherSpec{{
					Type: "bearer",
				}},
			},
		},
		Backend: rulechain.BackendDefinitionSpec{
			URL:      targetURL,
			Accepted: []int{http.StatusOK},
			Headers: map[string]*string{
				"x-secret": nil, // null-copy: should be stripped
			},
		},
	}}, renderer)
	require.NoError(t, err)
	require.Len(t, defs, 1)

	backendAgent := newBackendInteractionAgent(client, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, nil, 0, nil, "")
	req := httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil)
	req.Header.Set("X-Secret", "secret-value")
	req.Header.Set("Authorization", "Bearer token")

	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Request.Headers = map[string]string{
		"x-secret":      "secret-value",
		"authorization": "Bearer token",
	}
	state.Admission.Credentials = []pipeline.AdmissionCredential{
		{
			Type:  "header",
			Name:  "X-Secret",
			Value: "secret-value",
		},
		{
			Type:  "bearer",
			Token: "token",
		},
	}

	outcome, _, _ := agent.evaluateRule(context.Background(), defs[0], state)
	require.Equal(t, "pass", outcome)
}

// TestCredentialStripping_NoAuthDirectives verifies that when there are no auth
// directives, headers pass through normally (no stripping).
func TestCredentialStripping_NoAuthDirectives(t *testing.T) {
	const targetURL = "https://backend.test/api"
	client := runtimemocks.NewMockHTTPDoer(t)
	client.EXPECT().
		Do(mock.AnythingOfType("*http.Request")).
		RunAndReturn(func(req *http.Request) (*http.Response, error) {
			// X-Api-Token should NOT be stripped (no auth directives)
			require.Equal(t, "token-value", req.Header.Get("X-Api-Token"))
			return newBackendResponse(http.StatusOK, `{}`, map[string]string{}), nil
		})

	renderer := templates.NewRenderer(nil)
	defs, err := rulechain.CompileDefinitions([]rulechain.DefinitionSpec{{
		Name: "no-auth-test",
		// No auth directives
		Backend: rulechain.BackendDefinitionSpec{
			URL:      targetURL,
			Accepted: []int{http.StatusOK},
			Headers: map[string]*string{
				"x-api-token": nil, // null-copy: should pass through
			},
		},
	}}, renderer)
	require.NoError(t, err)
	require.Len(t, defs, 1)

	backendAgent := newBackendInteractionAgent(client, nil)
	agent := newRuleExecutionAgent(backendAgent, nil, renderer, nil, 0, nil, "")
	req := httptest.NewRequest(http.MethodGet, "http://unit.test/request", nil)
	req.Header.Set("X-Api-Token", "token-value")

	state := pipeline.NewState(req, "endpoint", "cache-key", "")
	state.Request.Headers = map[string]string{
		"x-api-token": "token-value",
	}

	outcome, _, _ := agent.evaluateRule(context.Background(), defs[0], state)
	require.Equal(t, "pass", outcome)
}

// Helper function for string pointers
func stringPtr(s string) *string {
	return &s
}
