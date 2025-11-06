package forwardpolicy

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/l0p7/passctrl/internal/runtime/pipeline"
)

// Config captures the forward request policy toggles.
type Config struct {
	ForwardProxyHeaders bool
}

// CategoryConfig is deprecated and kept for backward compatibility during migration.
// New code should use map[string]*string directly.
type CategoryConfig struct {
	Allow  []string
	Strip  []string
	Custom map[string]string
}

type policyRules struct {
	forwardProxyHeaders bool
}

// Agent sanitizes proxy headers for downstream consumption.
type Agent struct {
	policy policyRules
	logger *slog.Logger
}

// New constructs an Agent with the supplied configuration.
func New(cfg Config, logger *slog.Logger) (*Agent, error) {
	rules := policyRules{
		forwardProxyHeaders: cfg.ForwardProxyHeaders,
	}
	return &Agent{policy: rules, logger: logger}, nil
}

// DefaultConfig returns the baseline forward request policy.
func DefaultConfig() Config {
	return Config{
		ForwardProxyHeaders: false,
	}
}

// Name identifies the forward request policy agent for logging and result
// snapshots.
func (a *Agent) Name() string { return "forward_request_policy" }

// Execute sanitizes proxy headers when enabled.
// Header and query forwarding is now handled by backend-specific template maps.
func (a *Agent) Execute(_ context.Context, r *http.Request, state *pipeline.State) pipeline.Result {
	// Initialize empty forward state
	state.Forward.Headers = make(map[string]string)
	state.Forward.Query = make(map[string]string)

	// Populate sanitized proxy headers if enabled
	if a.policy.forwardProxyHeaders {
		incomingHeaders := captureRequestHeaders(r)
		for name, value := range incomingHeaders {
			if name != "forwarded" && !strings.HasPrefix(name, "x-forwarded-") {
				continue
			}
			if strings.TrimSpace(value) == "" {
				continue
			}
			state.Forward.Headers[name] = value
		}
	}

	return pipeline.Result{
		Name:   a.Name(),
		Status: "ready",
		Meta: map[string]any{
			"forwardProxyHeaders": a.policy.forwardProxyHeaders,
			"proxyHeaderCount":    len(state.Forward.Headers),
		},
	}
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
