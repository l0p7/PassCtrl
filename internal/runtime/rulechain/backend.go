package rulechain

import (
	"net/http"
	"net/textproto"
	"net/url"
	"sort"
	"strings"

	"github.com/l0p7/passctrl/internal/runtime/forwardpolicy"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/templates"
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
	// BodyTemplate, when present, is rendered against the pipeline state and
	// used as the request body (takes precedence over Body/BodyFile literals).
	BodyTemplate *templates.Template
	Accepted     []int
	accepted     map[int]struct{}
	pagination   BackendPagination
}

// BackendPagination details how pagination should be performed when querying a
// backend API.
type BackendPagination struct {
	Type     string
	MaxPages int
}

type backendForwardRules struct {
	allowAll        bool
	allow           map[string]struct{}
	strip           map[string]struct{}
	custom          map[string]string
	customTemplates map[string]*templates.Template
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
// If templates are available, they will be rendered using the provided state.
func (b BackendDefinition) SelectHeaders(incoming map[string]string, state *pipeline.State) map[string]string {
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
		// Render template if available
		if tmpl := b.Headers.customTemplates[name]; tmpl != nil && state != nil {
			rendered, err := tmpl.Render(state.TemplateContext())
			if err != nil {
				// On error, fall back to static value
				value = strings.TrimSpace(value)
			} else {
				value = strings.TrimSpace(rendered)
			}
		} else {
			value = strings.TrimSpace(value)
		}

		if value == "" {
			delete(selected, name)
			continue
		}
		selected[name] = value
	}
	return selected
}

// SelectQuery curates the query parameters that should be forwarded to the
// backend. If templates are available, they will be rendered using the provided state.
func (b BackendDefinition) SelectQuery(incoming map[string]string, state *pipeline.State) map[string]string {
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
		// Render template if available
		if tmpl := b.Query.customTemplates[name]; tmpl != nil && state != nil {
			rendered, err := tmpl.Render(state.TemplateContext())
			if err != nil {
				// On error, fall back to static value
				value = strings.TrimSpace(value)
			} else {
				value = strings.TrimSpace(rendered)
			}
		} else {
			value = strings.TrimSpace(value)
		}

		if value == "" {
			delete(selected, name)
			continue
		}
		selected[name] = value
	}
	return selected
}

func buildBackendDefinition(spec BackendDefinitionSpec, renderer *templates.Renderer) BackendDefinition {
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
		Headers:             compileBackendForwardRules(spec.Headers, renderer),
		Query:               compileBackendForwardRules(spec.Query, renderer),
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

func compileBackendForwardRules(cfg forwardpolicy.CategoryConfig, renderer *templates.Renderer) backendForwardRules {
	rules := backendForwardRules{
		allow:           make(map[string]struct{}),
		strip:           make(map[string]struct{}),
		custom:          make(map[string]string),
		customTemplates: make(map[string]*templates.Template),
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
	if len(cfg.Custom) > 0 {
		keys := make([]string, 0, len(cfg.Custom))
		for name := range cfg.Custom {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			key := strings.TrimSpace(name)
			if key == "" {
				continue
			}
			value := cfg.Custom[name]
			lower := strings.ToLower(key)
			rules.custom[lower] = value

			// Compile template if renderer is available
			if renderer != nil {
				// Note: errors during compilation are logged but don't fail the build
				// Templates will fall back to static values if compilation fails
				if tmpl, err := renderer.CompileInline("backend:"+lower, value); err == nil {
					rules.customTemplates[lower] = tmpl
				}
			}
		}
	}
	return rules
}

// ApplyHeaders mutates the supplied request with the curated backend headers
// and proxy forwarding metadata.
func (b BackendDefinition) ApplyHeaders(req *http.Request, state *pipeline.State) {
	headers := b.SelectHeaders(state.Forward.Headers, state)
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
	selected := b.SelectQuery(state.Forward.Query, state)
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
