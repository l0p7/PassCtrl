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

func TestAgentExecuteCachedResponse(t *testing.T) {
	state := &pipeline.State{}
	state.Response.Status = http.StatusAccepted
	state.Response.Message = "ready"

	agent := New()
	res := agent.Execute(context.Background(), nil, state)

	require.Equal(t, "cached", res.Status)
	require.Equal(t, "ready", state.Response.Message)
}

func TestAgentExecuteOutcomeMapping(t *testing.T) {
	tests := map[string]struct {
		outcome       string
		reason        string
		wantStatus    int
		expectedCache bool
	}{
		"pass": {
			outcome:    "pass",
			wantStatus: http.StatusOK,
		},
		"fail with reason": {
			outcome:    "fail",
			reason:     "policy rejected",
			wantStatus: http.StatusForbidden,
		},
		"fail default": {
			outcome:    "fail",
			wantStatus: http.StatusForbidden,
		},
		"error default": {
			outcome:    "error",
			wantStatus: http.StatusBadGateway,
		},
		"unknown": {
			outcome:    "",
			wantStatus: http.StatusInternalServerError,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			state := &pipeline.State{}
			state.Rule.Outcome = tc.outcome
			state.Rule.Reason = tc.reason

			agent := New()
			res := agent.Execute(context.Background(), nil, state)

			require.Equal(t, "rendered", res.Status)
			require.Equal(t, tc.wantStatus, state.Response.Status)
			require.Empty(t, state.Response.Message)
			require.Equal(t, tc.outcome, state.Response.Headers["X-PassCtrl-Outcome"])
		})
	}
}

func TestAgentExecuteInitializesHeaders(t *testing.T) {
	state := &pipeline.State{}
	state.Rule.Outcome = "fail"
	state.Response.Headers = nil

	agent := New()
	_ = agent.Execute(context.Background(), nil, state)

	require.NotNil(t, state.Response.Headers)
	require.Equal(t, "fail", state.Response.Headers["X-PassCtrl-Outcome"])
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
	require.Nil(t, cloneHeaders(nil))

	original := map[string]string{"a": "1", "b": "2"}
	clone := cloneHeaders(original)
	require.Equal(t, "1", clone["a"])
	require.Equal(t, "2", clone["b"])
	clone["a"] = "updated"
	require.Equal(t, "1", original["a"])
}
