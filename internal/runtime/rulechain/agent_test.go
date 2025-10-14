package rulechain

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/l0p7/passctrl/internal/expr"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/templates"
)

func TestNewAgentFiltersDefinitions(t *testing.T) {
	defs := []Definition{{
		Name:        "  allow-login  ",
		Description: "  trims description  ",
	}, {
		Name: "   ",
	}}

	agent := NewAgent(defs)

	if len(agent.rules) != 1 {
		t.Fatalf("expected only non-empty definition to be retained, got %d", len(agent.rules))
	}

	if agent.rules[0].Name != "allow-login" {
		t.Fatalf("expected definition name to be trimmed, got %q", agent.rules[0].Name)
	}

	if agent.rules[0].Description != "trims description" {
		t.Fatalf("expected description to be trimmed, got %q", agent.rules[0].Description)
	}
}

func TestAgentExecuteStates(t *testing.T) {
	t.Run("cache hit short circuits", func(t *testing.T) {
		state := &pipeline.State{}
		state.Cache.Hit = true
		state.Cache.Decision = "pass"

		agent := NewAgent(nil)
		res := agent.Execute(context.Background(), nil, state)

		if res.Status != "cached" {
			t.Fatalf("expected cached status, got %s", res.Status)
		}

		if state.Rule.Outcome != "pass" || !state.Rule.FromCache || state.Rule.Executed {
			t.Fatalf("expected cached decision reflected in rule state: %#v", state.Rule)
		}
	})

	t.Run("admission failure prevents execution", func(t *testing.T) {
		state := &pipeline.State{}
		state.Admission.Authenticated = false

		agent := NewAgent([]Definition{{Name: "deny"}})
		res := agent.Execute(context.Background(), nil, state)

		if res.Status != "short_circuited" {
			t.Fatalf("expected short_circuited status, got %s", res.Status)
		}

		if state.Rule.ShouldExecute || state.Rule.Executed {
			t.Fatalf("expected rule execution to be disabled on admission failure: %#v", state.Rule)
		}
	})

	t.Run("authenticated request populates plan", func(t *testing.T) {
		state := &pipeline.State{}
		state.Admission.Authenticated = true
		state.Rule.History = []pipeline.RuleHistoryEntry{{Name: "previous"}}

		agent := NewAgent([]Definition{{Name: "allow"}})
		res := agent.Execute(context.Background(), nil, state)

		if res.Status != "ready" {
			t.Fatalf("expected ready status, got %s", res.Status)
		}

		if state.Rule.ShouldExecute != true {
			t.Fatalf("expected rule execution to proceed")
		}

		if state.Rule.History != nil {
			t.Fatalf("rule history should be cleared on new evaluation")
		}

		if state.Rule.EvaluatedAt.IsZero() || time.Since(state.Rule.EvaluatedAt) > time.Second {
			t.Fatalf("expected evaluation timestamp to be set recently, got %v", state.Rule.EvaluatedAt)
		}

		plan, ok := state.Plan().(ExecutionPlan)
		if !ok {
			t.Fatalf("expected execution plan to be stored on state")
		}

		if len(plan.Rules) != 1 {
			t.Fatalf("expected single rule in execution plan, got %d", len(plan.Rules))
		}

		if &plan.Rules[0] == &agent.rules[0] {
			t.Fatalf("execution plan should hold a copy of definitions, not original slice")
		}
	})
}

func TestCompileDefinitionsBuildsTemplates(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	defs, err := CompileDefinitions([]DefinitionSpec{{
		Name: "decision",
		Conditions: ConditionSpec{
			Pass: []string{"true"},
		},
		PassMessage:  "{{ .rule.outcome }} granted",
		FailMessage:  "{{ .rule.outcome }} denied",
		ErrorMessage: "{{ .rule.outcome }} errored",
	}}, renderer)
	if err != nil {
		t.Fatalf("compile definitions: %v", err)
	}

	if len(defs) != 1 {
		t.Fatalf("expected single compiled definition, got %d", len(defs))
	}

	if defs[0].PassTemplate == nil || defs[0].FailTemplate == nil || defs[0].ErrorTemplate == nil {
		t.Fatalf("expected all templates to be compiled")
	}

	ctx := map[string]any{"rule": map[string]any{"outcome": "pass"}}
	out, err := defs[0].PassTemplate.Render(ctx)
	if err != nil {
		t.Fatalf("render pass template: %v", err)
	}

	if out != "pass granted" {
		t.Fatalf("unexpected template output: %q", out)
	}
}

func TestCompileDefinitionsTemplateError(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	_, err := CompileDefinitions([]DefinitionSpec{{
		Name:        "decision",
		Conditions:  ConditionSpec{Pass: []string{"true"}},
		PassMessage: "{{",
	}}, renderer)
	if err == nil {
		t.Fatalf("expected template compilation error")
	}
	if !strings.Contains(err.Error(), "pass message template") {
		t.Fatalf("expected error to reference pass template compilation, got %v", err)
	}
}

func TestCompileConditionProgramsErrorWrapping(t *testing.T) {
	env, err := expr.NewEnvironment()
	if err != nil {
		t.Fatalf("new environment: %v", err)
	}
	_, err = compileConditionPrograms(env, ConditionSpec{Pass: []string{"invalid('"}})
	if err == nil {
		t.Fatalf("expected compilation failure")
	}
	if !strings.Contains(err.Error(), "pass conditions") {
		t.Fatalf("expected error to annotate failing condition set, got %v", err)
	}
}

func TestCompileProgramsSkipsEmptyExpressions(t *testing.T) {
	env, err := expr.NewEnvironment()
	if err != nil {
		t.Fatalf("new environment: %v", err)
	}
	programs, err := compilePrograms(env, []string{"  ", "true", ""})
	if err != nil {
		t.Fatalf("compile programs: %v", err)
	}
	if len(programs) != 1 {
		t.Fatalf("expected only non-empty expressions to be compiled, got %d", len(programs))
	}
	if src := programs[0].Source(); src != "true" {
		t.Fatalf("expected expression to be trimmed, got %q", src)
	}
}
