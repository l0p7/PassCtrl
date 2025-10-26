package expr

import (
	"testing"

	"github.com/l0p7/passctrl/internal/templates"
	"github.com/stretchr/testify/require"
)

func TestNewRuleHybridEvaluator(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	evaluator, err := NewRuleHybridEvaluator(renderer)
	require.NoError(t, err)
	require.NotNil(t, evaluator)
}

func TestRuleHybridEvaluator_CEL_BackendAccess(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	evaluator, err := NewRuleHybridEvaluator(renderer)
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		data       map[string]any
		want       any
	}{
		{
			name:       "backend body field",
			expression: "backend.body.userId",
			data: map[string]any{
				"backend": map[string]any{
					"body": map[string]any{
						"userId": "123",
					},
				},
			},
			want: "123",
		},
		{
			name:       "backend status",
			expression: "backend.status",
			data: map[string]any{
				"backend": map[string]any{
					"status": int64(200),
				},
			},
			want: int64(200),
		},
		{
			name:       "backend header",
			expression: "backend.headers[\"content-type\"]",
			data: map[string]any{
				"backend": map[string]any{
					"headers": map[string]any{
						"content-type": "application/json",
					},
				},
			},
			want: "application/json",
		},
		{
			name:       "boolean expression",
			expression: "backend.body.status == \"active\"",
			data: map[string]any{
				"backend": map[string]any{
					"body": map[string]any{
						"status": "active",
					},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := evaluator.Evaluate(tt.expression, tt.data)
			require.NoError(t, err)
			require.Equal(t, tt.want, result)
		})
	}
}

func TestRuleHybridEvaluator_CEL_AuthAccess(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	evaluator, err := NewRuleHybridEvaluator(renderer)
	require.NoError(t, err)

	data := map[string]any{
		"auth": map[string]any{
			"input": map[string]any{
				"token": "abc123",
				"user":  "john",
			},
		},
	}

	result, err := evaluator.Evaluate("auth.input.token", data)
	require.NoError(t, err)
	require.Equal(t, "abc123", result)

	result, err = evaluator.Evaluate("auth.input.user", data)
	require.NoError(t, err)
	require.Equal(t, "john", result)
}

func TestRuleHybridEvaluator_CEL_VarsAccess(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	evaluator, err := NewRuleHybridEvaluator(renderer)
	require.NoError(t, err)

	data := map[string]any{
		"vars": map[string]any{
			"api_base": "https://api.internal",
			"tenant":   "acme-corp",
		},
	}

	result, err := evaluator.Evaluate("vars.api_base", data)
	require.NoError(t, err)
	require.Equal(t, "https://api.internal", result)

	result, err = evaluator.Evaluate("vars.tenant", data)
	require.NoError(t, err)
	require.Equal(t, "acme-corp", result)
}

func TestRuleHybridEvaluator_CEL_VariablesAccess(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	evaluator, err := NewRuleHybridEvaluator(renderer)
	require.NoError(t, err)

	// Test accessing already-evaluated local variables
	data := map[string]any{
		"variables": map[string]any{
			"user_id":   "123",
			"is_active": true,
		},
	}

	result, err := evaluator.Evaluate("variables.user_id", data)
	require.NoError(t, err)
	require.Equal(t, "123", result)

	result, err = evaluator.Evaluate("variables.is_active", data)
	require.NoError(t, err)
	require.Equal(t, true, result)
}

func TestRuleHybridEvaluator_Template_BackendAccess(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	evaluator, err := NewRuleHybridEvaluator(renderer)
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		data       map[string]any
		want       string
	}{
		{
			name:       "backend body interpolation",
			expression: "{{ .backend.body.firstName }} {{ .backend.body.lastName }}",
			data: map[string]any{
				"backend": map[string]any{
					"body": map[string]any{
						"firstName": "John",
						"lastName":  "Doe",
					},
				},
			},
			want: "John Doe",
		},
		{
			name:       "backend status string",
			expression: "Status: {{ .backend.status }}",
			data: map[string]any{
				"backend": map[string]any{
					"status": 200,
				},
			},
			want: "Status: 200",
		},
		{
			name:       "sprig function with backend",
			expression: "{{ .backend.body.email | lower }}",
			data: map[string]any{
				"backend": map[string]any{
					"body": map[string]any{
						"email": "JOHN@EXAMPLE.COM",
					},
				},
			},
			want: "john@example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := evaluator.Evaluate(tt.expression, tt.data)
			require.NoError(t, err)
			require.Equal(t, tt.want, result)
		})
	}
}

func TestRuleHybridEvaluator_Template_MultiContext(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	evaluator, err := NewRuleHybridEvaluator(renderer)
	require.NoError(t, err)

	data := map[string]any{
		"backend": map[string]any{
			"body": map[string]any{
				"userId": "123",
			},
		},
		"vars": map[string]any{
			"api_base": "https://api.internal",
		},
	}

	result, err := evaluator.Evaluate("{{ .vars.api_base }}/users/{{ .backend.body.userId }}", data)
	require.NoError(t, err)
	require.Equal(t, "https://api.internal/users/123", result)
}

func TestRuleHybridEvaluator_MixedEvaluation(t *testing.T) {
	renderer := templates.NewRenderer(nil)
	evaluator, err := NewRuleHybridEvaluator(renderer)
	require.NoError(t, err)

	data := map[string]any{
		"backend": map[string]any{
			"body": map[string]any{
				"userId": "123",
				"status": "active",
			},
		},
	}

	// CEL evaluation
	celResult, err := evaluator.Evaluate("backend.body.userId", data)
	require.NoError(t, err)
	require.Equal(t, "123", celResult)

	// Template evaluation
	tmplResult, err := evaluator.Evaluate("User: {{ .backend.body.userId }}", data)
	require.NoError(t, err)
	require.Equal(t, "User: 123", tmplResult)

	// CEL boolean
	boolResult, err := evaluator.Evaluate("backend.body.status == \"active\"", data)
	require.NoError(t, err)
	require.Equal(t, true, boolResult)
}
