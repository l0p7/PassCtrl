package responsepolicy

import (
	"context"
	"net/http"
	"strings"

	"github.com/l0p7/passctrl/internal/runtime/pipeline"
)

// Agent materializes the HTTP response from the rule outcome.
type Agent struct{}

// New constructs a response policy agent.
func New() *Agent { return &Agent{} }

// Name identifies the response policy agent for logging and snapshots.
func (a *Agent) Name() string { return "response_policy" }

// Execute materializes the HTTP response structure from the rule outcome,
// ensuring a status code and headers are ready for the client.
func (a *Agent) Execute(_ context.Context, _ *http.Request, state *pipeline.State) pipeline.Result {
	if state.Response.Status != 0 {
		return pipeline.Result{
			Name:    a.Name(),
			Status:  "cached",
			Details: "response replayed from cache",
		}
	}
	switch state.Rule.Outcome {
	case "pass":
		state.Response.Status = http.StatusOK
		state.Response.Message = "access granted"
	case "fail":
		state.Response.Status = http.StatusForbidden
		state.Response.Message = coalesce(state.Rule.Reason, "access denied")
	case "error":
		state.Response.Status = http.StatusBadGateway
		state.Response.Message = coalesce(state.Rule.Reason, "rule error")
	default:
		state.Response.Status = http.StatusInternalServerError
		state.Response.Message = "undetermined rule outcome"
	}
	if state.Response.Headers == nil {
		state.Response.Headers = make(map[string]string)
	}
	state.Response.Headers["X-PassCtrl-Outcome"] = state.Rule.Outcome
	return pipeline.Result{
		Name:    a.Name(),
		Status:  "rendered",
		Details: state.Response.Message,
	}
}

func coalesce(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
