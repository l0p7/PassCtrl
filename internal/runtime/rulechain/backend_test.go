package rulechain

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/l0p7/passctrl/internal/runtime/forwardpolicy"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/stretchr/testify/require"
)

func TestBuildBackendDefinitionAndSelection(t *testing.T) {
	spec := BackendDefinitionSpec{
		URL:                 " https://api.example.com/resource ",
		Method:              "post",
		ForwardProxyHeaders: true,
		Headers: forwardpolicy.CategoryConfig{
			Allow:  []string{"X-Auth", "X-Remove", "  "},
			Strip:  []string{"X-Remove", "  X-Ignore  "},
			Custom: map[string]string{"X-Custom": " override ", "X-Remove": ""},
		},
		Query: forwardpolicy.CategoryConfig{
			Allow:  []string{"limit", "page"},
			Strip:  []string{"page"},
			Custom: map[string]string{"limit": " 100 "},
		},
		Body:     "{\"status\":\"ok\"}",
		BodyFile: "  payload.json  ",
		Accepted: nil,
		Pagination: BackendPaginationSpec{
			Type:     " LINK ",
			MaxPages: 0,
		},
	}

	backend := buildBackendDefinition(spec)

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
	})

	require.Len(t, headers, 2)
	require.Equal(t, "token", headers["x-auth"])
	require.Equal(t, "override", headers["x-custom"])
	_, ok := headers["x-remove"]
	require.False(t, ok)

	queries := backend.SelectQuery(map[string]string{
		"limit": "10",
		"page":  "3",
	})

	require.Equal(t, "100", queries["limit"])
	_, ok = queries["page"]
	require.False(t, ok)

	req := httptest.NewRequest(http.MethodGet, "https://api.example.com/resource?remove=1", http.NoBody)
	state := &pipeline.State{}
	state.Forward.Headers = map[string]string{
		"x-auth":   "token",
		"x-remove": "to-be-removed",
		"x-custom": "ignore",
	}
	state.Forward.Query = map[string]string{
		"limit": "10",
		"page":  "3",
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
