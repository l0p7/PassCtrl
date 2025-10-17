package responsepolicy

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/l0p7/passctrl/internal/runtime/forwardpolicy"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/templates"
)

// Config controls endpoint-level response policy (status, headers, body templates).
type Config struct {
	Endpoint string
	Renderer *templates.Renderer
	Pass     CategoryConfig
	Fail     CategoryConfig
	Error    CategoryConfig
}

// CategoryConfig describes overrides for a single outcome category.
type CategoryConfig struct {
	Status   int
	Body     string
	BodyFile string
	Headers  forwardpolicy.CategoryConfig
}

// compiledCategory stores compiled templates and header directives.
type compiledCategory struct {
	status  int
	body    *templates.Template
	headers forwardpolicy.CategoryConfig
	// Precompiled custom header templates keyed by header name.
	headerTemplates map[string]*templates.Template
}

// Agent materializes the HTTP response from the rule outcome.
type Agent struct {
	pass  compiledCategory
	fail  compiledCategory
	error compiledCategory
}

// New constructs a response policy agent with default behavior.
func New() *Agent { return &Agent{} }

// NewWithConfig constructs a response policy agent using endpoint configuration.
func NewWithConfig(cfg Config) *Agent {
	compile := func(name string, cat CategoryConfig) compiledCategory {
		out := compiledCategory{status: cat.Status, headers: cat.Headers, headerTemplates: map[string]*templates.Template{}}
		if cfg.Renderer != nil {
			if strings.TrimSpace(cat.Body) != "" {
				tmpl, _ := cfg.Renderer.CompileInline(cfg.Endpoint+":"+name+":body", cat.Body)
				out.body = tmpl
			} else if strings.TrimSpace(cat.BodyFile) != "" {
				tmpl, _ := cfg.Renderer.CompileFile(cat.BodyFile)
				out.body = tmpl
			}
			// Precompile custom header values
			if len(cat.Headers.Custom) > 0 {
				// deterministic order for stable naming
				keys := make([]string, 0, len(cat.Headers.Custom))
				for k := range cat.Headers.Custom {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					v := cat.Headers.Custom[k]
					tmpl, _ := cfg.Renderer.CompileInline(cfg.Endpoint+":"+name+":header:"+k, v)
					out.headerTemplates[k] = tmpl
				}
			}
		}
		return out
	}
	return &Agent{
		pass:  compile("pass", cfg.Pass),
		fail:  compile("fail", cfg.Fail),
		error: compile("error", cfg.Error),
	}
}

// Name identifies the response policy agent for logging and snapshots.
func (a *Agent) Name() string { return "response_policy" }

// Execute materializes the HTTP response structure from the rule outcome,
// ensuring a status code and headers are ready for the client.
func (a *Agent) Execute(_ context.Context, _ *http.Request, state *pipeline.State) pipeline.Result {
	if state.Response.Status != 0 {
		return pipeline.Result{Name: a.Name(), Status: "cached", Details: "response replayed from cache"}
	}

	// Default statuses
	switch state.Rule.Outcome {
	case "pass":
		state.Response.Status = http.StatusOK
	case "fail":
		state.Response.Status = http.StatusForbidden
	case "error":
		state.Response.Status = http.StatusBadGateway
	default:
		state.Response.Status = http.StatusInternalServerError
	}

	// Apply endpoint overrides and headers
	cat := a.categoryFor(state.Rule.Outcome)
	if cat.status > 0 {
		state.Response.Status = cat.status
	}
	if state.Response.Headers == nil {
		state.Response.Headers = make(map[string]string)
	}
	// Start from current headers; apply allow/strip
	if len(cat.headers.Allow) > 0 {
		allowed := make(map[string]string, len(cat.headers.Allow))
		for _, k := range cat.headers.Allow {
			key := strings.ToLower(strings.TrimSpace(k))
			if key == "*" {
				allowed = cloneHeaders(state.Response.Headers)
				break
			}
			if v, ok := state.Response.Headers[key]; ok {
				allowed[key] = v
			}
		}
		state.Response.Headers = allowed
	}
	if len(cat.headers.Strip) > 0 {
		for _, k := range cat.headers.Strip {
			delete(state.Response.Headers, strings.ToLower(strings.TrimSpace(k)))
		}
	}
	// Apply custom headers (templated when renderer provided)
	if len(cat.headers.Custom) > 0 {
		for name := range cat.headers.Custom {
			key := strings.TrimSpace(name)
			tmpl := cat.headerTemplates[name]
			var value string
			if tmpl != nil {
				rendered, err := tmpl.Render(state.TemplateContext())
				if err == nil {
					value = strings.TrimSpace(rendered)
				}
			}
			if value == "" {
				value = strings.TrimSpace(cat.headers.Custom[name])
			}
			if value != "" {
				state.Response.Headers[key] = value
			}
		}
	}
	state.Response.Headers["X-PassCtrl-Outcome"] = state.Rule.Outcome

	// Render body from endpoint templates when present
	if cat.body != nil {
		if rendered, err := cat.body.Render(state.TemplateContext()); err == nil {
			state.Response.Message = strings.TrimSpace(rendered)
		}
	}

	return pipeline.Result{Name: a.Name(), Status: "rendered"}
}

func (a *Agent) categoryFor(outcome string) compiledCategory {
	switch outcome {
	case "pass":
		return a.pass
	case "fail":
		return a.fail
	case "error":
		return a.error
	default:
		return compiledCategory{}
	}
}

func cloneHeaders(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
