package forwardpolicy

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/templates"
)

// Config captures the forward request policy toggles and curated allow/strip
// sets for headers and query parameters.
type Config struct {
	ForwardProxyHeaders bool
	Headers             CategoryConfig
	Query               CategoryConfig
}

// CategoryConfig defines allow/strip/custom rules for either headers or query
// parameters.
type CategoryConfig struct {
	Allow  []string
	Strip  []string
	Custom map[string]string
}

type policyRules struct {
	forwardProxyHeaders bool

	headerAllowAll        bool
	headerAllow           map[string]struct{}
	headerStrip           map[string]struct{}
	headerCustom          map[string]string
	headerCustomTemplates map[string]*templates.Template

	queryAllowAll        bool
	queryAllow           map[string]struct{}
	queryStrip           map[string]struct{}
	queryCustom          map[string]string
	queryCustomTemplates map[string]*templates.Template
}

// Agent curates the inbound request metadata to expose a minimal forward view
// for downstream rule evaluation and backend calls.
type Agent struct {
	policy policyRules
	logger *slog.Logger
}

// New constructs an Agent with the supplied configuration.
// If renderer is provided, custom header/query values will be treated as templates.
func New(cfg Config, renderer *templates.Renderer, logger *slog.Logger) (*Agent, error) {
	rules, err := compile(cfg, renderer)
	if err != nil {
		return nil, err
	}
	return &Agent{policy: rules, logger: logger}, nil
}

// DefaultConfig returns the baseline forward request policy that keeps
// admission signals and the rule evaluation toggles available to later
// agents.
func DefaultConfig() Config {
	return Config{
		Headers: CategoryConfig{
			Allow: []string{"authorization", "x-passctrl-deny"},
		},
		Query: CategoryConfig{
			Allow: []string{"allow", "deny", "error"},
		},
	}
}

func compile(cfg Config, renderer *templates.Renderer) (policyRules, error) {
	rules := policyRules{
		forwardProxyHeaders:   cfg.ForwardProxyHeaders,
		headerAllow:           make(map[string]struct{}),
		headerStrip:           make(map[string]struct{}),
		headerCustom:          make(map[string]string),
		headerCustomTemplates: make(map[string]*templates.Template),
		queryAllow:            make(map[string]struct{}),
		queryStrip:            make(map[string]struct{}),
		queryCustom:           make(map[string]string),
		queryCustomTemplates:  make(map[string]*templates.Template),
	}

	for _, name := range cfg.Headers.Allow {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if trimmed == "*" {
			rules.headerAllowAll = true
			continue
		}
		rules.headerAllow[strings.ToLower(trimmed)] = struct{}{}
	}
	for _, name := range cfg.Headers.Strip {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		rules.headerStrip[strings.ToLower(trimmed)] = struct{}{}
	}
	if len(cfg.Headers.Custom) > 0 {
		keys := make([]string, 0, len(cfg.Headers.Custom))
		for name := range cfg.Headers.Custom {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			key := strings.TrimSpace(name)
			if key == "" {
				continue
			}
			value := cfg.Headers.Custom[name]
			lower := strings.ToLower(key)
			rules.headerCustom[lower] = value

			// Compile template if renderer is available
			if renderer != nil {
				tmpl, err := renderer.CompileInline("forward:header:"+lower, value)
				if err != nil {
					return policyRules{}, err
				}
				rules.headerCustomTemplates[lower] = tmpl
			}
		}
	}

	for _, name := range cfg.Query.Allow {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if trimmed == "*" {
			rules.queryAllowAll = true
			continue
		}
		rules.queryAllow[strings.ToLower(trimmed)] = struct{}{}
	}
	for _, name := range cfg.Query.Strip {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		rules.queryStrip[strings.ToLower(trimmed)] = struct{}{}
	}
	if len(cfg.Query.Custom) > 0 {
		keys := make([]string, 0, len(cfg.Query.Custom))
		for name := range cfg.Query.Custom {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			key := strings.TrimSpace(name)
			if key == "" {
				continue
			}
			value := cfg.Query.Custom[name]
			lower := strings.ToLower(key)
			rules.queryCustom[lower] = value

			// Compile template if renderer is available
			if renderer != nil {
				tmpl, err := renderer.CompileInline("forward:query:"+lower, value)
				if err != nil {
					return policyRules{}, err
				}
				rules.queryCustomTemplates[lower] = tmpl
			}
		}
	}

	return rules, nil
}

// Name identifies the forward request policy agent for logging and result
// snapshots.
func (a *Agent) Name() string { return "forward_request_policy" }

// Execute filters request metadata down to the fields that downstream rule
// evaluation is allowed to observe.
func (a *Agent) Execute(_ context.Context, r *http.Request, state *pipeline.State) pipeline.Result {
	state.Forward.Headers = make(map[string]string)
	state.Forward.Query = make(map[string]string)

	incomingHeaders := captureRequestHeaders(r)
	incomingQuery := captureRequestQuery(r)

	headers := a.selectHeaders(incomingHeaders, state)
	queries := a.selectQueryParams(incomingQuery, state)

	state.Forward.Headers = headers
	state.Forward.Query = queries

	return pipeline.Result{
		Name:   a.Name(),
		Status: "curated",
		Meta: map[string]any{
			"headers": state.Forward.Headers,
			"query":   state.Forward.Query,
		},
	}
}

func (a *Agent) selectHeaders(incoming map[string]string, state *pipeline.State) map[string]string {
	selected := make(map[string]string)
	if a.policy.headerAllowAll {
		for name, value := range incoming {
			selected[name] = value
		}
	} else {
		for name := range a.policy.headerAllow {
			if value, ok := incoming[name]; ok {
				selected[name] = value
			}
		}
	}

	if a.policy.forwardProxyHeaders {
		for name, value := range incoming {
			if name != "forwarded" && !strings.HasPrefix(name, "x-forwarded-") {
				continue
			}
			if strings.TrimSpace(value) == "" {
				continue
			}
			selected[name] = value
		}
	}

	for name := range a.policy.headerStrip {
		delete(selected, name)
	}

	for name, value := range a.policy.headerCustom {
		// Render template if available
		if tmpl := a.policy.headerCustomTemplates[name]; tmpl != nil {
			rendered, err := tmpl.Render(state.TemplateContext())
			if err != nil {
				// Log error but continue with static value
				if a.logger != nil {
					a.logger.Warn("forward policy header template render failed",
						slog.String("header", name),
						slog.String("error", err.Error()))
				}
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

func (a *Agent) selectQueryParams(incoming map[string]string, state *pipeline.State) map[string]string {
	selected := make(map[string]string)
	if a.policy.queryAllowAll {
		for name, value := range incoming {
			selected[name] = value
		}
	} else {
		for name := range a.policy.queryAllow {
			if value, ok := incoming[name]; ok {
				selected[name] = value
			}
		}
	}

	for name := range a.policy.queryStrip {
		delete(selected, name)
	}

	for name, value := range a.policy.queryCustom {
		// Render template if available
		if tmpl := a.policy.queryCustomTemplates[name]; tmpl != nil {
			rendered, err := tmpl.Render(state.TemplateContext())
			if err != nil {
				// Log error but continue with static value
				if a.logger != nil {
					a.logger.Warn("forward policy query template render failed",
						slog.String("query", name),
						slog.String("error", err.Error()))
				}
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

func captureRequestHeaders(r *http.Request) map[string]string {
	headers := make(map[string]string)
	for name, values := range r.Header {
		if len(values) == 0 {
			continue
		}
		headers[strings.ToLower(name)] = values[0]
	}
	return headers
}

func captureRequestQuery(r *http.Request) map[string]string {
	query := make(map[string]string)
	if r.URL == nil {
		return query
	}
	for name, values := range r.URL.Query() {
		if len(values) == 0 {
			continue
		}
		query[strings.ToLower(name)] = values[0]
	}
	return query
}
