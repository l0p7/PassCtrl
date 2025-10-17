package responsepolicy

import (
	"context"
	"net/http"
	"testing"

	"github.com/l0p7/passctrl/internal/runtime/pipeline"
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
