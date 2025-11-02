package responsepolicy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/l0p7/passctrl/internal/runtime/forwardpolicy"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/templates"
	"github.com/stretchr/testify/require"
)

func TestAgentExecute(t *testing.T) {
	tests := []struct {
		name           string
		setup          func() *pipeline.State
		wantResult     string
		wantStatusCode int
		wantMessage    string
		wantOutcome    string
	}{
		{
			name: "replays cached response",
			setup: func() *pipeline.State {
				state := &pipeline.State{}
				state.Response.Status = http.StatusAccepted
				state.Response.Message = "ready"
				return state
			},
			wantResult:     "cached",
			wantStatusCode: http.StatusAccepted,
			wantMessage:    "ready",
			wantOutcome:    "",
		},
		{
			name: "renders pass outcome",
			setup: func() *pipeline.State {
				state := &pipeline.State{}
				state.Rule.Outcome = "pass"
				return state
			},
			wantResult:     "rendered",
			wantStatusCode: http.StatusOK,
			wantOutcome:    "pass",
		},
		{
			name: "renders fail outcome with reason",
			setup: func() *pipeline.State {
				state := &pipeline.State{}
				state.Rule.Outcome = "fail"
				state.Rule.Reason = "policy rejected"
				return state
			},
			wantResult:     "rendered",
			wantStatusCode: http.StatusForbidden,
			wantOutcome:    "fail",
		},
		{
			name: "renders fail outcome with header init",
			setup: func() *pipeline.State {
				state := &pipeline.State{}
				state.Rule.Outcome = "fail"
				state.Response.Headers = nil
				return state
			},
			wantResult:     "rendered",
			wantStatusCode: http.StatusForbidden,
			wantOutcome:    "fail",
		},
		{
			name: "renders error outcome",
			setup: func() *pipeline.State {
				state := &pipeline.State{}
				state.Rule.Outcome = "error"
				return state
			},
			wantResult:     "rendered",
			wantStatusCode: http.StatusBadGateway,
			wantOutcome:    "error",
		},
		{
			name: "renders unknown outcome",
			setup: func() *pipeline.State {
				return &pipeline.State{}
			},
			wantResult:     "rendered",
			wantStatusCode: http.StatusInternalServerError,
			wantOutcome:    "",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			state := tc.setup()
			agent := New()

			res := agent.Execute(context.Background(), nil, state)

			require.Equal(t, tc.wantResult, res.Status)
			require.Equal(t, tc.wantStatusCode, state.Response.Status)
			require.Equal(t, tc.wantMessage, state.Response.Message)
			if tc.wantResult == "cached" {
				require.Empty(t, state.Response.Headers)
			} else {
				require.NotNil(t, state.Response.Headers)
				require.Equal(t, tc.wantOutcome, state.Response.Headers["X-PassCtrl-Outcome"])
			}
		})
	}
}

func TestAgentExecuteWithConfigAppliesOverrides(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/demo", http.NoBody)
	state := pipeline.NewState(req, "endpoint-a", "cache-key", "corr-id")
	state.Rule.Outcome = "pass"
	state.Response.Headers["keep"] = "value"
	state.Response.Headers["stripme"] = "remove"
	state.Response.Headers["other"] = "should-drop"

	renderer := templates.NewRenderer(nil)
	cfg := Config{
		Endpoint: "endpoint-a",
		Renderer: renderer,
		Pass: CategoryConfig{
			Status: http.StatusAccepted,
			Body:   "hello {{ .endpoint }}",
			Headers: forwardpolicy.CategoryConfig{
				Allow: []string{"keep"},
				Strip: []string{"stripme"},
				Custom: map[string]string{
					"X-Rendered": "outcome {{ .rule.Outcome }}",
					"X-Static":   " static ",
				},
			},
		},
	}

	agent := NewWithConfig(cfg)
	res := agent.Execute(context.Background(), nil, state)

	require.Equal(t, "rendered", res.Status)
	require.Equal(t, http.StatusAccepted, state.Response.Status)
	require.Equal(t, "hello endpoint-a", state.Response.Message)
	require.NotContains(t, state.Response.Headers, "stripme")
	require.NotContains(t, state.Response.Headers, "other")
	require.Equal(t, "value", state.Response.Headers["keep"])
	require.Equal(t, "outcome pass", state.Response.Headers["X-Rendered"])
	require.Equal(t, "static", state.Response.Headers["X-Static"])
	require.Equal(t, "pass", state.Response.Headers["X-PassCtrl-Outcome"])
}

func TestAgentExecuteWithResponseVariablesInTemplates(t *testing.T) {
	tests := []struct {
		name              string
		outcome           string
		responseVariables map[string]any
		bodyTemplate      string
		headerTemplates   map[string]string
		wantStatus        int
		wantBody          string
		wantHeaders       map[string]string
	}{
		{
			name:    "pass outcome with exported variables in body",
			outcome: "pass",
			responseVariables: map[string]any{
				"user_id":   "123",
				"user_name": "alice",
				"tier":      "premium",
			},
			bodyTemplate: `{"user":"{{ .response.user_name }}","id":"{{ .response.user_id }}","tier":"{{ .response.tier }}"}`,
			wantStatus:   http.StatusOK,
			wantBody:     `{"user":"alice","id":"123","tier":"premium"}`,
		},
		{
			name:    "pass outcome with exported variables in headers",
			outcome: "pass",
			responseVariables: map[string]any{
				"user_id": "456",
				"plan":    "enterprise",
			},
			headerTemplates: map[string]string{
				"X-User-ID": "{{ .response.user_id }}",
				"X-Plan":    "{{ .response.plan }}",
			},
			wantStatus: http.StatusOK,
			wantHeaders: map[string]string{
				"X-User-ID": "456",
				"X-Plan":    "enterprise",
			},
		},
		{
			name:    "fail outcome with exported error variables",
			outcome: "fail",
			responseVariables: map[string]any{
				"error_code": int64(403),
				"error_msg":  "insufficient permissions",
			},
			bodyTemplate: `{"error":"{{ .response.error_msg }}","code":{{ .response.error_code }}}`,
			headerTemplates: map[string]string{
				"X-Error-Code": "{{ .response.error_code }}",
			},
			wantStatus: http.StatusForbidden,
			wantBody:   `{"error":"insufficient permissions","code":403}`,
			wantHeaders: map[string]string{
				"X-Error-Code": "403",
			},
		},
		{
			name:    "error outcome with diagnostic variables",
			outcome: "error",
			responseVariables: map[string]any{
				"backend_error": "connection timeout",
				"retry_after":   "30s",
			},
			bodyTemplate: `{"error":"{{ .response.backend_error }}","retry":"{{ .response.retry_after }}"}`,
			wantStatus:   http.StatusBadGateway,
			wantBody:     `{"error":"connection timeout","retry":"30s"}`,
		},
		{
			name:    "variables with template filters",
			outcome: "pass",
			responseVariables: map[string]any{
				"email":        "USER@EXAMPLE.COM",
				"display_name": "Alice Smith",
			},
			bodyTemplate: `{"email":"{{ .response.email | lower }}","name":"{{ .response.display_name | upper }}"}`,
			wantStatus:   http.StatusOK,
			wantBody:     `{"email":"user@example.com","name":"ALICE SMITH"}`,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://example.com/test", http.NoBody)
			state := pipeline.NewState(req, "test-endpoint", "cache-key", "corr-id")
			state.Rule.Outcome = tc.outcome
			state.Response.Variables = tc.responseVariables

			renderer := templates.NewRenderer(nil)
			cfg := Config{
				Endpoint: "test-endpoint",
				Renderer: renderer,
			}

			// Configure response based on outcome
			switch tc.outcome {
			case "pass":
				cfg.Pass = CategoryConfig{
					Status: tc.wantStatus,
					Body:   tc.bodyTemplate,
					Headers: forwardpolicy.CategoryConfig{
						Custom: tc.headerTemplates,
					},
				}
			case "fail":
				cfg.Fail = CategoryConfig{
					Status: tc.wantStatus,
					Body:   tc.bodyTemplate,
					Headers: forwardpolicy.CategoryConfig{
						Custom: tc.headerTemplates,
					},
				}
			case "error":
				cfg.Error = CategoryConfig{
					Status: tc.wantStatus,
					Body:   tc.bodyTemplate,
					Headers: forwardpolicy.CategoryConfig{
						Custom: tc.headerTemplates,
					},
				}
			}

			agent := NewWithConfig(cfg)
			res := agent.Execute(context.Background(), nil, state)

			require.Equal(t, "rendered", res.Status)
			require.Equal(t, tc.wantStatus, state.Response.Status)

			if tc.wantBody != "" {
				require.Equal(t, tc.wantBody, state.Response.Message)
			}

			for key, expectedValue := range tc.wantHeaders {
				require.Equal(t, expectedValue, state.Response.Headers[key], "header %s mismatch", key)
			}
		})
	}
}

func TestAgentExecuteWithMultiRuleVariableAccumulation(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/test", http.NoBody)
	state := pipeline.NewState(req, "multi-rule-endpoint", "cache-key", "corr-id")
	state.Rule.Outcome = "pass"

	// Simulate variables accumulated from multiple rules in the chain
	state.Response.Variables = map[string]any{
		"user_id": "123",     // From first rule (lookup-user)
		"tier":    "premium", // From first rule (lookup-user)
		"allowed": true,      // From second rule (check-entitlement)
		"region":  "us-west", // From third rule (check-region)
	}

	renderer := templates.NewRenderer(nil)
	cfg := Config{
		Endpoint: "multi-rule-endpoint",
		Renderer: renderer,
		Pass: CategoryConfig{
			Status: http.StatusOK,
			Body:   `{"user":"{{ .response.user_id }}","tier":"{{ .response.tier }}","allowed":{{ .response.allowed }},"region":"{{ .response.region }}"}`,
			Headers: forwardpolicy.CategoryConfig{
				Custom: map[string]string{
					"X-User-ID":     "{{ .response.user_id }}",
					"X-User-Tier":   "{{ .response.tier }}",
					"X-User-Region": "{{ .response.region }}",
				},
			},
		},
	}

	agent := NewWithConfig(cfg)
	res := agent.Execute(context.Background(), nil, state)

	require.Equal(t, "rendered", res.Status)
	require.Equal(t, http.StatusOK, state.Response.Status)
	require.JSONEq(t, `{"user":"123","tier":"premium","allowed":true,"region":"us-west"}`, state.Response.Message)
	require.Equal(t, "123", state.Response.Headers["X-User-ID"])
	require.Equal(t, "premium", state.Response.Headers["X-User-Tier"])
	require.Equal(t, "us-west", state.Response.Headers["X-User-Region"])
}

func TestAgentExecuteWithEmptyResponseVariables(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/test", http.NoBody)
	state := pipeline.NewState(req, "test-endpoint", "cache-key", "corr-id")
	state.Rule.Outcome = "pass"
	// Empty response variables (no variables exported by rules)
	state.Response.Variables = map[string]any{}

	renderer := templates.NewRenderer(nil)
	cfg := Config{
		Endpoint: "test-endpoint",
		Renderer: renderer,
		Pass: CategoryConfig{
			Status: http.StatusOK,
			Body:   `{"status":"authenticated"}`,
			Headers: forwardpolicy.CategoryConfig{
				Custom: map[string]string{
					"X-Auth-Status": "success",
				},
			},
		},
	}

	agent := NewWithConfig(cfg)
	res := agent.Execute(context.Background(), nil, state)

	require.Equal(t, "rendered", res.Status)
	require.Equal(t, http.StatusOK, state.Response.Status)
	require.JSONEq(t, `{"status":"authenticated"}`, state.Response.Message)
	require.Equal(t, "success", state.Response.Headers["X-Auth-Status"])
}

func TestCloneHeaders(t *testing.T) {
	tests := []struct {
		name      string
		input     map[string]string
		mutate    func(map[string]string)
		wantNil   bool
		wantValue map[string]string
	}{
		{
			name:    "nil map returns nil",
			input:   nil,
			wantNil: true,
		},
		{
			name:  "clones map values",
			input: map[string]string{"a": "1", "b": "2"},
			mutate: func(m map[string]string) {
				m["a"] = "updated"
			},
			wantValue: map[string]string{"a": "1", "b": "2"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			clone := cloneHeaders(tc.input)
			if tc.wantNil {
				require.Nil(t, clone)
				return
			}

			require.NotNil(t, clone)
			require.Equal(t, tc.wantValue, clone)
			if tc.mutate != nil {
				tc.mutate(clone)
			}
			if tc.input != nil {
				for key, value := range tc.wantValue {
					require.Equal(t, value, tc.input[key])
				}
			}
		})
	}
}
