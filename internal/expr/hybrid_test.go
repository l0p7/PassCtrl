package expr

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/l0p7/passctrl/internal/templates"
	"github.com/stretchr/testify/require"
)

func TestHybridEvaluator_CEL(t *testing.T) {
	// Use nil sandbox for inline templates (no file templates needed)
	renderer := templates.NewRenderer(nil)
	evaluator, err := NewHybridEvaluator(renderer)
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		data       map[string]any
		want       any
	}{
		{
			name:       "string extraction",
			expression: "request.method",
			data: map[string]any{
				"request": map[string]any{
					"method": "GET",
				},
			},
			want: "GET",
		},
		{
			name:       "number extraction",
			expression: "request.status",
			data: map[string]any{
				"request": map[string]any{
					"status": 200,
				},
			},
			want: int64(200),
		},
		{
			name:       "boolean expression",
			expression: "request.method == \"POST\"",
			data: map[string]any{
				"request": map[string]any{
					"method": "POST",
				},
			},
			want: true,
		},
		{
			name:       "header access",
			expression: "request.headers[\"content-type\"]",
			data: map[string]any{
				"request": map[string]any{
					"headers": map[string]string{
						"content-type": "application/json",
					},
				},
			},
			want: "application/json",
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

func TestHybridEvaluator_Template(t *testing.T) {
	// Use nil sandbox for inline templates (no file templates needed)
	renderer := templates.NewRenderer(nil)
	evaluator, err := NewHybridEvaluator(renderer)
	require.NoError(t, err)

	tests := []struct {
		name       string
		expression string
		data       map[string]any
		want       string
	}{
		{
			name:       "simple interpolation",
			expression: "{{ .request.method }}",
			data: map[string]any{
				"request": map[string]any{
					"method": "GET",
				},
			},
			want: "GET",
		},
		{
			name:       "string concatenation",
			expression: "{{ .request.method }} {{ .request.path }}",
			data: map[string]any{
				"request": map[string]any{
					"method": "POST",
					"path":   "/api/users",
				},
			},
			want: "POST /api/users",
		},
		{
			name:       "header access with index",
			expression: "{{ index .request.headers \"content-type\" }}",
			data: map[string]any{
				"request": map[string]any{
					"headers": map[string]string{
						"content-type": "application/json",
					},
				},
			},
			want: "application/json",
		},
		{
			name:       "sprig function - lower",
			expression: "{{ .request.method | lower }}",
			data: map[string]any{
				"request": map[string]any{
					"method": "GET",
				},
			},
			want: "get",
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

func TestHybridEvaluator_Detection(t *testing.T) {
	// Use nil sandbox for inline templates (no file templates needed)
	renderer := templates.NewRenderer(nil)
	evaluator, err := NewHybridEvaluator(renderer)
	require.NoError(t, err)

	data := map[string]any{
		"request": map[string]any{
			"method": "GET",
		},
	}

	// CEL - no {{ brackets
	celResult, err := evaluator.Evaluate("request.method", data)
	require.NoError(t, err)
	require.Equal(t, "GET", celResult)

	// Template - has {{ brackets
	tmplResult, err := evaluator.Evaluate("{{ .request.method }}", data)
	require.NoError(t, err)
	require.Equal(t, "GET", tmplResult)
}

func TestHybridEvaluator_Empty(t *testing.T) {
	// Use nil sandbox for inline templates (no file templates needed)
	renderer := templates.NewRenderer(nil)
	evaluator, err := NewHybridEvaluator(renderer)
	require.NoError(t, err)

	result, err := evaluator.Evaluate("", nil)
	require.NoError(t, err)
	require.Empty(t, result)

	result, err = evaluator.Evaluate("   ", nil)
	require.NoError(t, err)
	require.Empty(t, result)
}

func TestRequestContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/users?token=abc123&page=1", http.NoBody)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "secret")
	req.RemoteAddr = "192.168.1.100:12345"

	ctx := RequestContext(req)

	require.NotNil(t, ctx)
	require.Contains(t, ctx, "request")

	requestData, ok := ctx["request"].(map[string]any)
	require.True(t, ok)

	require.Equal(t, "192.168.1.100:12345", requestData["remoteAddr"])
	require.Equal(t, "POST", requestData["method"])
	require.Equal(t, "/api/users", requestData["path"])

	headers, ok := requestData["headers"].(map[string]string)
	require.True(t, ok)
	require.Equal(t, "application/json", headers["content-type"])
	require.Equal(t, "secret", headers["x-api-key"])

	query, ok := requestData["query"].(map[string]string)
	require.True(t, ok)
	require.Equal(t, "abc123", query["token"])
	require.Equal(t, "1", query["page"])
}

func TestRequestContext_NormalizedHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", http.NoBody)
	req.Header.Set("Content-Type", "text/html")
	req.Header.Set("X-Custom-Header", "value")

	ctx := RequestContext(req)
	requestData := ctx["request"].(map[string]any)
	headers := requestData["headers"].(map[string]string)

	// Headers should be lowercased for consistent access
	require.Equal(t, "text/html", headers["content-type"])
	require.Equal(t, "value", headers["x-custom-header"])
}
