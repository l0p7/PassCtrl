package expr

import (
	"fmt"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
)

// Environment builds and compiles CEL programs against the PassCtrl runtime state.
type Environment struct {
	env *cel.Env
}

// NewEnvironment declares the CEL variables exposed to rule conditions.
func NewEnvironment() (*Environment, error) {
	env, err := cel.NewEnv(
		cel.Variable("raw", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("admission", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("forward", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("backend", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("vars", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("now", cel.DynType),
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
		return nil, fmt.Errorf("expr: build environment: %w", err)
	}
	return &Environment{env: env}, nil
}

// Program wraps a compiled CEL program that yields a boolean result.
type Program struct {
	source   string
	program  cel.Program
	wantBool bool
}

// Compile prepares the program for execution, ensuring the expression yields a boolean.
func (e *Environment) Compile(expression string) (Program, error) {
	return e.compile(expression, true)
}

// CompileValue prepares the program for execution without enforcing a boolean
// return type. It is used for variable extraction where CEL programs can yield
// arbitrary values.
func (e *Environment) CompileValue(expression string) (Program, error) {
	return e.compile(expression, false)
}

// EvalBool executes the program against the provided activation and coerces the result to bool.
func (p Program) EvalBool(vars map[string]any) (bool, error) {
	if p.program == nil {
		return false, fmt.Errorf("expr: program not initialized")
	}
	if !p.wantBool {
		return false, fmt.Errorf("expr: program %q does not return a boolean", p.source)
	}
	val, _, err := p.program.Eval(vars)
	if err != nil {
		return false, fmt.Errorf("expr: eval %q: %w", p.source, err)
	}
	switch v := val.(type) {
	case types.Bool:
		return bool(v), nil
	case ref.Val:
		if v.Type() == types.BoolType {
			if b, ok := v.Value().(bool); ok {
				return b, nil
			}
		}
	}
	return false, fmt.Errorf("expr: %q yielded non-bool result %T", p.source, val)
}

// Source returns the original CEL expression for logging.
func (p Program) Source() string { return p.source }

// Eval executes the CEL program and returns the raw value.
func (p Program) Eval(vars map[string]any) (any, error) {
	if p.program == nil {
		return nil, fmt.Errorf("expr: program not initialized")
	}
	val, _, err := p.program.Eval(vars)
	if err != nil {
		return nil, fmt.Errorf("expr: eval %q: %w", p.source, err)
	}
	return val.Value(), nil
}

func (e *Environment) compile(expression string, wantBool bool) (Program, error) {
	expr := strings.TrimSpace(expression)
	if expr == "" {
		return Program{}, fmt.Errorf("expr: expression required")
	}
	ast, issues := e.env.Compile(expr)
	if issues != nil && issues.Err() != nil {
		return Program{}, fmt.Errorf("expr: compile %q: %w", expr, issues.Err())
	}
	if wantBool {
		if t := ast.OutputType(); t != cel.BoolType && t != cel.DynType {
			return Program{}, fmt.Errorf("expr: %q must return bool, got %s", expr, cel.FormatCELType(t))
		}
	}
	program, err := e.env.Program(ast)
	if err != nil {
		return Program{}, fmt.Errorf("expr: program %q: %w", expr, err)
	}
	return Program{source: expr, program: program, wantBool: wantBool}, nil
}

func lookupMapValue(mapVal ref.Val, key ref.Val) ref.Val {
	mapper, ok := mapVal.(traits.Mapper)
	if !ok {
		return types.NewErr("expr: lookup only supports string-key maps")
	}
	value, found := mapper.Find(key)
	if !found {
		return types.NullValue
	}
	if value == nil {
		return types.NullValue
	}
	return value
}
