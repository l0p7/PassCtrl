package expr

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLookupMapValue(t *testing.T) {
	env, err := NewEnvironment()
	require.NoError(t, err)

	activation := map[string]any{
		"forward": map[string]any{
			"headers": map[string]any{"key": "value"},
		},
	}

	tests := []struct {
		name string
		expr string
		want bool
	}{
		{name: "returns true when key found", expr: `lookup(forward.headers, "key") == "value"`, want: true},
		{name: "returns false when key missing", expr: `lookup(forward.headers, "missing") == "value"`, want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			program, err := env.Compile(tc.expr)
			require.NoError(t, err)
			matched, err := program.EvalBool(activation)
			require.NoError(t, err)
			require.Equal(t, tc.want, matched)
		})
	}
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

	t.Run("evaluates value", func(t *testing.T) {
		result, err := program.Eval(activation)
		require.NoError(t, err)
		require.Equal(t, "value", result)
	})

	t.Run("boolean evaluation fails", func(t *testing.T) {
		_, err := program.EvalBool(activation)
		require.Error(t, err, "expected EvalBool to fail for non-boolean program")
	})
}

func TestProgramSource(t *testing.T) {
	env, err := NewEnvironment()
	require.NoError(t, err)

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "trims whitespace", expr: "  true ", want: "true"},
		{name: "preserves expression", expr: "forward.request", want: "forward.request"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			program, err := env.Compile(tc.expr)
			require.NoError(t, err)
			require.Equal(t, tc.want, program.Source())
		})
	}
}
