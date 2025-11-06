package rulechain

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/templates"
	"github.com/stretchr/testify/require"
)

// strPtr returns a pointer to a string literal
func strPtr(s string) *string {
	return &s
}

func TestBuildBackendDefinitionAndSelection(t *testing.T) {
	spec := BackendDefinitionSpec{
		URL:                 " https://api.example.com/resource ",
		Method:              "post",
		ForwardProxyHeaders: true,
		Headers: map[string]*string{
			"X-Auth":   nil,                  // copy from raw request (null-copy)
			"X-Custom": strPtr(" override "), // custom static value
			// X-Remove and X-Ignore are omitted (not forwarded)
		},
		Query: map[string]*string{
			"limit": strPtr(" 100 "), // custom static value
			// page is omitted (not forwarded)
		},
		Body:     "{\"status\":\"ok\"}",
		BodyFile: "  payload.json  ",
		Accepted: nil,
		Pagination: BackendPaginationSpec{
			Type:     " LINK ",
			MaxPages: 0,
		},
	}

	backend := buildBackendDefinition(spec, nil)

	require.Equal(t, "https://api.example.com/resource", backend.URL)
	require.Equal(t, http.MethodPost, backend.Method)
	require.Equal(t, "payload.json", backend.BodyFile)
	require.True(t, backend.Accepts(http.StatusOK))
	require.False(t, backend.Accepts(http.StatusTeapot))
	require.Equal(t, "link", backend.Pagination().Type)
	require.Equal(t, 1, backend.Pagination().MaxPages)

	headers := backend.SelectHeaders(map[string]string{
		"x-auth":   "token",
		"x-remove": "to-be-removed",
		"x-ignore": "value",
	}, nil)

	require.Len(t, headers, 2)
	require.Equal(t, "token", headers["x-auth"])
	require.Equal(t, "override", headers["x-custom"])
	_, ok := headers["x-remove"]
	require.False(t, ok)

	queries := backend.SelectQuery(map[string]string{
		"limit": "10",
		"page":  "3",
	}, nil)

	require.Equal(t, "100", queries["limit"])
	_, ok = queries["page"]
	require.False(t, ok)

	req := httptest.NewRequest(http.MethodGet, "https://api.example.com/resource?remove=1", http.NoBody)
	state := &pipeline.State{}
	state.Raw.Headers = map[string]string{
		"x-auth":   "token",
		"x-remove": "to-be-removed",
		"x-custom": "ignore",
	}
	state.Raw.Query = map[string]string{
		"limit": "10",
		"page":  "3",
	}
	state.Forward.Headers = map[string]string{
		"x-forwarded-for": "1.1.1.1",
		"forwarded":       "for=1.1.1.1",
	}
	state.Admission.ForwardedFor = "1.1.1.1"
	state.Admission.Forwarded = "for=1.1.1.1"

	backend.ApplyHeaders(req, state)
	backend.ApplyQuery(req, state)

	require.Equal(t, "token", req.Header.Get("X-Auth"))
	require.Equal(t, "override", req.Header.Get("X-Custom"))
	require.Equal(t, "1.1.1.1", req.Header.Get("X-Forwarded-For"))
	require.Equal(t, "for=1.1.1.1", req.Header.Get("Forwarded"))
	require.Equal(t, "100", req.URL.Query().Get("limit"))
	require.False(t, req.URL.Query().Has("page"))
}

func TestNextLinkFromHeader(t *testing.T) {
	absolute := []string{"<https://api.example.com/page/2>; rel=\"next\""}
	require.Equal(t, "https://api.example.com/page/2", NextLinkFromHeader(absolute, nil))

	base, err := url.Parse("https://api.example.com/resource?page=1")
	require.NoError(t, err)

	mixed := []string{"</page/2>; rel=\"next\"", "<https://api.example.com/page/3>; rel=\"prev\""}
	require.Equal(t, "https://api.example.com/page/2", NextLinkFromHeader(mixed, base))

	junk := []string{"invalid", "<https://api.example.com/page/4>; rel=\"prev\""}
	require.Empty(t, NextLinkFromHeader(junk, base))
}

func TestBackendDefinitionIsConfigured(t *testing.T) {
	var backend BackendDefinition
	require.False(t, backend.IsConfigured())
	backend.URL = "https://api.example.com"
	require.True(t, backend.IsConfigured())
}

func TestBackendDefinitionWithTemplates(t *testing.T) {
	renderer := templates.NewRenderer(nil)

	spec := BackendDefinitionSpec{
		URL:    "https://api.example.com",
		Method: "POST",
		Headers: map[string]*string{
			"Authorization": strPtr(`Bearer {{ index .raw.Headers "authorization" | replace "Basic " "" }}`),
			"X-Trace-ID":    strPtr(`{{ index .raw.Headers "x-request-id" }}`),
		},
		Query: map[string]*string{
			"token": strPtr(`{{ index .raw.Headers "authorization" }}`),
			"page":  strPtr(`{{ index .raw.Query "offset" | default "1" }}`),
		},
	}

	backend := buildBackendDefinition(spec, renderer)

	// Create a state with raw request data
	req := httptest.NewRequest(http.MethodPost, "https://example.com?offset=5", http.NoBody)
	req.Header.Set("Authorization", "Basic user123")
	req.Header.Set("X-Request-ID", "trace-abc")
	state := pipeline.NewState(req, "test", "test|key", "")

	// Test template rendering for headers
	headers := backend.SelectHeaders(map[string]string{}, state)
	require.Equal(t, "Bearer user123", headers["authorization"])
	require.Equal(t, "trace-abc", headers["x-trace-id"])

	// Test template rendering for query parameters
	query := backend.SelectQuery(map[string]string{}, state)
	require.Equal(t, "Basic user123", query["token"])
	require.Equal(t, "5", query["page"])
}

func TestBackendDefinitionTemplatesFallbackOnError(t *testing.T) {
	renderer := templates.NewRenderer(nil)

	spec := BackendDefinitionSpec{
		URL: "https://api.example.com",
		Headers: map[string]*string{
			"X-Static": strPtr("fallback-value"),
		},
	}

	backend := buildBackendDefinition(spec, renderer)

	req := httptest.NewRequest(http.MethodGet, "https://example.com", http.NoBody)
	state := pipeline.NewState(req, "test", "test|key", "")

	headers := backend.SelectHeaders(map[string]string{}, state)
	// Should use static value when no template is present
	require.Equal(t, "fallback-value", headers["x-static"])
}

func TestBackendDefinitionTemplatesHandleEmptyResults(t *testing.T) {
	renderer := templates.NewRenderer(nil)

	spec := BackendDefinitionSpec{
		URL: "https://api.example.com",
		Headers: map[string]*string{
			"X-Missing": strPtr(`{{ index .raw.Headers "non-existent" }}`),
		},
		Query: map[string]*string{
			"missing": strPtr(`{{ index .raw.Query "non-existent" }}`),
		},
	}

	backend := buildBackendDefinition(spec, renderer)

	req := httptest.NewRequest(http.MethodGet, "https://example.com", http.NoBody)
	state := pipeline.NewState(req, "test", "test|key", "")

	headers := backend.SelectHeaders(map[string]string{}, state)
	// Empty template results should be stripped
	require.NotContains(t, headers, "x-missing")

	query := backend.SelectQuery(map[string]string{}, state)
	// Empty template results should be stripped
	require.NotContains(t, query, "missing")
}
