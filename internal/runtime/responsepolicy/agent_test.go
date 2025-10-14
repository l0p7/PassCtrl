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
		wantMessage   string
		expectedCache bool
	}{
		"pass": {
			outcome:     "pass",
			wantStatus:  http.StatusOK,
			wantMessage: "access granted",
		},
		"fail with reason": {
			outcome:     "fail",
			reason:      "policy rejected",
			wantStatus:  http.StatusForbidden,
			wantMessage: "policy rejected",
		},
		"fail default": {
			outcome:     "fail",
			wantStatus:  http.StatusForbidden,
			wantMessage: "access denied",
		},
		"error default": {
			outcome:     "error",
			wantStatus:  http.StatusBadGateway,
			wantMessage: "rule error",
		},
		"unknown": {
			outcome:     "",
			wantStatus:  http.StatusInternalServerError,
			wantMessage: "undetermined rule outcome",
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

			if state.Response.Message != tc.wantMessage {
				t.Fatalf("expected message %q, got %q", tc.wantMessage, state.Response.Message)
			}

			if outcome := state.Response.Headers["X-PassCtrl-Outcome"]; outcome != tc.outcome {
				t.Fatalf("expected outcome header %q, got %q", tc.outcome, outcome)
			}
		})
	}
}

func TestCoalesce(t *testing.T) {
	if got := coalesce("", "  ", "value"); got != "value" {
		t.Fatalf("expected trimmed value, got %q", got)
	}

	if got := coalesce("", "  "); got != "" {
		t.Fatalf("expected empty string when no values present, got %q", got)
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
