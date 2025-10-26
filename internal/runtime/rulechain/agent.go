package rulechain

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/l0p7/passctrl/internal/expr"
	"github.com/l0p7/passctrl/internal/runtime/forwardpolicy"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/templates"
)

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
	Headers             forwardpolicy.CategoryConfig
	Query               forwardpolicy.CategoryConfig
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

// ResponseSpec captures templating controls for rule-level response overrides.
type ResponseSpec struct {
	Status   int
	Body     string
	BodyFile string
	Headers  forwardpolicy.CategoryConfig
}

// ResponsesSpec groups the rule-level response overrides per outcome.
type ResponsesSpec struct {
	Pass  ResponseSpec
	Fail  ResponseSpec
	Error ResponseSpec
}

// VariablesSpec holds the CEL extraction expressions for each scope.
type VariablesSpec struct {
	Global map[string]VariableSpec
	Rule   map[string]VariableSpec
	Local  map[string]VariableSpec
	// LocalV2 holds v2 local variables (map[string]string) for hybrid CEL/Template evaluation
	LocalV2 map[string]string
}

// VariableSpec captures the CEL expression used to populate a variable.
type VariableSpec struct {
	From string
}

// ResponseDefinition stores compiled response overrides for an outcome.
type ResponseDefinition struct {
	Headers         forwardpolicy.CategoryConfig
	HeaderTemplates map[string]*templates.Template
}

// ResponsesDefinition groups compiled response overrides per outcome.
type ResponsesDefinition struct {
	Pass  ResponseDefinition
	Fail  ResponseDefinition
	Error ResponseDefinition
}

// VariableDefinition stores the compiled CEL program for a variable export.
type VariableDefinition struct {
	Source  string
	Program expr.Program
}

// VariablesDefinition exposes compiled variable programs for each scope.
type VariablesDefinition struct {
	Global map[string]VariableDefinition
	Rule   map[string]VariableDefinition
	Local  map[string]VariableDefinition
	// LocalV2 holds v2 local variables (map[string]string) for hybrid CEL/Template evaluation
	LocalV2 map[string]string
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

// Execute decides whether rules should run or if a cached decision is
// sufficient, short-circuiting on cache hits or admission failures.
func (a *Agent) Execute(_ context.Context, _ *http.Request, state *pipeline.State) pipeline.Result {
	state.Rule.EvaluatedAt = time.Now().UTC()
	state.Rule.History = nil
	state.SetPlan(ExecutionPlan{})

	if state.Cache.Hit {
		state.Rule.Outcome = state.Cache.Decision
		state.Rule.Reason = "decision replayed from cache"
		state.Rule.FromCache = true
		state.Rule.Executed = false
		state.Rule.ShouldExecute = false
		return pipeline.Result{
			Name:    a.Name(),
			Status:  "cached",
			Details: state.Rule.Reason,
		}
	}

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
				Fail:  []string{`forward.headers["x-passctrl-deny"] == "true"`},
				Error: []string{`forward.query["error"] == "true"`},
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
	backend := buildBackendDefinition(spec.Backend)
	responses, err := compileResponseDefinitions(ruleName, spec.Responses, renderer)
	if err != nil {
		return Definition{}, fmt.Errorf("responses: %w", err)
	}
	variables, err := compileVariableDefinitions(env, spec.Variables)
	if err != nil {
		return Definition{}, fmt.Errorf("variables: %w", err)
	}

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

func compileResponseDefinitions(ruleName string, spec ResponsesSpec, renderer *templates.Renderer) (ResponsesDefinition, error) {
	pass, err := compileResponseDefinition(ruleName, "pass", spec.Pass, renderer)
	if err != nil {
		return ResponsesDefinition{}, fmt.Errorf("pass: %w", err)
	}
	fail, err := compileResponseDefinition(ruleName, "fail", spec.Fail, renderer)
	if err != nil {
		return ResponsesDefinition{}, fmt.Errorf("fail: %w", err)
	}
	errorDef, err := compileResponseDefinition(ruleName, "error", spec.Error, renderer)
	if err != nil {
		return ResponsesDefinition{}, fmt.Errorf("error: %w", err)
	}
	return ResponsesDefinition{
		Pass:  pass,
		Fail:  fail,
		Error: errorDef,
	}, nil
}

func compileResponseDefinition(ruleName, category string, spec ResponseSpec, renderer *templates.Renderer) (ResponseDefinition, error) {
	if spec.Status != 0 {
		return ResponseDefinition{}, fmt.Errorf("rule responses no longer support status overrides (found %s.status)", category)
	}
	if strings.TrimSpace(spec.Body) != "" || strings.TrimSpace(spec.BodyFile) != "" {
		return ResponseDefinition{}, fmt.Errorf("rule responses no longer support body overrides (found %s.body/bodyFile)", category)
	}

	def := ResponseDefinition{
		Headers: forwardpolicy.CategoryConfig{
			Allow:  append([]string{}, spec.Headers.Allow...),
			Strip:  append([]string{}, spec.Headers.Strip...),
			Custom: cloneStringMap(spec.Headers.Custom),
		},
		HeaderTemplates: map[string]*templates.Template{},
	}

	if renderer == nil {
		return def, nil
	}

	ruleLabel := strings.TrimSpace(ruleName)
	if ruleLabel == "" {
		ruleLabel = "rule"
	}
	prefix := fmt.Sprintf("%s:%s:response", ruleLabel, strings.ToLower(category))

	if len(def.Headers.Custom) > 0 {
		keys := make([]string, 0, len(def.Headers.Custom))
		for k := range def.Headers.Custom {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := def.Headers.Custom[key]
			tmpl, err := renderer.CompileInline(fmt.Sprintf("%s:header:%s", prefix, key), value)
			if err != nil {
				return ResponseDefinition{}, fmt.Errorf("header %s template: %w", key, err)
			}
			def.HeaderTemplates[key] = tmpl
		}
	}

	return def, nil
}

func compileVariableDefinitions(env *expr.Environment, spec VariablesSpec) (VariablesDefinition, error) {
	def := VariablesDefinition{
		Global:  make(map[string]VariableDefinition),
		Rule:    make(map[string]VariableDefinition),
		Local:   make(map[string]VariableDefinition),
		LocalV2: spec.LocalV2, // V2 local variables for hybrid CEL/Template evaluation
	}

	if err := compileVariableMap(env, spec.Global, def.Global, "global"); err != nil {
		return VariablesDefinition{}, err
	}
	if err := compileVariableMap(env, spec.Rule, def.Rule, "rule"); err != nil {
		return VariablesDefinition{}, err
	}
	if err := compileVariableMap(env, spec.Local, def.Local, "local"); err != nil {
		return VariablesDefinition{}, err
	}

	return def, nil
}

func compileVariableMap(env *expr.Environment, specs map[string]VariableSpec, target map[string]VariableDefinition, scope string) error {
	if len(specs) == 0 {
		return nil
	}
	for name, variable := range specs {
		trimmedName := strings.TrimSpace(name)
		if trimmedName == "" {
			return fmt.Errorf("%s: variable name required", scope)
		}
		exprSrc := strings.TrimSpace(variable.From)
		if exprSrc == "" {
			continue
		}
		program, err := env.CompileValue(exprSrc)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", scope, trimmedName, err)
		}
		target[trimmedName] = VariableDefinition{
			Source:  exprSrc,
			Program: program,
		}
	}
	return nil
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
