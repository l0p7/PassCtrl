package forwardpolicy

import (
	"context"
	"net/http"
	"strings"

	"github.com/l0p7/passctrl/internal/runtime/pipeline"
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

	headerAllowAll bool
	headerAllow    map[string]struct{}
	headerStrip    map[string]struct{}
	headerCustom   map[string]string

	queryAllowAll bool
	queryAllow    map[string]struct{}
	queryStrip    map[string]struct{}
	queryCustom   map[string]string
}

// Agent curates the inbound request metadata to expose a minimal forward view
// for downstream rule evaluation and backend calls.
type Agent struct {
	policy policyRules
}

// New constructs an Agent with the supplied configuration.
func New(cfg Config) *Agent {
	return &Agent{policy: compile(cfg)}
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

func compile(cfg Config) policyRules {
	rules := policyRules{
		forwardProxyHeaders: cfg.ForwardProxyHeaders,
		headerAllow:         make(map[string]struct{}),
		headerStrip:         make(map[string]struct{}),
		headerCustom:        make(map[string]string),
		queryAllow:          make(map[string]struct{}),
		queryStrip:          make(map[string]struct{}),
		queryCustom:         make(map[string]string),
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
	for name, value := range cfg.Headers.Custom {
		key := strings.TrimSpace(name)
		if key == "" {
			continue
		}
		rules.headerCustom[strings.ToLower(key)] = value
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
	for name, value := range cfg.Query.Custom {
		key := strings.TrimSpace(name)
		if key == "" {
			continue
		}
		rules.queryCustom[strings.ToLower(key)] = value
	}

	return rules
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

	headers := a.selectHeaders(incomingHeaders)
	queries := a.selectQueryParams(incomingQuery)

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

func (a *Agent) selectHeaders(incoming map[string]string) map[string]string {
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
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			delete(selected, name)
			continue
		}
		selected[name] = trimmed
	}
	return selected
}

func (a *Agent) selectQueryParams(incoming map[string]string) map[string]string {
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
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			delete(selected, name)
			continue
		}
		selected[name] = trimmed
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
