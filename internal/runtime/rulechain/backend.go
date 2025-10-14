package rulechain

import (
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"github.com/l0p7/passctrl/internal/runtime/forwardpolicy"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
)

// BackendDefinition captures the configuration required to contact a backend
// API while evaluating a rule.
type BackendDefinition struct {
	URL                 string
	Method              string
	ForwardProxyHeaders bool
	Headers             backendForwardRules
	Query               backendForwardRules
	Body                string
	BodyFile            string
	Accepted            []int
	accepted            map[int]struct{}
	pagination          BackendPagination
}

// BackendPagination details how pagination should be performed when querying a
// backend API.
type BackendPagination struct {
	Type     string
	MaxPages int
}

type backendForwardRules struct {
	allowAll bool
	allow    map[string]struct{}
	strip    map[string]struct{}
	custom   map[string]string
}

// Pagination returns the pagination configuration for the backend.
func (b BackendDefinition) Pagination() BackendPagination { return b.pagination }

// IsConfigured reports whether the backend definition contains a target URL.
func (b BackendDefinition) IsConfigured() bool { return b.URL != "" }

// Accepts reports whether a backend response status should be treated as
// successful.
func (b BackendDefinition) Accepts(status int) bool {
	if len(b.accepted) == 0 {
		return true
	}
	_, ok := b.accepted[status]
	return ok
}

// SelectHeaders curates the headers that should be forwarded to the backend.
func (b BackendDefinition) SelectHeaders(incoming map[string]string) map[string]string {
	selected := make(map[string]string)
	if b.Headers.allowAll {
		for name, value := range incoming {
			selected[name] = value
		}
	} else {
		for name := range b.Headers.allow {
			if value, ok := incoming[name]; ok {
				selected[name] = value
			}
		}
	}
	for name := range b.Headers.strip {
		delete(selected, name)
	}
	for name, value := range b.Headers.custom {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			delete(selected, name)
			continue
		}
		selected[name] = trimmed
	}
	return selected
}

// SelectQuery curates the query parameters that should be forwarded to the
// backend.
func (b BackendDefinition) SelectQuery(incoming map[string]string) map[string]string {
	selected := make(map[string]string)
	if b.Query.allowAll {
		for name, value := range incoming {
			selected[name] = value
		}
	} else {
		for name := range b.Query.allow {
			if value, ok := incoming[name]; ok {
				selected[name] = value
			}
		}
	}
	for name := range b.Query.strip {
		delete(selected, name)
	}
	for name, value := range b.Query.custom {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			delete(selected, name)
			continue
		}
		selected[name] = trimmed
	}
	return selected
}

func buildBackendDefinition(spec BackendDefinitionSpec) BackendDefinition {
	url := strings.TrimSpace(spec.URL)
	if url == "" {
		return BackendDefinition{}
	}

	method := strings.ToUpper(strings.TrimSpace(spec.Method))
	if method == "" {
		method = http.MethodGet
	}

	accepted := spec.Accepted
	if len(accepted) == 0 {
		accepted = []int{http.StatusOK}
	}

	acceptedSet := make(map[int]struct{}, len(accepted))
	for _, status := range accepted {
		acceptedSet[status] = struct{}{}
	}

	paginationType := strings.ToLower(strings.TrimSpace(spec.Pagination.Type))
	maxPages := spec.Pagination.MaxPages
	if maxPages <= 0 {
		maxPages = 1
	}

	return BackendDefinition{
		URL:                 url,
		Method:              method,
		ForwardProxyHeaders: spec.ForwardProxyHeaders,
		Headers:             compileBackendForwardRules(spec.Headers),
		Query:               compileBackendForwardRules(spec.Query),
		Body:                spec.Body,
		BodyFile:            strings.TrimSpace(spec.BodyFile),
		Accepted:            accepted,
		accepted:            acceptedSet,
		pagination: BackendPagination{
			Type:     paginationType,
			MaxPages: maxPages,
		},
	}
}

func compileBackendForwardRules(cfg forwardpolicy.CategoryConfig) backendForwardRules {
	rules := backendForwardRules{
		allow:  make(map[string]struct{}),
		strip:  make(map[string]struct{}),
		custom: make(map[string]string),
	}

	for _, name := range cfg.Allow {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if trimmed == "*" {
			rules.allowAll = true
			continue
		}
		rules.allow[strings.ToLower(trimmed)] = struct{}{}
	}
	for _, name := range cfg.Strip {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		rules.strip[strings.ToLower(trimmed)] = struct{}{}
	}
	for name, value := range cfg.Custom {
		key := strings.TrimSpace(name)
		if key == "" {
			continue
		}
		rules.custom[strings.ToLower(key)] = value
	}
	return rules
}

// ApplyHeaders mutates the supplied request with the curated backend headers
// and proxy forwarding metadata.
func (b BackendDefinition) ApplyHeaders(req *http.Request, state *pipeline.State) {
	headers := b.SelectHeaders(state.Forward.Headers)
	for name, value := range headers {
		if strings.TrimSpace(value) == "" {
			continue
		}
		req.Header.Set(textproto.CanonicalMIMEHeaderKey(name), value)
	}
	if b.ForwardProxyHeaders {
		if state.Admission.ForwardedFor != "" {
			req.Header.Set("X-Forwarded-For", state.Admission.ForwardedFor)
		}
		if state.Admission.Forwarded != "" {
			req.Header.Set("Forwarded", state.Admission.Forwarded)
		}
	}
}

// ApplyQuery mutates the supplied request URL with the curated backend query
// parameters.
func (b BackendDefinition) ApplyQuery(req *http.Request, state *pipeline.State) {
	selected := b.SelectQuery(state.Forward.Query)
	values := req.URL.Query()
	for name := range b.Query.strip {
		values.Del(name)
	}
	for name, value := range selected {
		values.Set(name, value)
	}
	req.URL.RawQuery = values.Encode()
}

// NextLinkFromHeader parses the RFC5988 Link header for a `rel="next"`
// reference. When a relative path is returned the supplied base URL is used to
// expand it into an absolute reference.
func NextLinkFromHeader(values []string, base *url.URL) string {
	for _, headerVal := range values {
		parts := strings.Split(headerVal, ",")
		for _, part := range parts {
			segment := strings.TrimSpace(part)
			if segment == "" {
				continue
			}
			sections := strings.Split(segment, ";")
			if len(sections) == 0 {
				continue
			}
			target := strings.TrimSpace(sections[0])
			if !strings.HasPrefix(target, "<") || !strings.Contains(target, ">") {
				continue
			}
			href := strings.Trim(target, "<>")
			var isNext bool
			for _, attr := range sections[1:] {
				kv := strings.SplitN(strings.TrimSpace(attr), "=", 2)
				if len(kv) != 2 {
					continue
				}
				key := strings.ToLower(strings.TrimSpace(kv[0]))
				value := strings.Trim(strings.TrimSpace(kv[1]), `"`)
				if key == "rel" && strings.EqualFold(value, "next") {
					isNext = true
					break
				}
			}
			if !isNext {
				continue
			}
			ref, err := url.Parse(href)
			if err != nil {
				continue
			}
			if ref.IsAbs() {
				return ref.String()
			}
			if base == nil {
				return ref.String()
			}
			return base.ResolveReference(ref).String()
		}
	}
	return ""
}
