package rulechain

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/l0p7/passctrl/internal/runtime/forwardpolicy"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
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

	if backend.URL != "https://api.example.com/resource" {
		t.Fatalf("expected URL to be trimmed, got %q", backend.URL)
	}

	if backend.Method != http.MethodPost {
		t.Fatalf("expected method to be POST, got %s", backend.Method)
	}

	if backend.BodyFile != "payload.json" {
		t.Fatalf("expected body file to be trimmed, got %q", backend.BodyFile)
	}

	if !backend.Accepts(http.StatusOK) {
		t.Fatalf("expected default 200 status to be accepted")
	}

	if backend.Accepts(http.StatusTeapot) {
		t.Fatalf("unexpected status accepted")
	}

	if backend.Pagination().Type != "link" || backend.Pagination().MaxPages != 1 {
		t.Fatalf("unexpected pagination settings: %#v", backend.Pagination())
	}

	headers := backend.SelectHeaders(map[string]string{
		"x-auth":   "token",
		"x-remove": "to-be-removed",
		"x-ignore": "value",
	})

	if len(headers) != 2 {
		t.Fatalf("expected curated headers to include auth and custom override, got %#v", headers)
	}

	if headers["x-auth"] != "token" {
		t.Fatalf("expected auth header to be retained")
	}

	if headers["x-custom"] != "override" {
		t.Fatalf("expected custom header to override value, got %q", headers["x-custom"])
	}

	if _, ok := headers["x-remove"]; ok {
		t.Fatalf("expected stripped header to be removed")
	}

	queries := backend.SelectQuery(map[string]string{
		"limit": "10",
		"page":  "3",
	})

	if queries["limit"] != "100" {
		t.Fatalf("expected custom limit to override provided value, got %q", queries["limit"])
	}

	if _, ok := queries["page"]; ok {
		t.Fatalf("expected stripped query parameter to be removed")
	}

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

	if got := req.Header.Get("X-Auth"); got != "token" {
		t.Fatalf("expected header to be applied with canonical casing, got %q", got)
	}

	if got := req.Header.Get("X-Custom"); got != "override" {
		t.Fatalf("expected custom header to be applied, got %q", got)
	}

	if req.Header.Get("X-Forwarded-For") != "1.1.1.1" || req.Header.Get("Forwarded") != "for=1.1.1.1" {
		t.Fatalf("expected proxy headers to be forwarded")
	}

	if values := req.URL.Query(); values.Get("limit") != "100" {
		t.Fatalf("expected query parameter to be applied, got %q", values.Get("limit"))
	}

	if req.URL.Query().Has("page") {
		t.Fatalf("expected stripped query parameter to be removed from request")
	}
}

func TestNextLinkFromHeader(t *testing.T) {
	absolute := []string{"<https://api.example.com/page/2>; rel=\"next\""}
	if got := NextLinkFromHeader(absolute, nil); got != "https://api.example.com/page/2" {
		t.Fatalf("expected absolute link to be returned, got %q", got)
	}

	base, err := url.Parse("https://api.example.com/resource?page=1")
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}

	mixed := []string{"</page/2>; rel=\"next\"", "<https://api.example.com/page/3>; rel=\"prev\""}
	if got := NextLinkFromHeader(mixed, base); got != "https://api.example.com/page/2" {
		t.Fatalf("expected relative link to be resolved against base, got %q", got)
	}

	junk := []string{"invalid", "<https://api.example.com/page/4>; rel=\"prev\""}
	if got := NextLinkFromHeader(junk, base); got != "" {
		t.Fatalf("expected no link for junk header values, got %q", got)
	}
}
