package rulechain

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/l0p7/passctrl/internal/expr"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/templates"
)

// CacheTTLSpec defines TTLs for different rule outcomes.
type CacheTTLSpec struct {
	Pass  string // Duration: "5m", "30s", etc.
	Fail  string // Duration: "30s", "1m", etc.
	Error string // Always "0s" - errors never cached
}

// CacheConfigSpec defines per-rule caching configuration.
type CacheConfigSpec struct {
	FollowCacheControl bool         // Parse backend Cache-Control header
	TTL                CacheTTLSpec // TTLs per outcome
	Strict             *bool        // Include upstream variables in cache key (default: true)
}

// DefinitionSpec captures the declarative rule definition loaded from
// configuration prior to compilation.
type DefinitionSpec struct {
	Name         string
	Description  string
	Auth         []AuthDirectiveSpec
	Conditions   ConditionSpec
	Backend      BackendDefinitionSpec
	PassMessage  string
	Responses    ResponsesSpec
	Variables    VariablesSpec
	FailMessage  string
	ErrorMessage string
	Cache        CacheConfigSpec
}

// ConditionSpec groups the CEL expressions that govern rule outcomes.
type ConditionSpec struct {
	Pass  []string
	Fail  []string
	Error []string
}

// BackendDefinitionSpec captures the declarative backend configuration that a
// rule may invoke when evaluating conditions.
type BackendDefinitionSpec struct {
	URL                 string
	Method              string
	ForwardProxyHeaders bool
	Headers             map[string]*string
	Query               map[string]*string
	Body                string
	BodyFile            string
	Accepted            []int
	Pagination          BackendPaginationSpec
}

// BackendPaginationSpec describes how the backend should paginate responses.
type BackendPaginationSpec struct {
	Type     string
	MaxPages int
}

// ConditionPrograms contains the compiled CEL programs for each rule outcome.
type ConditionPrograms struct {
	Pass  []expr.Program
	Fail  []expr.Program
	Error []expr.Program
}

// ResponseSpec captures rule-level response variable exports.
// Status, Body, BodyFile, and Headers are no longer supported and will cause compilation errors if specified.
type ResponseSpec struct {
	Status    int               // Deprecated: will error if non-zero
	Body      string            // Deprecated: will error if non-empty
	BodyFile  string            // Deprecated: will error if non-empty
	Variables map[string]string // Exported to subsequent rules AND endpoint response templates
}

// ResponsesSpec groups the rule-level response overrides per outcome.
type ResponsesSpec struct {
	Pass  ResponseSpec
	Fail  ResponseSpec
	Error ResponseSpec
}

// VariablesSpec holds variable expressions for rule-local temporary calculations.
// These variables are evaluated during rule execution and stored in state.Rule.Variables.Local.
// They are NOT cached and NOT exported to subsequent rules or endpoint templates.
// For sharing data, use responses.pass/fail/error.variables instead.
type VariablesSpec struct {
	// Variables holds local/temporary variable expressions for hybrid CEL/Template evaluation
	// Auto-detected by presence of {{ for templates, otherwise treated as CEL
	Variables map[string]string
}

// ResponseDefinition stores compiled response configuration for an outcome.
type ResponseDefinition struct {
	// ExportedVariables holds variable expressions to evaluate after outcome is determined
	// Evaluated with hybrid CEL/Template evaluator, accessible to subsequent rules AND endpoint response templates
	ExportedVariables map[string]string
}

// ResponsesDefinition groups compiled response overrides per outcome.
type ResponsesDefinition struct {
	Pass  ResponseDefinition
	Fail  ResponseDefinition
	Error ResponseDefinition
}

// VariablesDefinition stores compiled variable expressions for rule-local temporary calculations.
// Variables use hybrid CEL/Template evaluation (auto-detected by {{ presence).
type VariablesDefinition struct {
	// Variables holds local/temporary variable expressions for hybrid CEL/Template evaluation
	Variables map[string]string
}

// Definition represents a fully compiled rule that can be executed by the rule
// chain and rule execution agents.
type Definition struct {
	Name          string
	Description   string
	Auth          []AuthDirective
	Conditions    ConditionPrograms
	Backend       BackendDefinition
	PassMessage   string
	Responses     ResponsesDefinition
	Variables     VariablesDefinition
	FailMessage   string
	ErrorMessage  string
	PassTemplate  *templates.Template
	FailTemplate  *templates.Template
	ErrorTemplate *templates.Template
	Cache         CacheConfigSpec
}

// ExecutionPlan records the rule definitions that should be evaluated for the
// current request.
type ExecutionPlan struct {
	Rules []Definition
}

// Agent prepares the execution plan for the rule chain once cache and admission
// checks have passed.
type Agent struct {
	rules []Definition
}

// NewAgent constructs an Agent instance with the supplied rule definitions.
func NewAgent(rules []Definition) *Agent {
	pruned := make([]Definition, 0, len(rules))
	for _, rule := range rules {
		if strings.TrimSpace(rule.Name) == "" {
			continue
		}
		rule.Name = strings.TrimSpace(rule.Name)
		rule.Description = strings.TrimSpace(rule.Description)
		rule.PassMessage = strings.TrimSpace(rule.PassMessage)
		rule.FailMessage = strings.TrimSpace(rule.FailMessage)
		rule.ErrorMessage = strings.TrimSpace(rule.ErrorMessage)
		pruned = append(pruned, rule)
	}
	return &Agent{rules: pruned}
}

// Name identifies the rule chain agent.
func (a *Agent) Name() string { return "rule_chain" }

// Execute decides whether rules should run, short-circuiting on admission failures.
func (a *Agent) Execute(_ context.Context, _ *http.Request, state *pipeline.State) pipeline.Result {
	state.Rule.EvaluatedAt = time.Now().UTC()
	state.Rule.History = nil
	state.SetPlan(ExecutionPlan{})

	if !state.Admission.Authenticated {
		state.Rule.Outcome = "fail"
		state.Rule.Reason = "admission rejected request"
		state.Rule.Executed = false
		state.Rule.ShouldExecute = false
		return pipeline.Result{
			Name:    a.Name(),
			Status:  "short_circuited",
			Details: state.Rule.Reason,
		}
	}

	compiled := make([]Definition, len(a.rules))
	copy(compiled, a.rules)

	state.SetPlan(ExecutionPlan{Rules: compiled})
	state.Rule.ShouldExecute = true
	state.Rule.Outcome = ""
	state.Rule.Reason = ""
	state.Rule.Executed = false
	state.Rule.FromCache = false

	return pipeline.Result{
		Name:    a.Name(),
		Status:  "ready",
		Details: "rule evaluation will proceed",
		Meta: map[string]any{
			"rules": len(compiled),
		},
	}
}

// DefaultDefinitions returns the built-in rule set used when no configuration
// is available.
func DefaultDefinitions(renderer *templates.Renderer) []Definition {
	specs := []DefinitionSpec{
		{
			Name: "allow-when-not-denied",
			Conditions: ConditionSpec{
				Fail:  []string{`lookup(forward.headers, "x-passctrl-deny") == "true"`},
				Error: []string{`lookup(forward.query, "error") == "true"`},
			},
			PassMessage: "rule accepted the curated request",
		},
	}
	defs, err := CompileDefinitions(specs, renderer)
	if err != nil {
		panic(fmt.Sprintf("compile default rules: %v", err))
	}
	return defs
}

// CompileDefinitions converts the declarative rule specs into runtime
// definitions ready for execution.
func CompileDefinitions(specs []DefinitionSpec, renderer *templates.Renderer) ([]Definition, error) {
	env, err := expr.NewEnvironment()
	if err != nil {
		return nil, err
	}
	compiled := make([]Definition, 0, len(specs))
	for _, spec := range specs {
		def, err := compileDefinition(env, spec, renderer)
		if err != nil {
			return nil, fmt.Errorf("rule %s: %w", spec.Name, err)
		}
		compiled = append(compiled, def)
	}
	return compiled, nil
}

func compileDefinition(env *expr.Environment, spec DefinitionSpec, renderer *templates.Renderer) (Definition, error) {
	ruleName := strings.TrimSpace(spec.Name)

	programs, err := compileConditionPrograms(env, spec.Conditions)
	if err != nil {
		return Definition{}, err
	}

	auth, err := compileAuthDirectives(ruleName, spec.Auth, renderer)
	if err != nil {
		return Definition{}, err
	}
	backend := buildBackendDefinition(spec.Backend, renderer)
	responses, err := compileResponseDefinitions(spec.Responses)
	if err != nil {
		return Definition{}, fmt.Errorf("responses: %w", err)
	}
	variables := compileVariableDefinitions(spec.Variables)

	compileName := ruleName
	if compileName == "" {
		compileName = "rule"
	}
	def := Definition{
		Name:         strings.TrimSpace(spec.Name),
		Description:  strings.TrimSpace(spec.Description),
		Auth:         auth,
		Conditions:   programs,
		Backend:      backend,
		PassMessage:  strings.TrimSpace(spec.PassMessage),
		Responses:    responses,
		Variables:    variables,
		FailMessage:  strings.TrimSpace(spec.FailMessage),
		ErrorMessage: strings.TrimSpace(spec.ErrorMessage),
		Cache:        spec.Cache,
	}
	if renderer != nil {
		if tmpl, err := renderer.CompileInline(fmt.Sprintf("%s:pass", compileName), spec.PassMessage); err != nil {
			return Definition{}, fmt.Errorf("pass message template: %w", err)
		} else {
			def.PassTemplate = tmpl
		}
		if tmpl, err := renderer.CompileInline(fmt.Sprintf("%s:fail", compileName), spec.FailMessage); err != nil {
			return Definition{}, fmt.Errorf("fail message template: %w", err)
		} else {
			def.FailTemplate = tmpl
		}
		if tmpl, err := renderer.CompileInline(fmt.Sprintf("%s:error", compileName), spec.ErrorMessage); err != nil {
			return Definition{}, fmt.Errorf("error message template: %w", err)
		} else {
			def.ErrorTemplate = tmpl
		}
		// Compile backend request body templates
		if strings.TrimSpace(spec.Backend.Body) != "" {
			if tmpl, err := renderer.CompileInline(fmt.Sprintf("%s:backend:body", compileName), spec.Backend.Body); err == nil {
				def.Backend.BodyTemplate = tmpl
			} else {
				return Definition{}, fmt.Errorf("backend body template: %w", err)
			}
		} else if strings.TrimSpace(spec.Backend.BodyFile) != "" {
			name := fmt.Sprintf("%s:backend:bodyFile", compileName)
			tmpl, err := renderer.CompileInline(name, spec.Backend.BodyFile)
			if err != nil {
				return Definition{}, fmt.Errorf("backend body file template compile: %w", err)
			}
			def.Backend.BodyTemplate = tmpl
		}
	}
	return def, nil
}

func compileConditionPrograms(env *expr.Environment, spec ConditionSpec) (ConditionPrograms, error) {
	pass, err := compilePrograms(env, spec.Pass)
	if err != nil {
		return ConditionPrograms{}, fmt.Errorf("pass conditions: %w", err)
	}
	fail, err := compilePrograms(env, spec.Fail)
	if err != nil {
		return ConditionPrograms{}, fmt.Errorf("fail conditions: %w", err)
	}
	errorConds, err := compilePrograms(env, spec.Error)
	if err != nil {
		return ConditionPrograms{}, fmt.Errorf("error conditions: %w", err)
	}
	return ConditionPrograms{Pass: pass, Fail: fail, Error: errorConds}, nil
}

func compilePrograms(env *expr.Environment, expressions []string) ([]expr.Program, error) {
	programs := make([]expr.Program, 0, len(expressions))
	for _, source := range expressions {
		trimmed := strings.TrimSpace(source)
		if trimmed == "" {
			continue
		}
		program, err := env.Compile(trimmed)
		if err != nil {
			return nil, fmt.Errorf("compile condition %q: %w", trimmed, err)
		}
		programs = append(programs, program)
	}
	return programs, nil
}

func compileResponseDefinitions(spec ResponsesSpec) (ResponsesDefinition, error) {
	pass, err := compileResponseDefinition("pass", spec.Pass)
	if err != nil {
		return ResponsesDefinition{}, fmt.Errorf("pass: %w", err)
	}
	fail, err := compileResponseDefinition("fail", spec.Fail)
	if err != nil {
		return ResponsesDefinition{}, fmt.Errorf("fail: %w", err)
	}
	errorDef, err := compileResponseDefinition("error", spec.Error)
	if err != nil {
		return ResponsesDefinition{}, fmt.Errorf("error: %w", err)
	}
	return ResponsesDefinition{
		Pass:  pass,
		Fail:  fail,
		Error: errorDef,
	}, nil
}

func compileResponseDefinition(category string, spec ResponseSpec) (ResponseDefinition, error) {
	// Rules no longer define status, body, or headers - only variables
	if spec.Status != 0 {
		return ResponseDefinition{}, fmt.Errorf("rule responses no longer support status overrides (found %s.status)", category)
	}
	if strings.TrimSpace(spec.Body) != "" || strings.TrimSpace(spec.BodyFile) != "" {
		return ResponseDefinition{}, fmt.Errorf("rule responses no longer support body overrides (found %s.body/bodyFile)", category)
	}

	def := ResponseDefinition{
		ExportedVariables: cloneStringMap(spec.Variables),
	}

	return def, nil
}

func compileVariableDefinitions(spec VariablesSpec) VariablesDefinition {
	// Variables use hybrid CEL/Template evaluation (no pre-compilation needed)
	// Expressions are stored as-is and evaluated at runtime
	return VariablesDefinition{
		Variables: cloneStringMap(spec.Variables),
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
