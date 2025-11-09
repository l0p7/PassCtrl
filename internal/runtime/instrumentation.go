package runtime

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/l0p7/passctrl/internal/runtime/pipeline"
)

type instrumentedAgent struct {
	inner  pipeline.Agent
	logger *slog.Logger
}

func (a *instrumentedAgent) Name() string { return a.inner.Name() }

func (a *instrumentedAgent) Execute(ctx context.Context, r *http.Request, state *pipeline.State) pipeline.Result {
	start := time.Now()
	result := a.inner.Execute(ctx, r, state)
	duration := time.Since(start)

	attrs := []slog.Attr{
		slog.String("status", result.Status),
		slog.Float64("latency_ms", float64(duration)/float64(time.Millisecond)),
	}

	outcome := result.Status
	if state != nil && state.Rule.Outcome != "" {
		outcome = state.Rule.Outcome
	}
	attrs = append(attrs, slog.String("outcome", outcome))

	if state != nil {
		if state.Endpoint != "" {
			attrs = append(attrs, slog.String("endpoint", state.Endpoint))
		}
		if state.CorrelationID != "" {
			attrs = append(attrs, slog.String("correlation_id", state.CorrelationID))
		}
	}

	if result.Details != "" {
		attrs = append(attrs, slog.String("details", result.Details))
	}
	if len(result.Meta) > 0 {
		attrs = append(attrs, slog.Any("meta", result.Meta))
	}

	a.logger.LogAttrs(ctx, slog.LevelInfo, "agent executed", attrs...)
	return result
}

func (p *Pipeline) instrumentAgents(endpoint string, agents []pipeline.Agent) []pipeline.Agent {
	if len(agents) == 0 {
		return nil
	}
	wrapped := make([]pipeline.Agent, 0, len(agents))
	for _, ag := range agents {
		if ag == nil {
			continue
		}
		logger := p.logger.With(
			slog.String("component", "runtime"),
			slog.String("agent", ag.Name()),
			slog.String("endpoint", endpoint),
		)
		wrapped = append(wrapped, &instrumentedAgent{inner: ag, logger: logger})
	}
	return wrapped
}
