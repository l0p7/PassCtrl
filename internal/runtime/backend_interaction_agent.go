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

	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/runtime/rulechain"
)

// backendInteractionAgent executes HTTP requests to backend APIs with pagination support.
// It is responsible purely for HTTP execution and response capture, without any template
// rendering, credential matching, condition evaluation, or caching logic.
type backendInteractionAgent struct {
	client httpDoer
	logger *slog.Logger
}

// newBackendInteractionAgent creates a new backend interaction agent with the given HTTP client and logger.
func newBackendInteractionAgent(client httpDoer, logger *slog.Logger) *backendInteractionAgent {
	return &backendInteractionAgent{
		client: client,
		logger: logger,
	}
}

// Execute executes a pre-rendered backend request and handles pagination.
// The rendered parameter contains all template-rendered values (URL, headers, body, etc.).
// Populates state.Backend.* with responses and errors.
// Returns error only for fatal issues (nil state, context cancellation).
// Non-fatal errors (network, timeout, parse) are stored in state.Backend.Error.
func (a *backendInteractionAgent) Execute(ctx context.Context, rendered renderedBackendRequest, backend rulechain.BackendDefinition, state *pipeline.State) error {
	if a.client == nil {
		return errors.New("backend interaction agent: http client missing")
	}

	// Build contextual logger with endpoint and correlation ID
	logger := a.buildLogger(state)

	pagination := backend.Pagination()
	maxPages := pagination.MaxPages
	if maxPages <= 0 {
		maxPages = 1
	}

	logger.Debug("backend request initiated",
		slog.String("method", rendered.Method),
		slog.String("url", rendered.URL),
		slog.Int("max_pages", maxPages),
	)

	nextURL := rendered.URL
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

		// Prepare body reader for first page (use rendered body)
		// For subsequent pages, body is reused from first page
		var body io.Reader
		var bodyText string
		if page == 0 && rendered.Body != "" {
			bodyText = rendered.Body
			body = strings.NewReader(rendered.Body)
		}

		req, err := http.NewRequestWithContext(ctx, rendered.Method, parsed.String(), body)
		if err != nil {
			return fmt.Errorf("backend request build: %w", err)
		}
		if body != nil {
			snap := bodyText
			req.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader(snap)), nil
			}
		}

		// Apply rendered headers (only on first page)
		if page == 0 {
			for name, value := range rendered.Headers {
				if strings.TrimSpace(value) != "" {
					req.Header.Set(name, value)
				}
			}
		}

		// Apply rendered query parameters (on all pages, pagination URLs can add/override)
		// This ensures query params from the original request are preserved across pagination
		if len(rendered.Query) > 0 {
			values := req.URL.Query()
			// Only add rendered query params that aren't already in the URL
			// This lets pagination URLs override or add their own params
			for name, value := range rendered.Query {
				if values.Get(name) == "" {
					values.Set(name, value)
				}
			}
			req.URL.RawQuery = values.Encode()
		}

		resp, err := a.client.Do(req)
		if err != nil {
			logger.Error("backend request failed",
				slog.String("url", req.URL.String()),
				slog.Int("page", page),
				slog.Any("error", err),
			)
			return fmt.Errorf("backend request: %w", err)
		}

		pageState := pipeline.BackendPageState{
			URL:      req.URL.String(),
			Status:   resp.StatusCode,
			Headers:  captureResponseHeaders(resp.Header),
			Accepted: backend.Accepts(resp.StatusCode),
		}

		logger.Debug("backend response received",
			slog.String("url", req.URL.String()),
			slog.Int("page", page),
			slog.Int("status", resp.StatusCode),
			slog.Bool("accepted", pageState.Accepted),
		)

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
		logger.Debug("pagination continues",
			slog.Int("page", page),
			slog.String("next_url", nextURL),
		)
	}

	if len(pages) > 1 {
		logger.Debug("pagination completed",
			slog.Int("total_pages", len(pages)),
		)
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

// captureResponseHeaders converts http.Header to a map[string]string,
// taking only the first value of each header and lowercasing header names.
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

// cloneHeaders creates a shallow copy of a header map.
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

// normalizeJSONNumbers recursively converts json.Number values to int64 or float64
// for consistent CEL evaluation.
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

// buildLogger creates a contextual logger with endpoint and correlation ID from pipeline state.
func (a *backendInteractionAgent) buildLogger(state *pipeline.State) *slog.Logger {
	logger := a.logger
	if logger == nil {
		logger = slog.Default()
	}

	logger = logger.With(
		slog.String("component", "runtime"),
		slog.String("agent", "backend_interaction"),
	)

	if state != nil {
		if state.Endpoint != "" {
			logger = logger.With(slog.String("endpoint", state.Endpoint))
		}
		if state.CorrelationID != "" {
			logger = logger.With(slog.String("correlation_id", state.CorrelationID))
		}
	}

	return logger
}
