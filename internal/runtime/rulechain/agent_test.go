package rulechain

import (
	"context"
	"testing"
	"time"

	"github.com/l0p7/passctrl/internal/expr"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/templates"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAgentFiltersDefinitions(t *testing.T) {
	defs := []Definition{{
		Name:        "  allow-login  ",
		Description: "  trims description  ",
	}, {
		Name: "   ",
	}}

	agent := NewAgent(defs)

	require.Len(t, agent.rules, 1)
	require.Equal(t, "allow-login", agent.rules[0].Name)
	require.Equal(t, "trims description", agent.rules[0].Description)
}

func TestAgentExecuteStates(t *testing.T) {
	t.Run("admission failure prevents execution", func(t *testing.T) {
		state := &pipeline.State{}
		state.Admission.Authenticated = false

		agent := NewAgent([]Definition{{Name: "deny"}})
		res := agent.Execute(context.Background(), nil, state)

		require.Equal(t, "short_circuited", res.Status)
		require.False(t, state.Rule.ShouldExecute)
		require.False(t, state.Rule.Executed)
	})

	t.Run("authenticated request populates plan", func(t *testing.T) {
		state := &pipeline.State{}
		state.Admission.Authenticated = true
		state.Rule.History = []pipeline.RuleHistoryEntry{{Name: "previous"}}

		agent := NewAgent([]Definition{{Name: "allow"}})
		res := agent.Execute(context.Background(), nil, state)

		require.Equal(t, "ready", res.Status)
		require.True(t, state.Rule.ShouldExecute)
		require.Nil(t, state.Rule.History)
		require.False(t, state.Rule.EvaluatedAt.IsZero())
		assert.WithinDuration(t, time.Now(), state.Rule.EvaluatedAt, time.Second)

		plan, ok := state.Plan().(ExecutionPlan)
		require.True(t, ok, "expected execution plan to be stored on state")
		require.Len(t, plan.Rules, 1)
		require.NotSame(t, &agent.rules[0], &plan.Rules[0], "execution plan should hold a copy of definitions")
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
	require.NoError(t, err)
	require.Len(t, defs, 1)
	require.NotNil(t, defs[0].PassTemplate)
	require.NotNil(t, defs[0].FailTemplate)
	require.NotNil(t, defs[0].ErrorTemplate)

	ctx := map[string]any{"rule": map[string]any{"outcome": "pass"}}
	out, err := defs[0].PassTemplate.Render(ctx)
	require.NoError(t, err)
	require.Equal(t, "pass granted", out)
}

func TestCompileDefinitionsTemplateError(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	_, err := CompileDefinitions([]DefinitionSpec{{
		Name:        "decision",
		Conditions:  ConditionSpec{Pass: []string{"true"}},
		PassMessage: "{{",
	}}, renderer)
	require.Error(t, err)
	require.Contains(t, err.Error(), "pass message template")
}

func TestCompileDefinitionsRejectsRuleResponseStatusAndBody(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	_, err := CompileDefinitions([]DefinitionSpec{{
		Name: "reject-status",
		Responses: ResponsesSpec{
			Pass: ResponseSpec{Status: 201},
		},
	}}, renderer)
	require.Error(t, err)
	require.Contains(t, err.Error(), "status overrides")

	_, err = CompileDefinitions([]DefinitionSpec{{
		Name: "reject-body",
		Responses: ResponsesSpec{
			Fail: ResponseSpec{Body: "not allowed"},
		},
	}}, renderer)
	require.Error(t, err)
	require.Contains(t, err.Error(), "body overrides")
}

func TestCompileConditionProgramsErrorWrapping(t *testing.T) {
	env, err := expr.NewEnvironment()
	require.NoError(t, err)
	_, err = compileConditionPrograms(env, ConditionSpec{Pass: []string{"invalid('"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "pass conditions")
}

func TestCompileProgramsSkipsEmptyExpressions(t *testing.T) {
	env, err := expr.NewEnvironment()
	require.NoError(t, err)
	programs, err := compilePrograms(env, []string{"  ", "true", ""})
	require.NoError(t, err)
	require.Len(t, programs, 1)
	require.Equal(t, "true", programs[0].Source())
}

func TestDefaultDefinitions(t *testing.T) {
	defs := DefaultDefinitions(nil)
	require.NotEmpty(t, defs)
	for _, def := range defs {
		require.NotEmpty(t, def.Name)
		require.False(t, def.Backend.IsConfigured())
	}
}
