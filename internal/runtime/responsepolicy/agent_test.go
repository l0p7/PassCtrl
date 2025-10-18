package responsepolicy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/l0p7/passctrl/internal/runtime/forwardpolicy"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/templates"
)

func TestAgentExecuteCachedResponse(t *testing.T) {
	state := &pipeline.State{}
	state.Response.Status = http.StatusAccepted
	state.Response.Message = "ready"

	agent := New()
	res := agent.Execute(context.Background(), nil, state)

	if res.Status != "cached" {
		t.Fatalf("expected cached status, got %s", res.Status)
	}

	if state.Response.Message != "ready" {
		t.Fatalf("expected response to remain unchanged on cache reuse")
	}
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

			if res.Status != "rendered" {
				t.Fatalf("expected rendered status, got %s", res.Status)
			}

			if state.Response.Status != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, state.Response.Status)
			}

			if state.Response.Message != "" {
				t.Fatalf("expected empty message by default, got %q", state.Response.Message)
			}

			if outcome := state.Response.Headers["X-PassCtrl-Outcome"]; outcome != tc.outcome {
				t.Fatalf("expected outcome header %q, got %q", tc.outcome, outcome)
			}
		})
	}
}

func TestAgentExecuteInitializesHeaders(t *testing.T) {
	state := &pipeline.State{}
	state.Rule.Outcome = "fail"
	state.Response.Headers = nil

	agent := New()
	_ = agent.Execute(context.Background(), nil, state)

	if state.Response.Headers == nil {
		t.Fatalf("expected response headers to be initialized")
	}

	if value := state.Response.Headers["X-PassCtrl-Outcome"]; value != "fail" {
		t.Fatalf("expected outcome header to be recorded, got %q", value)
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

	if res.Status != "rendered" {
		t.Fatalf("expected rendered status with config, got %s", res.Status)
	}
	if state.Response.Status != http.StatusAccepted {
		t.Fatalf("expected status override to apply, got %d", state.Response.Status)
	}
	if state.Response.Message != "hello endpoint-a" {
		t.Fatalf("expected templated body to render, got %q", state.Response.Message)
	}
	if _, exists := state.Response.Headers["stripme"]; exists {
		t.Fatalf("expected strip list to remove header")
	}
	if _, exists := state.Response.Headers["other"]; exists {
		t.Fatalf("expected allow list to drop unlisted headers")
	}
	if state.Response.Headers["keep"] != "value" {
		t.Fatalf("expected allow list to retain keep header, got %q", state.Response.Headers["keep"])
	}
	if got := state.Response.Headers["X-Rendered"]; got != "outcome pass" {
		t.Fatalf("expected rendered custom header, got %q", got)
	}
	if got := state.Response.Headers["X-Static"]; got != "static" {
		t.Fatalf("expected static custom header to be trimmed, got %q", got)
	}
	if outcome := state.Response.Headers["X-PassCtrl-Outcome"]; outcome != "pass" {
		t.Fatalf("expected outcome header to remain accurate, got %q", outcome)
	}
}

func TestCloneHeaders(t *testing.T) {
	if clone := cloneHeaders(nil); clone != nil {
		t.Fatalf("expected nil clone for nil input, got %#v", clone)
	}

	original := map[string]string{"a": "1", "b": "2"}
	clone := cloneHeaders(original)
	if clone["a"] != "1" || clone["b"] != "2" {
		t.Fatalf("expected clone to copy values, got %#v", clone)
	}
	clone["a"] = "updated"
	if original["a"] != "1" {
		t.Fatalf("expected clone to be independent from original")
	}
}
