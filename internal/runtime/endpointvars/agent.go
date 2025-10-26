package endpointvars

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/l0p7/passctrl/internal/expr"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/templates"
)

// Agent evaluates endpoint-level variables once per request and stores them
// in state.Variables.Global for use by all rules. Variables support both CEL
// and Go templates, detected automatically by the presence of {{.
type Agent struct {
	variables map[string]string
	evaluator *expr.HybridEvaluator
	logger    *slog.Logger
}

// New creates an endpoint variable evaluation agent.
func New(variables map[string]string, renderer *templates.Renderer, logger *slog.Logger) (*Agent, error) {
	evaluator, err := expr.NewHybridEvaluator(renderer)
	if err != nil {
		return nil, fmt.Errorf("endpointvars: create evaluator: %w", err)
	}

	return &Agent{
		variables: variables,
		evaluator: evaluator,
		logger:    logger,
	}, nil
}

// Name identifies the agent for observability.
func (a *Agent) Name() string {
	return "endpoint_variables"
}

// Execute evaluates all endpoint variables and stores results in state.Variables.Global.
func (a *Agent) Execute(ctx context.Context, r *http.Request, state *pipeline.State) pipeline.Result {
	// Skip if no variables configured
	if len(a.variables) == 0 {
		return pipeline.Result{
			Name:   a.Name(),
			Status: "skipped",
		}
	}

	// Build request context for evaluation
	requestCtx := expr.RequestContext(r)

	// Evaluate each variable
	evaluated := make(map[string]any, len(a.variables))
	for name, expression := range a.variables {
		value, err := a.evaluator.Evaluate(expression, requestCtx)
		if err != nil {
			if a.logger != nil {
				a.logger.Warn("endpoint variable evaluation failed",
					slog.String("variable", name),
					slog.String("expression", expression),
					slog.Any("error", err),
				)
			}
			// Continue with other variables on error, use empty string
			evaluated[name] = ""
			continue
		}
		evaluated[name] = value
	}

	// Store in state for rule access
	state.Variables.Global = evaluated

	if a.logger != nil {
		a.logger.Debug("endpoint variables evaluated",
			slog.Int("count", len(evaluated)),
		)
	}

	return pipeline.Result{
		Name:   a.Name(),
		Status: "completed",
		Meta: map[string]any{
			"variables_count": len(evaluated),
		},
	}
}
