package expr

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLookupMapValue(t *testing.T) {
	env, err := NewEnvironment()
	require.NoError(t, err)

	program, err := env.Compile(`lookup(forward.headers, "key") == "value"`)
	require.NoError(t, err)

	activation := map[string]any{
		"forward": map[string]any{
			"headers": map[string]any{"key": "value"},
		},
	}
	matched, err := program.EvalBool(activation)
	require.NoError(t, err)
	require.True(t, matched, "expected lookup to match existing key")

	missingProgram, err := env.Compile(`lookup(forward.headers, "missing") == "value"`)
	require.NoError(t, err)
	matched, err = missingProgram.EvalBool(activation)
	require.NoError(t, err)
	require.False(t, matched, "expected lookup to return null for missing key")
}

func TestCompileValue(t *testing.T) {
	env, err := NewEnvironment()
	require.NoError(t, err)

	program, err := env.CompileValue(`forward.headers["key"]`)
	require.NoError(t, err)

	activation := map[string]any{
		"forward": map[string]any{
			"headers": map[string]any{"key": "value"},
		},
	}

	result, err := program.Eval(activation)
	require.NoError(t, err)
	require.Equal(t, "value", result)

	_, err = program.EvalBool(activation)
	require.Error(t, err, "expected EvalBool to fail for non-boolean program")
}

func TestProgramSource(t *testing.T) {
	env, err := NewEnvironment()
	require.NoError(t, err)
	program, err := env.Compile(`  true `)
	require.NoError(t, err)
	require.Equal(t, "true", program.Source())
}
