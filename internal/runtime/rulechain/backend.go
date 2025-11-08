package rulechain

import (
	"net/http"
	"net/textproto"
	"net/url"
	"sort"
	"strings"

	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/templates"
)

// BackendDefinition captures the configuration required to contact a backend
// API while evaluating a rule.
type BackendDefinition struct {
	URL                 string
	Method              string
	ForwardProxyHeaders bool
	Headers             backendHeaderMap
	Query               backendHeaderMap
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

// backendHeaderMap stores header/query configuration with null-copy semantics.
// - nil value = copy from raw request
// - non-nil value = template string (may be static or template)
type backendHeaderMap struct {
	entries   map[string]*string
	templates map[string]*templates.Template
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

// SelectHeaders renders headers for the backend request using null-copy semantics:
// - nil value: copy from raw request headers
// - non-nil value: render template (or use static value)
func (b BackendDefinition) SelectHeaders(rawHeaders map[string]string, state *pipeline.State) map[string]string {
	result := make(map[string]string)

	// Process each configured header
	for name, valuePtr := range b.Headers.entries {
		lowerName := strings.ToLower(name)

		if valuePtr == nil {
			// Null-copy: take from raw request if present
			if rawValue, ok := rawHeaders[lowerName]; ok {
				result[lowerName] = rawValue
			}
			// If not present in raw, omit (don't add to result)
		} else {
			// Template or static value
			value := *valuePtr

			// Render template if available
			if tmpl := b.Headers.templates[lowerName]; tmpl != nil && state != nil {
				rendered, err := tmpl.Render(state.TemplateContext())
				if err != nil {
					// On error, use static value
					value = strings.TrimSpace(value)
				} else {
					value = strings.TrimSpace(rendered)
				}
			} else {
				value = strings.TrimSpace(value)
			}

			// Only add if non-empty
			if value != "" {
				result[lowerName] = value
			}
		}
	}

	return result
}

// SelectQuery renders query parameters for the backend request using null-copy semantics.
func (b BackendDefinition) SelectQuery(rawQuery map[string]string, state *pipeline.State) map[string]string {
	result := make(map[string]string)

	// Process each configured query parameter
	for name, valuePtr := range b.Query.entries {
		lowerName := strings.ToLower(name)

		if valuePtr == nil {
			// Null-copy: take from raw request if present
			if rawValue, ok := rawQuery[lowerName]; ok {
				result[lowerName] = rawValue
			}
		} else {
			// Template or static value
			value := *valuePtr

			// Render template if available
			if tmpl := b.Query.templates[lowerName]; tmpl != nil && state != nil {
				rendered, err := tmpl.Render(state.TemplateContext())
				if err != nil {
					// On error, use static value
					value = strings.TrimSpace(value)
				} else {
					value = strings.TrimSpace(rendered)
				}
			} else {
				value = strings.TrimSpace(value)
			}

			// Only add if non-empty
			if value != "" {
				result[lowerName] = value
			}
		}
	}

	return result
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
		Headers:             compileBackendHeaderMap(spec.Headers, renderer),
		Query:               compileBackendHeaderMap(spec.Query, renderer),
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

func compileBackendHeaderMap(cfg map[string]*string, renderer *templates.Renderer) backendHeaderMap {
	if len(cfg) == 0 {
		return backendHeaderMap{
			entries:   make(map[string]*string),
			templates: make(map[string]*templates.Template),
		}
	}

	result := backendHeaderMap{
		entries:   make(map[string]*string, len(cfg)),
		templates: make(map[string]*templates.Template),
	}

	// Sort keys for deterministic ordering
	keys := make([]string, 0, len(cfg))
	for name := range cfg {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	for _, name := range keys {
		valuePtr := cfg[name]
		lowerName := strings.ToLower(strings.TrimSpace(name))

		if lowerName == "" {
			continue
		}

		// Store the entry
		result.entries[lowerName] = valuePtr

		// Compile template if value is non-nil and renderer available
		if valuePtr != nil && renderer != nil {
			value := *valuePtr
			// Only compile if it looks like a template (contains {{)
			if strings.Contains(value, "{{") {
				if tmpl, err := renderer.CompileInline("backend:"+lowerName, value); err == nil {
					result.templates[lowerName] = tmpl
				}
				// Errors during compilation are silent - will fall back to static value
			}
		}
	}

	return result
}

// ApplyHeaders mutates the supplied request with the curated backend headers
// and proxy forwarding metadata.
func (b BackendDefinition) ApplyHeaders(req *http.Request, state *pipeline.State) {
	headers := b.SelectHeaders(state.Request.Headers, state)
	for name, value := range headers {
		if strings.TrimSpace(value) == "" {
			continue
		}
		req.Header.Set(textproto.CanonicalMIMEHeaderKey(name), value)
	}

	// Add sanitized proxy headers if enabled
	if b.ForwardProxyHeaders {
		for name, value := range state.Forward.Headers {
			if name != "forwarded" && !strings.HasPrefix(name, "x-forwarded-") {
				continue
			}
			if strings.TrimSpace(value) == "" {
				continue
			}
			req.Header.Set(textproto.CanonicalMIMEHeaderKey(name), value)
		}
	}
}

// ApplyQuery mutates the supplied request with the curated backend query parameters.
func (b BackendDefinition) ApplyQuery(req *http.Request, state *pipeline.State) {
	queries := b.SelectQuery(state.Request.Query, state)
	if len(queries) == 0 {
		return
	}

	q := req.URL.Query()
	for name, value := range queries {
		if strings.TrimSpace(value) == "" {
			continue
		}
		q.Set(name, value)
	}
	req.URL.RawQuery = q.Encode()
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
			if !strings.Contains(segment, `rel="next"`) {
				continue
			}

			start := strings.Index(segment, "<")
			end := strings.Index(segment, ">")
			if start == -1 || end == -1 || start >= end {
				continue
			}

			nextURL := strings.TrimSpace(segment[start+1 : end])
			if nextURL == "" {
				continue
			}

			ref, err := url.Parse(nextURL)
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
