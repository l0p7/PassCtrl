package expr

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/l0p7/passctrl/internal/templates"
)

// RuleContextBuilder is a function that builds the context for rule variable evaluation.
// It's defined as a type to avoid circular dependencies with pipeline.State.
type RuleContextBuilder func() map[string]any

// HybridEvaluator can evaluate both CEL expressions and Go templates.
// It automatically detects the type based on the presence of {{ in the expression.
type HybridEvaluator struct {
	celEnv   *Environment
	renderer *templates.Renderer
}

// NewHybridEvaluator creates an evaluator that supports both CEL and templates.
// Uses the request environment (for endpoint variables).
func NewHybridEvaluator(renderer *templates.Renderer) (*HybridEvaluator, error) {
	celEnv, err := NewRequestEnvironment()
	if err != nil {
		return nil, fmt.Errorf("hybrid: create CEL environment: %w", err)
	}
	return &HybridEvaluator{
		celEnv:   celEnv,
		renderer: renderer,
	}, nil
}

// NewRuleHybridEvaluator creates an evaluator for rule local variables.
// Uses the rule environment (includes backend, auth, vars, request, variables).
func NewRuleHybridEvaluator(renderer *templates.Renderer) (*HybridEvaluator, error) {
	celEnv, err := NewRuleEnvironment()
	if err != nil {
		return nil, fmt.Errorf("hybrid: create rule CEL environment: %w", err)
	}
	return &HybridEvaluator{
		celEnv:   celEnv,
		renderer: renderer,
	}, nil
}

// Evaluate executes the expression and returns the result.
// If the expression contains {{, it's treated as a template.
// Otherwise, it's treated as a CEL expression.
func (h *HybridEvaluator) Evaluate(expression string, data any) (any, error) {
	trimmed := strings.TrimSpace(expression)
	if trimmed == "" {
		return "", nil
	}

	// Detect template syntax
	if strings.Contains(trimmed, "{{") {
		return h.evaluateTemplate(trimmed, data)
	}

	// Evaluate as CEL
	return h.evaluateCEL(trimmed, data)
}

// evaluateTemplate renders a Go template.
func (h *HybridEvaluator) evaluateTemplate(source string, data any) (string, error) {
	tmpl, err := h.renderer.CompileInline("var", source)
	if err != nil {
		return "", fmt.Errorf("hybrid: compile template: %w", err)
	}
	result, err := tmpl.Render(data)
	if err != nil {
		return "", fmt.Errorf("hybrid: render template: %w", err)
	}
	return result, nil
}

// evaluateCEL evaluates a CEL expression.
func (h *HybridEvaluator) evaluateCEL(expression string, data any) (any, error) {
	prog, err := h.celEnv.CompileValue(expression)
	if err != nil {
		return nil, fmt.Errorf("hybrid: compile CEL: %w", err)
	}

	// Convert data to CEL activation map
	vars, ok := data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("hybrid: CEL requires map[string]any activation, got %T", data)
	}

	result, err := prog.Eval(vars)
	if err != nil {
		return nil, fmt.Errorf("hybrid: evaluate CEL: %w", err)
	}
	return result, nil
}

// RequestContext builds the context data for endpoint variable evaluation.
// It includes request metadata accessible as:
// - CEL: request.remoteAddr, request.method, request.path, request.headers["name"], request.query["param"]
// - Template: {{ .request.remoteAddr }}, {{ .request.headers.x_api_key }}
func RequestContext(r *http.Request) map[string]any {
	headers := make(map[string]string, len(r.Header))
	for key, values := range r.Header {
		if len(values) > 0 {
			// Use first value, normalize key to lowercase with underscores for template access
			headers[strings.ToLower(key)] = values[0]
		}
	}

	query := make(map[string]string)
	for key, values := range r.URL.Query() {
		if len(values) > 0 {
			query[key] = values[0]
		}
	}

	return map[string]any{
		"request": map[string]any{
			"remoteAddr": r.RemoteAddr,
			"method":     r.Method,
			"path":       r.URL.Path,
			"headers":    headers,
			"query":      query,
		},
	}
}

// NewRequestEnvironment creates a CEL environment for endpoint variable evaluation.
// It includes request context variables.
func NewRequestEnvironment() (*Environment, error) {
	env, err := cel.NewEnv(
		cel.Variable("request", cel.MapType(cel.StringType, cel.DynType)),
		cel.Function("lookup",
			cel.Overload("lookup_map_string",
				[]*cel.Type{cel.MapType(cel.StringType, cel.DynType), cel.StringType},
				cel.DynType,
				cel.BinaryBinding(lookupMapValue),
			),
		),
		cel.HomogeneousAggregateLiterals(),
	)
	if err != nil {
		return nil, fmt.Errorf("expr: build request environment: %w", err)
	}
	return &Environment{env: env}, nil
}

// NewRuleEnvironment creates a CEL environment for rule local variable evaluation.
// It includes all context available to rules: backend, auth, vars, request, variables.
func NewRuleEnvironment() (*Environment, error) {
	env, err := cel.NewEnv(
		cel.Variable("backend", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("auth", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("vars", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("request", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("variables", cel.MapType(cel.StringType, cel.DynType)),
		cel.Function("lookup",
			cel.Overload("lookup_map_string",
				[]*cel.Type{cel.MapType(cel.StringType, cel.DynType), cel.StringType},
				cel.DynType,
				cel.BinaryBinding(lookupMapValue),
			),
		),
		cel.HomogeneousAggregateLiterals(),
	)
	if err != nil {
		return nil, fmt.Errorf("expr: build rule environment: %w", err)
	}
	return &Environment{env: env}, nil
}
