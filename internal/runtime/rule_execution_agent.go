package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/l0p7/passctrl/internal/expr"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/runtime/rulechain"
	"github.com/l0p7/passctrl/internal/templates"
)

type ruleExecutionAgent struct {
    client *http.Client
    logger *slog.Logger
    renderer *templates.Renderer
}

func newRuleExecutionAgent(client *http.Client, logger *slog.Logger, renderer *templates.Renderer) *ruleExecutionAgent {
    if client == nil {
        client = &http.Client{Timeout: 10 * time.Second}
    }
    return &ruleExecutionAgent{client: client, logger: logger, renderer: renderer}
}

func (a *ruleExecutionAgent) Name() string { return "rule_execution" }

// Execute performs the simulated rule evaluation unless a cache hit or
// previous agent disabled the live execution path.
func (a *ruleExecutionAgent) Execute(ctx context.Context, _ *http.Request, state *pipeline.State) pipeline.Result {
	if state.Rule.FromCache || !state.Rule.ShouldExecute {
		return pipeline.Result{
			Name:    a.Name(),
			Status:  "skipped",
			Details: "no live rule evaluation required",
		}
	}

	plan, _ := state.Plan().(rulechain.ExecutionPlan)
	if len(plan.Rules) == 0 {
		state.Rule.Executed = false
		state.Rule.Outcome = "pass"
		state.Rule.Reason = "no rules defined"
		state.Rule.History = nil
		return pipeline.Result{
			Name:    a.Name(),
			Status:  state.Rule.Outcome,
			Details: state.Rule.Reason,
		}
	}

	history := make([]pipeline.RuleHistoryEntry, 0, len(plan.Rules))
	var finalOutcome string
	var finalReason string

	for _, def := range plan.Rules {
		start := time.Now()
		outcome, reason := a.evaluateRule(ctx, def, state)
		history = append(history, pipeline.RuleHistoryEntry{
			Name:     def.Name,
			Outcome:  outcome,
			Reason:   reason,
			Duration: time.Since(start),
		})

		finalOutcome = outcome
		finalReason = reason

		if outcome != "pass" {
			break
		}
	}

	state.Rule.Executed = len(history) > 0
	state.Rule.History = history
	state.Rule.Outcome = finalOutcome
	state.Rule.Reason = finalReason
	state.ClearPlan()

	outcome := finalOutcome
	if outcome == "" {
		outcome = "error"
	}

	return pipeline.Result{
		Name:    a.Name(),
		Status:  outcome,
		Details: finalReason,
		Meta: map[string]any{
			"executedRules": len(history),
		},
	}
}

func (a *ruleExecutionAgent) evaluateRule(ctx context.Context, def rulechain.Definition, state *pipeline.State) (string, string) {
	resetBackendState(&state.Backend)

	if def.Backend.IsConfigured() {
		if err := a.invokeBackend(ctx, def.Backend, state); err != nil {
			state.Backend.Error = err.Error()
			reason := a.ruleMessage(def.ErrorTemplate, def.ErrorMessage, fmt.Sprintf("backend request failed: %v", err), state)
			return "error", reason
		}
	} else {
		state.Backend.Accepted = true
	}

	activation := buildActivation(state)

	if matched, source, err := evaluateProgramList(def.Conditions.Error, activation, false); err != nil {
		return "error", fmt.Sprintf("error condition %s evaluation failed: %v", source, err)
	} else if matched {
		reason := a.ruleMessage(def.ErrorTemplate, def.ErrorMessage, fmt.Sprintf("error condition matched: %s", source), state)
		return "error", reason
	}

	if matched, source, err := evaluateProgramList(def.Conditions.Fail, activation, false); err != nil {
		return "error", fmt.Sprintf("fail condition %s evaluation failed: %v", source, err)
	} else if matched {
		reason := a.ruleMessage(def.FailTemplate, def.FailMessage, fmt.Sprintf("fail condition matched: %s", source), state)
		return "fail", reason
	}

	if matched, source, err := evaluateProgramList(def.Conditions.Pass, activation, true); err != nil {
		return "error", fmt.Sprintf("pass condition %s evaluation failed: %v", source, err)
	} else if matched {
		reason := a.ruleMessage(def.PassTemplate, def.PassMessage, fmt.Sprintf("pass conditions satisfied: %s", source), state)
		return "pass", reason
	}

	if len(def.Conditions.Pass) > 0 {
		return "fail", a.ruleMessage(def.FailTemplate, def.FailMessage, "required pass condition not satisfied", state)
	}

	if def.Backend.IsConfigured() && !state.Backend.Accepted {
		fallback := "backend response not accepted"
		if state.Backend.Status != 0 {
			fallback = fmt.Sprintf("backend response not accepted: status %d", state.Backend.Status)
		}
		return "fail", a.ruleMessage(def.FailTemplate, def.FailMessage, fallback, state)
	}

	return "pass", a.ruleMessage(def.PassTemplate, def.PassMessage, "rule evaluated without explicit outcome", state)
}

func (a *ruleExecutionAgent) ruleMessage(tmpl *templates.Template, message, fallback string, state *pipeline.State) string {
	trimmed := strings.TrimSpace(message)
	if tmpl != nil {
		rendered, err := tmpl.Render(state.TemplateContext())
		if err != nil {
			logger := a.logger
			if logger == nil {
				logger = slog.Default()
			}
			logger = logger.With(slog.String("agent", a.Name()))
			if state != nil {
				if state.Endpoint != "" {
					logger = logger.With(slog.String("endpoint", state.Endpoint))
				}
				if state.CorrelationID != "" {
					logger = logger.With(slog.String("correlation_id", state.CorrelationID))
				}
			}
			logger.Warn("rule message template rendering failed", slog.Any("error", err))
		} else if candidate := strings.TrimSpace(rendered); candidate != "" {
			return candidate
		}
	}
	if trimmed != "" {
		return trimmed
	}
	return fallback
}

func (a *ruleExecutionAgent) invokeBackend(ctx context.Context, backend rulechain.BackendDefinition, state *pipeline.State) error {
	if a.client == nil {
		return errors.New("rule execution agent: http client missing")
	}

	method := backend.Method
	if strings.TrimSpace(method) == "" {
		method = http.MethodGet
	}

	pagination := backend.Pagination()
	maxPages := pagination.MaxPages
	if maxPages <= 0 {
		maxPages = 1
	}

	nextURL := backend.URL
	visited := make(map[string]struct{})
	pages := make([]pipeline.BackendPageState, 0, maxPages)

	for page := 0; page < maxPages; page++ {
		trimmed := strings.TrimSpace(nextURL)
		if trimmed == "" {
			break
		}
		if _, seen := visited[trimmed]; seen {
			break
		}
		visited[trimmed] = struct{}{}

		parsed, err := url.Parse(trimmed)
		if err != nil {
			return fmt.Errorf("backend request url: %w", err)
		}

    var body io.Reader
    var bodyText string
    if backend.BodyTemplate != nil {
        rendered, err := backend.BodyTemplate.Render(state.TemplateContext())
        if err != nil {
            return fmt.Errorf("backend body render: %w", err)
        }

        // If the rendered string looks like a file path and a renderer is
        // available, treat it as a template file reference; otherwise use the
        // rendered string as the body contents.
		content := rendered
		trimmedRendered := strings.TrimSpace(rendered)
		if trimmedRendered != "" && a.renderer != nil {
			if fileTmpl, err := a.renderer.CompileFile(trimmedRendered); err == nil {
				output, err := fileTmpl.Render(state.TemplateContext())
				if err != nil {
					return fmt.Errorf("backend body file render: %w", err)
				}
				content = output
			}
		}

        bodyText = content
        body = strings.NewReader(content)
    } else if strings.TrimSpace(backend.Body) != "" {
        bodyText = backend.Body
        body = strings.NewReader(backend.Body)
    }

		req, err := http.NewRequestWithContext(ctx, method, parsed.String(), body)
		if err != nil {
			return fmt.Errorf("backend request build: %w", err)
		}
    if body != nil {
        snap := bodyText
        req.GetBody = func() (io.ReadCloser, error) {
            return io.NopCloser(strings.NewReader(snap)), nil
        }
    }

		backend.ApplyHeaders(req, state)
		backend.ApplyQuery(req, state)

		resp, err := a.client.Do(req)
		if err != nil {
			return fmt.Errorf("backend request: %w", err)
		}

		pageState := pipeline.BackendPageState{
			URL:      req.URL.String(),
			Status:   resp.StatusCode,
			Headers:  captureResponseHeaders(resp.Header),
			Accepted: backend.Accepts(resp.StatusCode),
		}

		bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		closeErr := resp.Body.Close()
		if err != nil {
			return fmt.Errorf("backend read: %w", err)
		}
		if closeErr != nil {
			return fmt.Errorf("backend close: %w", closeErr)
		}

		contentType := strings.ToLower(resp.Header.Get("Content-Type"))
		if strings.Contains(contentType, "json") && len(bodyBytes) > 0 {
			decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
			decoder.UseNumber()
			var payload any
			if err := decoder.Decode(&payload); err != nil {
				return fmt.Errorf("backend json decode: %w", err)
			}
			pageState.Body = normalizeJSONNumbers(payload)
			pageState.BodyText = string(bodyBytes)
		} else {
			pageState.Body = nil
			pageState.BodyText = string(bodyBytes)
		}

		if pageState.Accepted {
			state.Backend.Accepted = true
		}

		pages = append(pages, pageState)

		if pagination.Type != "link-header" {
			break
		}
		nextURL = rulechain.NextLinkFromHeader(resp.Header.Values("Link"), req.URL)
		if nextURL == "" {
			break
		}
	}

	if len(pages) == 0 {
		return nil
	}

	state.Backend.Requested = true
	state.Backend.Pages = pages
	last := pages[len(pages)-1]
	state.Backend.Status = last.Status
	state.Backend.Headers = cloneHeaders(last.Headers)
	state.Backend.Body = last.Body
	state.Backend.BodyText = last.BodyText
	state.Backend.Accepted = last.Accepted
	return nil
}

func evaluateProgramList(programs []expr.Program, activation map[string]any, requireAll bool) (bool, string, error) {
	if requireAll {
		if len(programs) == 0 {
			return false, "", nil
		}

		sources := make([]string, 0, len(programs))
		for _, program := range programs {
			result, err := program.EvalBool(activation)
			if err != nil {
				return false, program.Source(), err
			}
			if !result {
				return false, "", nil
			}
			sources = append(sources, program.Source())
		}

		return true, strings.Join(sources, " && "), nil
	}

	for _, program := range programs {
		result, err := program.EvalBool(activation)
		if err != nil {
			return false, program.Source(), err
		}
		if result {
			return true, program.Source(), nil
		}
	}
	return false, "", nil
}

func buildActivation(state *pipeline.State) map[string]any {
	activation := map[string]any{
		"raw": map[string]any{
			"method":  state.Raw.Method,
			"path":    state.Raw.Path,
			"host":    state.Raw.Host,
			"headers": toAnyMap(state.Raw.Headers),
			"query":   toAnyMap(state.Raw.Query),
		},
		"admission": map[string]any{
			"authenticated": state.Admission.Authenticated,
			"reason":        state.Admission.Reason,
			"clientIp":      state.Admission.ClientIP,
			"trustedProxy":  state.Admission.TrustedProxy,
			"proxyStripped": state.Admission.ProxyStripped,
			"forwardedFor":  state.Admission.ForwardedFor,
			"forwarded":     state.Admission.Forwarded,
			"decision":      state.Admission.Decision,
		},
		"forward": map[string]any{
			"headers": toAnyMap(state.Forward.Headers),
			"query":   toAnyMap(state.Forward.Query),
		},
		"backend": map[string]any{
			"requested": state.Backend.Requested,
			"status":    state.Backend.Status,
			"headers":   toAnyMap(state.Backend.Headers),
			"body":      state.Backend.Body,
			"bodyText":  state.Backend.BodyText,
			"error":     state.Backend.Error,
			"accepted":  state.Backend.Accepted,
			"pages":     backendPagesActivation(state.Backend.Pages),
		},
		"vars": map[string]any{},
		"now":  time.Now().UTC(),
	}
	return activation
}

func backendPagesActivation(pages []pipeline.BackendPageState) []map[string]any {
	if len(pages) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(pages))
	for _, page := range pages {
		out = append(out, map[string]any{
			"url":      page.URL,
			"status":   page.Status,
			"headers":  toAnyMap(page.Headers),
			"body":     page.Body,
			"bodyText": page.BodyText,
			"accepted": page.Accepted,
		})
	}
	return out
}

func toAnyMap(in map[string]string) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func resetBackendState(state *pipeline.BackendState) {
	state.Requested = false
	state.Status = 0
	state.Body = nil
	state.BodyText = ""
	state.Error = ""
	state.Accepted = false
	state.Pages = nil
	if state.Headers == nil {
		state.Headers = make(map[string]string)
	} else {
		for k := range state.Headers {
			delete(state.Headers, k)
		}
	}
}

func captureResponseHeaders(header http.Header) map[string]string {
	headers := make(map[string]string)
	for name, values := range header {
		if len(values) == 0 {
			continue
		}
		headers[strings.ToLower(name)] = values[0]
	}
	return headers
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

func normalizeJSONNumbers(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[k] = normalizeJSONNumbers(val)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = normalizeJSONNumbers(val)
		}
		return out
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
		if f, err := v.Float64(); err == nil {
			return f
		}
		return v.String()
	default:
		return v
	}
}
