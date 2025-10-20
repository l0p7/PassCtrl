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
