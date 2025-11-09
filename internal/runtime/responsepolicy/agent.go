package responsepolicy

import (
	"context"
	"net/http"
	"sort"
	"strings"

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
	Headers  map[string]*string
}

// compiledCategory stores compiled templates and header directives.
type compiledCategory struct {
	status  int
	body    *templates.Template
	headers map[string]*string
	// Precompiled header templates keyed by header name.
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
		out := compiledCategory{
			status:          cat.Status,
			headers:         cat.Headers,
			headerTemplates: make(map[string]*templates.Template),
		}

		if cfg.Renderer != nil {
			if strings.TrimSpace(cat.Body) != "" {
				tmpl, _ := cfg.Renderer.CompileInline(cfg.Endpoint+":"+name+":body", cat.Body)
				out.body = tmpl
			} else if strings.TrimSpace(cat.BodyFile) != "" {
				tmpl, _ := cfg.Renderer.CompileFile(cat.BodyFile)
				out.body = tmpl
			}

			// Precompile header templates (non-nil values that contain {{)
			if len(cat.Headers) > 0 {
				keys := make([]string, 0, len(cat.Headers))
				for k := range cat.Headers {
					keys = append(keys, k)
				}
				sort.Strings(keys)

				for _, k := range keys {
					valuePtr := cat.Headers[k]
					if valuePtr != nil && strings.Contains(*valuePtr, "{{") {
						tmpl, _ := cfg.Renderer.CompileInline(cfg.Endpoint+":"+name+":header:"+k, *valuePtr)
						out.headerTemplates[k] = tmpl
					}
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

// Name identifies this agent in pipeline traces.
func (a *Agent) Name() string { return "response_policy" }

// Execute builds and sends the HTTP response.
func (a *Agent) Execute(_ context.Context, r *http.Request, state *pipeline.State) pipeline.Result {
	// If response is already populated (cached), skip processing
	if state.Response.Status > 0 {
		return pipeline.Result{
			Name:   a.Name(),
			Status: "cached",
			Meta: map[string]any{
				"status": state.Response.Status,
			},
		}
	}

	outcome := strings.ToLower(state.Rule.Outcome)

	cat := a.categoryFor(outcome)

	// Determine status
	status := http.StatusOK
	switch outcome {
	case "fail":
		status = http.StatusForbidden
	case "error":
		status = http.StatusBadGateway
	case "":
		status = http.StatusInternalServerError
	}
	if cat.status > 0 {
		status = cat.status
	}

	// Render body if template available
	var message string
	if cat.body != nil {
		rendered, err := cat.body.Render(state.TemplateContext())
		if err == nil {
			message = rendered
		}
	}

	// Render headers with null-copy semantics
	headers := make(map[string]string)
	for name, valuePtr := range cat.headers {
		lowerName := strings.ToLower(name)

		if valuePtr == nil {
			// Null-copy from raw request
			if rawValue, ok := state.Request.Headers[lowerName]; ok {
				headers[lowerName] = rawValue
			}
		} else {
			// Render template or use static value
			value := *valuePtr

			if tmpl := cat.headerTemplates[name]; tmpl != nil {
				rendered, err := tmpl.Render(state.TemplateContext())
				if err == nil {
					value = strings.TrimSpace(rendered)
				} else {
					value = strings.TrimSpace(value)
				}
			} else {
				value = strings.TrimSpace(value)
			}

			if value != "" {
				headers[lowerName] = value
			}
		}
	}

	// Add X-PassCtrl-Outcome header if outcome is present
	if outcome != "" {
		headers["X-PassCtrl-Outcome"] = outcome
	}

	state.Response.Status = status
	state.Response.Message = message
	state.Response.Headers = headers

	return pipeline.Result{
		Name:   a.Name(),
		Status: "ready",
		Meta: map[string]any{
			"outcome": outcome,
			"status":  status,
		},
	}
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
