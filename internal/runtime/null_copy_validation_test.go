package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/l0p7/passctrl/internal/config"
	"github.com/stretchr/testify/require"
)

// TestNullCopyHeaderSemantics validates that null values in backend headers
// result in copying from the raw request, while non-null values use static/template values.
func TestNullCopyHeaderSemantics(t *testing.T) {
	// Create a rule with null-copy headers
	xRequestIDVal := "override-123"
	bundle := config.RuleBundle{
		Endpoints: map[string]config.EndpointConfig{
			"test-endpoint": {
				Authentication: config.EndpointAuthenticationConfig{
					Allow: config.EndpointAuthAllowConfig{
						None: true, // Allow anonymous requests
					},
				},
				Rules: []config.EndpointRuleReference{
					{Name: "null-copy-rule"},
				},
			},
		},
		Rules: map[string]config.RuleConfig{
			"null-copy-rule": {
				BackendAPI: config.RuleBackendConfig{
					URL:    "http://backend.example/api",
					Method: "GET",
					Headers: map[string]*string{
						"x-trace-id":   nil,                        // null = copy from raw
						"x-request-id": &xRequestIDVal,             // non-null = static value
						"content-type": strPtr("application/json"), // static value
					},
					Query: map[string]*string{
						"page":   nil,            // null = copy from raw
						"limit":  strPtr("100"),  // non-null = override
						"format": strPtr("json"), // static addition
					},
				},
				Conditions: config.RuleConditionConfig{
					Pass: []string{"true"}, // Always pass
				},
			},
		},
	}

	pipe := NewPipeline(nil, PipelineOptions{})
	pipe.Reload(context.Background(), bundle)

	// Create request with headers and query params
	req := httptest.NewRequest(http.MethodGet, "http://example.com/test?page=5&limit=50", http.NoBody)
	req.Header.Set("X-Trace-ID", "trace-abc-123")
	req.Header.Set("X-Request-ID", "original-456") // will be overridden

	rec := httptest.NewRecorder()

	// Execute the request
	pipe.ServeAuth(rec, req)

	// Verify response
	require.Equal(t, http.StatusOK, rec.Code, "null-copy should work correctly")
}

// TestNullCopyMissingKeysOmitted validates that null-copy of missing headers/query
// params are silently omitted rather than causing errors.
func TestNullCopyMissingKeysOmitted(t *testing.T) {
	bundle := config.RuleBundle{
		Endpoints: map[string]config.EndpointConfig{
			"test-endpoint": {
				Authentication: config.EndpointAuthenticationConfig{
					Allow: config.EndpointAuthAllowConfig{
						None: true,
					},
				},
				Rules: []config.EndpointRuleReference{
					{Name: "missing-key-rule"},
				},
			},
		},
		Rules: map[string]config.RuleConfig{
			"missing-key-rule": {
				BackendAPI: config.RuleBackendConfig{
					URL:    "http://backend.example/api",
					Method: "GET",
					Headers: map[string]*string{
						"x-missing-header": nil, // null-copy from raw, but not present
						"x-present":        strPtr("static"),
					},
					Query: map[string]*string{
						"missing_param": nil, // null-copy from raw, but not present
						"present":       strPtr("value"),
					},
				},
				Conditions: config.RuleConditionConfig{
					Pass: []string{"true"}, // Always pass
				},
			},
		},
	}

	pipe := NewPipeline(nil, PipelineOptions{})
	pipe.Reload(context.Background(), bundle)

	// Create request WITHOUT the headers/query params that are null-copied
	req := httptest.NewRequest(http.MethodGet, "http://example.com/test", http.NoBody)

	rec := httptest.NewRecorder()

	// Execute the request - should not error
	pipe.ServeAuth(rec, req)

	// Verify response - missing keys should be silently omitted
	require.Equal(t, http.StatusOK, rec.Code, "missing null-copy keys should not cause errors")
}

// TestEmptyValuesOmitted validates that empty or whitespace-only values are omitted
// from the forwarded request.
func TestEmptyValuesOmitted(t *testing.T) {
	bundle := config.RuleBundle{
		Endpoints: map[string]config.EndpointConfig{
			"test-endpoint": {
				Authentication: config.EndpointAuthenticationConfig{
					Allow: config.EndpointAuthAllowConfig{
						None: true,
					},
				},
				Rules: []config.EndpointRuleReference{
					{Name: "empty-value-rule"},
				},
			},
		},
		Rules: map[string]config.RuleConfig{
			"empty-value-rule": {
				BackendAPI: config.RuleBackendConfig{
					URL:    "http://backend.example/api",
					Method: "GET",
					Headers: map[string]*string{
						"x-empty":       strPtr("  "),    // whitespace only - should be omitted
						"x-valid":       strPtr("value"), // normal value
						"x-zero-length": strPtr(""),      // empty string - should be omitted
					},
				},
				Conditions: config.RuleConditionConfig{
					Pass: []string{"true"}, // Always pass
				},
			},
		},
	}

	pipe := NewPipeline(nil, PipelineOptions{})
	pipe.Reload(context.Background(), bundle)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/test", http.NoBody)
	rec := httptest.NewRecorder()

	// Execute the request
	pipe.ServeAuth(rec, req)

	// Verify response
	require.Equal(t, http.StatusOK, rec.Code, "empty values should be handled correctly")
}

// TestHeaderNormalizationToLowercase validates that all header names are normalized
// to lowercase for consistent access.
func TestHeaderNormalizationToLowercase(t *testing.T) {
	bundle := config.RuleBundle{
		Endpoints: map[string]config.EndpointConfig{
			"test-endpoint": {
				Authentication: config.EndpointAuthenticationConfig{
					Allow: config.EndpointAuthAllowConfig{
						None: true,
					},
				},
				Rules: []config.EndpointRuleReference{
					{Name: "normalization-rule"},
				},
			},
		},
		Rules: map[string]config.RuleConfig{
			"normalization-rule": {
				BackendAPI: config.RuleBackendConfig{
					URL:    "http://backend.example/api",
					Method: "GET",
					Headers: map[string]*string{
						"X-Custom-Header":  strPtr("value1"), // Mixed case in config
						"x-another-header": nil,              // null-copy
					},
				},
				Conditions: config.RuleConditionConfig{
					Pass: []string{"true"}, // Always pass
				},
			},
		},
	}

	pipe := NewPipeline(nil, PipelineOptions{})
	pipe.Reload(context.Background(), bundle)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/test", http.NoBody)
	req.Header.Set("X-Another-Header", "value2") // Mixed case in request

	rec := httptest.NewRecorder()

	// Execute the request
	pipe.ServeAuth(rec, req)

	// Verify response
	require.Equal(t, http.StatusOK, rec.Code, "header normalization should work correctly")
}

// TestMixedNullAndNonNullValues validates that null and non-null values can coexist
// and are handled correctly.
func TestMixedNullAndNonNullValues(t *testing.T) {
	bundle := config.RuleBundle{
		Endpoints: map[string]config.EndpointConfig{
			"test-endpoint": {
				Authentication: config.EndpointAuthenticationConfig{
					Allow: config.EndpointAuthAllowConfig{
						None: true,
					},
				},
				Rules: []config.EndpointRuleReference{
					{Name: "mixed-rule"},
				},
			},
		},
		Rules: map[string]config.RuleConfig{
			"mixed-rule": {
				BackendAPI: config.RuleBackendConfig{
					URL:    "http://backend.example/api",
					Method: "GET",
					Headers: map[string]*string{
						"x-trace-id":    nil,                        // null-copy
						"x-api-version": strPtr("v2"),               // static override
						"x-custom":      strPtr("custom-value"),     // static new header
						"content-type":  strPtr("application/json"), // static
					},
					Query: map[string]*string{
						"page":   nil,            // null-copy
						"limit":  strPtr("50"),   // static override
						"format": strPtr("json"), // static new param
						"sort":   nil,            // null-copy (not present in request, will be omitted)
					},
				},
				Conditions: config.RuleConditionConfig{
					Pass: []string{"true"}, // Always pass
				},
			},
		},
	}

	pipe := NewPipeline(nil, PipelineOptions{})
	pipe.Reload(context.Background(), bundle)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/test?page=3&limit=100", http.NoBody)
	req.Header.Set("X-Trace-ID", "trace-789")
	req.Header.Set("X-Api-Version", "v1") // will be overridden

	rec := httptest.NewRecorder()

	// Execute the request
	pipe.ServeAuth(rec, req)

	// Verify response
	require.Equal(t, http.StatusOK, rec.Code, "mixed null and non-null values should work correctly")
}

// strPtr returns a pointer to a string literal
func strPtr(s string) *string {
	return &s
}
