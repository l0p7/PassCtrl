package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/l0p7/passctrl/internal/expr"
	"github.com/l0p7/passctrl/internal/runtime/cache"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/runtime/rulechain"
	"github.com/l0p7/passctrl/internal/templates"
)

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type ruleAuthSelection struct {
	directive  rulechain.AuthDirective
	credential pipeline.AdmissionCredential
	forward    ruleAuthForward
}

type ruleAuthForward struct {
	Type     string
	Name     string
	Value    string
	Token    string
	User     string
	Password string
}

type ruleResponseApplication struct {
	ruleName string
	response rulechain.ResponseDefinition
}

// renderedBackendRequest holds a fully-rendered backend request ready for HTTP execution.
// All templates have been evaluated and auth has been applied.
type renderedBackendRequest struct {
	Method  string
	URL     string
	Headers map[string]string
	Query   map[string]string
	Body    string
}

type ruleExecutionAgent struct {
	client        httpDoer
	logger        *slog.Logger
	renderer      *templates.Renderer
	ruleEvaluator *expr.HybridEvaluator
	cacheBackend  cache.DecisionCache // Per-rule caching backend
	serverMaxTTL  time.Duration       // Server-level TTL ceiling
}

func newRuleExecutionAgent(client httpDoer, logger *slog.Logger, renderer *templates.Renderer, cacheBackend cache.DecisionCache, serverMaxTTL time.Duration) *ruleExecutionAgent {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	// Create hybrid evaluator for rule local variables
	ruleEvaluator, err := expr.NewRuleHybridEvaluator(renderer)
	if err != nil {
		// Log error but continue - we'll fail gracefully during variable evaluation
		if logger != nil {
			logger.Warn("failed to create rule evaluator", slog.Any("error", err))
		}
	}
	return &ruleExecutionAgent{
		client:        client,
		logger:        logger,
		renderer:      renderer,
		ruleEvaluator: ruleEvaluator,
		cacheBackend:  cacheBackend,
		serverMaxTTL:  serverMaxTTL,
	}
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
	passResponses := make([]ruleResponseApplication, 0, len(plan.Rules))

	for _, def := range plan.Rules {
		start := time.Now()
		outcome, reason, response := a.evaluateRule(ctx, def, state)
		entry := pipeline.RuleHistoryEntry{
			Name:      def.Name,
			Outcome:   outcome,
			Reason:    reason,
			Duration:  time.Since(start),
			Variables: cloneAnyMap(state.Rule.Variables.Rule),
		}
		history = append(history, entry)

		finalOutcome = outcome
		finalReason = reason
		state.Rule.Outcome = outcome
		state.Rule.Reason = reason

		if outcome == "pass" {
			if response != nil && responseHasOverrides(*response) {
				passResponses = append(passResponses, ruleResponseApplication{
					ruleName: def.Name,
					response: *response,
				})
			}
			continue
		}

		if response != nil && responseHasOverrides(*response) {
			a.applyRuleResponse(def.Name, *response, state)
		}
		break
	}

	if finalOutcome == "pass" && len(passResponses) > 0 {
		// Apply pass responses in declaration order so headers accumulate across the chain.
		for _, resp := range passResponses {
			a.applyRuleResponse(resp.ruleName, resp.response, state)
		}
	}

	if finalOutcome == "" && len(history) == 0 {
		finalOutcome = "error"
		if finalReason == "" {
			finalReason = "no rules evaluated"
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

// finishRule evaluates exported variables and returns the outcome
func (a *ruleExecutionAgent) finishRule(def rulechain.Definition, outcome, reason string, state *pipeline.State) (string, string, *rulechain.ResponseDefinition) {
	resp := selectRuleResponse(def, outcome)
	if err := a.evaluateExportedVariables(def.Name, resp, state); err != nil {
		// Log error but don't fail the rule - exported variable evaluation is best-effort
		if a.logger != nil {
			a.logger.Warn("failed to evaluate exported variables",
				slog.String("rule", def.Name),
				slog.String("outcome", outcome),
				slog.Any("error", err))
		}
	}
	return outcome, reason, resp
}

// finishRuleWithCache wraps finishRule and stores the result in the per-rule cache if applicable.
func (a *ruleExecutionAgent) finishRuleWithCache(ctx context.Context, def rulechain.Definition, rendered *renderedBackendRequest, outcome, reason string, state *pipeline.State) (string, string, *rulechain.ResponseDefinition) {
	// Call finishRule to get the response and evaluate exported variables
	outcome, reason, resp := a.finishRule(def, outcome, reason, state)

	// Store in per-rule cache if applicable
	if rendered != nil && a.cacheBackend != nil && def.Backend.IsConfigured() {
		a.storeRuleCache(ctx, def, *rendered, outcome, reason, state)
	}

	return outcome, reason, resp
}

func (a *ruleExecutionAgent) evaluateRule(ctx context.Context, def rulechain.Definition, state *pipeline.State) (string, string, *rulechain.ResponseDefinition) {
	resetBackendState(&state.Backend)
	state.Rule.Auth = pipeline.RuleAuthState{
		Input:   make(map[string]any),
		Forward: make(map[string]any),
	}
	state.Rule.Variables.Rule = make(map[string]any)
	state.Rule.Variables.Local = make(map[string]any)
	state.Rule.Variables.Exported = make(map[string]any)

	selection, authStatus, authReason := a.prepareRuleAuth(def.Auth, state)
	if authStatus != "" {
		switch authStatus {
		case "fail":
			reason := a.ruleMessage(def.FailTemplate, def.FailMessage, authReason, state)
			return a.finishRule(def, "fail", reason, state)
		case "error":
			reason := a.ruleMessage(def.ErrorTemplate, def.ErrorMessage, authReason, state)
			return a.finishRule(def, "error", reason, state)
		default:
			return a.finishRule(def, authStatus, authReason, state)
		}
	}

	// Track rendered backend request for cache storage
	var renderedBackend *renderedBackendRequest

	if def.Backend.IsConfigured() {
		// Render backend request templates before invocation (enables cache key generation)
		rendered, err := a.renderBackendRequest(def.Backend, selection, state)
		if err != nil {
			state.Backend.Error = err.Error()
			reason := a.ruleMessage(def.ErrorTemplate, def.ErrorMessage, fmt.Sprintf("backend render failed: %v", err), state)
			return a.finishRule(def, "error", reason, state)
		}
		renderedBackend = &rendered

		// Check per-rule cache
		if entry, hit := a.checkRuleCache(ctx, def, rendered, state); hit {
			// Cache hit - restore cached outcome and variables
			restoreFromCache(entry, def.Name, state)
			return a.finishRule(def, entry.Outcome, entry.Reason, nil)
		}

		// Invoke backend with pre-rendered request
		if err := a.invokeBackend(ctx, rendered, def.Backend, state); err != nil {
			state.Backend.Error = err.Error()
			reason := a.ruleMessage(def.ErrorTemplate, def.ErrorMessage, fmt.Sprintf("backend request failed: %v", err), state)
			return a.finishRuleWithCache(ctx, def, renderedBackend, "error", reason, state)
		}
	} else {
		state.Backend.Accepted = true
	}

	if err := a.evaluateRuleVariables(def, state); err != nil {
		reason := a.ruleMessage(def.ErrorTemplate, def.ErrorMessage, fmt.Sprintf("variable extraction failed: %v", err), state)
		return a.finishRuleWithCache(ctx, def, renderedBackend, "error", reason, state)
	}

	activation := buildActivation(state)

	if matched, source, err := evaluateProgramList(def.Conditions.Error, activation, false); err != nil {
		return a.finishRuleWithCache(ctx, def, renderedBackend, "error", fmt.Sprintf("error condition %s evaluation failed: %v", source, err), state)
	} else if matched {
		reason := a.ruleMessage(def.ErrorTemplate, def.ErrorMessage, fmt.Sprintf("error condition matched: %s", source), state)
		return a.finishRuleWithCache(ctx, def, renderedBackend, "error", reason, state)
	}

	if matched, source, err := evaluateProgramList(def.Conditions.Fail, activation, false); err != nil {
		return a.finishRuleWithCache(ctx, def, renderedBackend, "error", fmt.Sprintf("fail condition %s evaluation failed: %v", source, err), state)
	} else if matched {
		reason := a.ruleMessage(def.FailTemplate, def.FailMessage, fmt.Sprintf("fail condition matched: %s", source), state)
		return a.finishRuleWithCache(ctx, def, renderedBackend, "fail", reason, state)
	}

	if matched, source, err := evaluateProgramList(def.Conditions.Pass, activation, true); err != nil {
		return a.finishRuleWithCache(ctx, def, renderedBackend, "error", fmt.Sprintf("pass condition %s evaluation failed: %v", source, err), state)
	} else if matched {
		reason := a.ruleMessage(def.PassTemplate, def.PassMessage, fmt.Sprintf("pass conditions satisfied: %s", source), state)
		return a.finishRuleWithCache(ctx, def, renderedBackend, "pass", reason, state)
	}

	if len(def.Conditions.Pass) > 0 {
		return a.finishRuleWithCache(ctx, def, renderedBackend, "fail", a.ruleMessage(def.FailTemplate, def.FailMessage, "required pass condition not satisfied", state), state)
	}

	if def.Backend.IsConfigured() && !state.Backend.Accepted {
		fallback := "backend response not accepted"
		if state.Backend.Status != 0 {
			fallback = fmt.Sprintf("backend response not accepted: status %d", state.Backend.Status)
		}
		return a.finishRuleWithCache(ctx, def, renderedBackend, "fail", a.ruleMessage(def.FailTemplate, def.FailMessage, fallback, state), state)
	}

	return a.finishRuleWithCache(ctx, def, renderedBackend, "pass", a.ruleMessage(def.PassTemplate, def.PassMessage, "rule evaluated without explicit outcome", state), state)
}

// checkRuleCache builds the cache key and checks for a cached rule result.
// Returns the cached entry and true if found, or nil and false if not found.
func (a *ruleExecutionAgent) checkRuleCache(ctx context.Context, def rulechain.Definition, rendered renderedBackendRequest, state *pipeline.State) (*RuleCacheEntry, bool) {
	if a.cacheBackend == nil {
		return nil, false
	}

	// Build cache key components
	baseKey := state.CacheKey()
	if baseKey == "" {
		return nil, false
	}

	// Build backend hash from rendered request
	descriptor := cache.BackendDescriptor{
		Method:  rendered.Method,
		URL:     rendered.URL,
		Headers: rendered.Headers,
		Body:    rendered.Body,
	}
	backendHash := buildBackendHash(descriptor)

	// Determine if strict mode is enabled
	strict := true
	if def.Cache.Strict != nil {
		strict = *def.Cache.Strict
	}

	// Build upstream variables hash
	upstreamHash := buildUpstreamVarsHash(strict, state)

	// Build final cache key
	cacheKey := buildRuleCacheKey(baseKey, def.Name, backendHash, upstreamHash)

	// Lookup cache
	return lookupRuleCache(ctx, a.cacheBackend, cacheKey)
}

// storeRuleCache stores a rule execution result in the per-rule cache.
func (a *ruleExecutionAgent) storeRuleCache(ctx context.Context, def rulechain.Definition, rendered renderedBackendRequest, outcome, reason string, state *pipeline.State) {
	if a.cacheBackend == nil {
		return
	}

	// Build cache key (same logic as checkRuleCache)
	baseKey := state.CacheKey()
	if baseKey == "" {
		return
	}

	descriptor := cache.BackendDescriptor{
		Method:  rendered.Method,
		URL:     rendered.URL,
		Headers: rendered.Headers,
		Body:    rendered.Body,
	}
	backendHash := buildBackendHash(descriptor)

	strict := true
	if def.Cache.Strict != nil {
		strict = *def.Cache.Strict
	}
	upstreamHash := buildUpstreamVarsHash(strict, state)
	cacheKey := buildRuleCacheKey(baseKey, def.Name, backendHash, upstreamHash)

	// Calculate effective TTL
	endpointTTL := cache.RuleCacheTTLConfig{} // TODO: Get from endpoint config
	ruleConfig := cache.RuleCacheConfig{
		FollowCacheControl: def.Cache.FollowCacheControl,
		TTL: cache.RuleCacheTTLConfig{
			Pass:  def.Cache.TTL.Pass,
			Fail:  def.Cache.TTL.Fail,
			Error: def.Cache.TTL.Error,
		},
		Strict: def.Cache.Strict,
	}
	ttl := cache.CalculateEffectiveTTL(outcome, a.serverMaxTTL, endpointTTL, ruleConfig, state.Backend.Headers)

	// Store in cache
	if err := storeRuleCache(ctx, a.cacheBackend, cacheKey, outcome, reason, state.Rule.Variables.Exported, state.Response.Headers, ttl); err != nil {
		// Log error but don't fail the rule
		if a.logger != nil {
			a.logger.Warn("failed to store rule cache",
				slog.String("rule", def.Name),
				slog.String("cache_key", cacheKey),
				slog.Any("error", err))
		}
	}
}

// renderBackendRequest renders all template components of a backend request before execution.
// This separation allows cache key generation before invoking the backend.
func (a *ruleExecutionAgent) renderBackendRequest(
	backend rulechain.BackendDefinition,
	authSel *ruleAuthSelection,
	state *pipeline.State,
) (renderedBackendRequest, error) {
	// Determine method
	method := backend.Method
	if strings.TrimSpace(method) == "" {
		method = http.MethodGet
	}

	// URL is used as-is (no template rendering for URL currently)
	url := backend.URL

	// Render body template if present
	var body string
	if backend.BodyTemplate != nil {
		rendered, err := backend.BodyTemplate.Render(state.TemplateContext())
		if err != nil {
			return renderedBackendRequest{}, fmt.Errorf("backend body render: %w", err)
		}

		// If the rendered string looks like a file path and a renderer is
		// available, treat it as a template file reference
		content := rendered
		trimmedRendered := strings.TrimSpace(rendered)
		if trimmedRendered != "" && a.renderer != nil {
			if fileTmpl, err := a.renderer.CompileFile(trimmedRendered); err == nil {
				output, err := fileTmpl.Render(state.TemplateContext())
				if err != nil {
					return renderedBackendRequest{}, fmt.Errorf("backend body file render: %w", err)
				}
				content = output
			}
		}
		body = content
	} else if strings.TrimSpace(backend.Body) != "" {
		body = backend.Body
	}

	// Select headers
	headers := backend.SelectHeaders(state.Forward.Headers)
	if backend.ForwardProxyHeaders {
		if state.Admission.ForwardedFor != "" {
			if headers == nil {
				headers = make(map[string]string)
			}
			headers["X-Forwarded-For"] = state.Admission.ForwardedFor
		}
		if state.Admission.Forwarded != "" {
			if headers == nil {
				headers = make(map[string]string)
			}
			headers["Forwarded"] = state.Admission.Forwarded
		}
	}

	// Apply auth selection to headers
	if authSel != nil {
		if headers == nil {
			headers = make(map[string]string)
		}
		if err := applyAuthToHeaders(authSel, headers); err != nil {
			return renderedBackendRequest{}, fmt.Errorf("apply auth to headers: %w", err)
		}
	}

	// Select query parameters
	query := backend.SelectQuery(state.Forward.Query)

	// Apply auth selection to query if needed
	if authSel != nil && authSel.forward.Type == "query" {
		if query == nil {
			query = make(map[string]string)
		}
		if authSel.forward.Name != "" {
			query[authSel.forward.Name] = authSel.forward.Value
		}
	}

	return renderedBackendRequest{
		Method:  method,
		URL:     url,
		Headers: headers,
		Query:   query,
		Body:    body,
	}, nil
}

// applyAuthToHeaders applies authentication to the headers map (not query params).
func applyAuthToHeaders(sel *ruleAuthSelection, headers map[string]string) error {
	if sel == nil {
		return nil
	}

	switch sel.forward.Type {
	case "", "none":
		return nil
	case "basic":
		if sel.forward.User == "" || sel.forward.Password == "" {
			return fmt.Errorf("basic credential missing user or password")
		}
		credential := base64.StdEncoding.EncodeToString([]byte(sel.forward.User + ":" + sel.forward.Password))
		headers["Authorization"] = "Basic " + credential
	case "bearer":
		if sel.forward.Token == "" {
			return fmt.Errorf("bearer credential missing token")
		}
		headers["Authorization"] = "Bearer " + sel.forward.Token
	case "header":
		if sel.forward.Name == "" {
			return fmt.Errorf("header credential missing name")
		}
		headers[sel.forward.Name] = sel.forward.Value
	case "query":
		// Query params are handled separately in renderBackendRequest
		return nil
	default:
		return fmt.Errorf("unsupported credential forward type %s", sel.forward.Type)
	}

	return nil
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

// invokeBackend executes a pre-rendered backend request and handles pagination.
// The rendered parameter contains all template-rendered values (URL, headers, body, etc.).
func (a *ruleExecutionAgent) invokeBackend(ctx context.Context, rendered renderedBackendRequest, backend rulechain.BackendDefinition, state *pipeline.State) error {
	if a.client == nil {
		return errors.New("rule execution agent: http client missing")
	}

	pagination := backend.Pagination()
	maxPages := pagination.MaxPages
	if maxPages <= 0 {
		maxPages = 1
	}

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

func (a *ruleExecutionAgent) prepareRuleAuth(directives []rulechain.AuthDirective, state *pipeline.State) (*ruleAuthSelection, string, string) {
	state.Rule.Auth.Input = make(map[string]any)
	state.Rule.Auth.Forward = make(map[string]any)
	state.Rule.Auth.Selected = ""

	if len(directives) == 0 {
		return nil, "", ""
	}

	creds := state.Admission.Credentials

	for _, directive := range directives {
		if directive.Type == "none" {
			state.Rule.Auth.Selected = directive.Type
			state.Rule.Auth.Input["type"] = directive.Type
			forward, err := a.buildForwardAuth(directive, pipeline.AdmissionCredential{}, state)
			if err != nil {
				return nil, "error", fmt.Sprintf("rule authentication forward failed: %v", err)
			}
			state.Rule.Auth.Forward = forward.toMap()
			return &ruleAuthSelection{directive: directive, forward: forward}, "", ""
		}

		cred, ok := matchAuthCredential(directive, creds)
		if !ok {
			continue
		}

		state.Rule.Auth.Selected = directive.Type
		state.Rule.Auth.Input = buildAuthInput(directive.Type, cred)
		forward, err := a.buildForwardAuth(directive, cred, state)
		if err != nil {
			return nil, "error", fmt.Sprintf("rule authentication forward failed: %v", err)
		}
		state.Rule.Auth.Forward = forward.toMap()

		return &ruleAuthSelection{directive: directive, credential: cred, forward: forward}, "", ""
	}

	state.Rule.Auth.Input["type"] = "unmatched"
	return nil, "fail", "rule authentication did not match any credential"
}

func matchAuthCredential(directive rulechain.AuthDirective, creds []pipeline.AdmissionCredential) (pipeline.AdmissionCredential, bool) {
	for _, cred := range creds {
		switch directive.Type {
		case "basic":
			if cred.Type == "basic" {
				return cred, true
			}
		case "bearer":
			if cred.Type == "bearer" {
				return cred, true
			}
		case "header":
			if cred.Type == "header" && strings.EqualFold(cred.Name, directive.Name) {
				return cred, true
			}
		case "query":
			if cred.Type == "query" && strings.EqualFold(cred.Name, directive.Name) {
				return cred, true
			}
		}
	}
	return pipeline.AdmissionCredential{}, false
}

func buildAuthInput(kind string, cred pipeline.AdmissionCredential) map[string]any {
	input := map[string]any{
		"type":   kind,
		"source": cred.Source,
	}
	switch kind {
	case "basic":
		if cred.Username != "" {
			input["username"] = cred.Username
		}
		if cred.Password != "" {
			input["password"] = cred.Password
		}
	case "bearer":
		if cred.Token != "" {
			input["token"] = cred.Token
		}
	case "header":
		if cred.Name != "" {
			input["name"] = cred.Name
		}
		if cred.Value != "" {
			input["value"] = cred.Value
			input["header"] = cred.Value
		}
	case "query":
		if cred.Name != "" {
			input["name"] = cred.Name
		}
		if cred.Value != "" {
			input["value"] = cred.Value
			input["query"] = cred.Value
		}
	case "none":
		input["type"] = "none"
	}
	return input
}

func (a *ruleExecutionAgent) buildForwardAuth(directive rulechain.AuthDirective, cred pipeline.AdmissionCredential, state *pipeline.State) (ruleAuthForward, error) {
	forwardsType := directive.Forward.Type
	if strings.TrimSpace(forwardsType) == "" {
		forwardsType = directive.Type
	}
	forwardType := strings.ToLower(strings.TrimSpace(forwardsType))

	forward := ruleAuthForward{Type: forwardType}
	ctx := state.TemplateContext()

	renderValue := func(tmpl *templates.Template, literal string) (string, error) {
		if tmpl != nil {
			rendered, err := tmpl.Render(ctx)
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(rendered), nil
		}
		return strings.TrimSpace(literal), nil
	}

	switch forwardType {
	case "", "none":
		return forward, nil
	case "basic":
		user, err := renderValue(directive.Forward.UserTemplate, directive.Forward.User)
		if err != nil {
			return ruleAuthForward{}, err
		}
		pass, err := renderValue(directive.Forward.PasswordTemplate, directive.Forward.Password)
		if err != nil {
			return ruleAuthForward{}, err
		}
		if user == "" {
			user = cred.Username
		}
		if pass == "" {
			pass = cred.Password
		}
		if user == "" || pass == "" {
			return ruleAuthForward{}, fmt.Errorf("basic credential requires user and password")
		}
		forward.User = user
		forward.Password = pass
	case "bearer":
		token, err := renderValue(directive.Forward.TokenTemplate, directive.Forward.Token)
		if err != nil {
			return ruleAuthForward{}, err
		}
		if token == "" {
			token = cred.Token
		}
		if token == "" {
			return ruleAuthForward{}, fmt.Errorf("bearer credential requires token")
		}
		forward.Token = token
	case "header":
		name, err := renderValue(directive.Forward.NameTemplate, directive.Forward.Name)
		if err != nil {
			return ruleAuthForward{}, err
		}
		value, err := renderValue(directive.Forward.ValueTemplate, directive.Forward.Value)
		if err != nil {
			return ruleAuthForward{}, err
		}
		if name == "" {
			if cred.Name != "" {
				name = cred.Name
			} else {
				name = directive.Name
			}
		}
		if value == "" {
			value = cred.Value
		}
		if name == "" || value == "" {
			return ruleAuthForward{}, fmt.Errorf("header credential requires name and value")
		}
		forward.Name = name
		forward.Value = value
	case "query":
		name, err := renderValue(directive.Forward.NameTemplate, directive.Forward.Name)
		if err != nil {
			return ruleAuthForward{}, err
		}
		value, err := renderValue(directive.Forward.ValueTemplate, directive.Forward.Value)
		if err != nil {
			return ruleAuthForward{}, err
		}
		if name == "" {
			if cred.Name != "" {
				name = cred.Name
			} else {
				name = directive.Name
			}
		}
		if value == "" {
			value = cred.Value
		}
		if name == "" {
			return ruleAuthForward{}, fmt.Errorf("query credential requires name")
		}
		forward.Name = name
		forward.Value = value
	default:
		return ruleAuthForward{}, fmt.Errorf("unsupported forward type %s", forwardType)
	}

	return forward, nil
}

func (f ruleAuthForward) toMap() map[string]any {
	out := make(map[string]any)
	if f.Type != "" {
		out["type"] = f.Type
	}
	if f.Name != "" {
		out["name"] = f.Name
	}
	if f.Value != "" {
		out["value"] = f.Value
	}
	if f.Token != "" {
		out["token"] = f.Token
	}
	if f.User != "" {
		out["user"] = f.User
	}
	if f.Password != "" {
		out["password"] = f.Password
	}
	return out
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
		"auth": map[string]any{
			"selected": state.Rule.Auth.Selected,
			"input":    cloneAnyMap(state.Rule.Auth.Input),
			"forward":  cloneAnyMap(state.Rule.Auth.Forward),
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
		"vars": state.VariablesContext(),
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

func selectRuleResponse(def rulechain.Definition, outcome string) *rulechain.ResponseDefinition {
	switch outcome {
	case "pass":
		resp := def.Responses.Pass
		return &resp
	case "fail":
		resp := def.Responses.Fail
		return &resp
	case "error":
		resp := def.Responses.Error
		return &resp
	default:
		return nil
	}
}

func responseHasOverrides(resp rulechain.ResponseDefinition) bool {
	if len(resp.Headers.Allow) > 0 || len(resp.Headers.Strip) > 0 || len(resp.Headers.Custom) > 0 {
		return true
	}
	return false
}

func (a *ruleExecutionAgent) evaluateRuleVariables(def rulechain.Definition, state *pipeline.State) error {
	vars := def.Variables
	hasV1 := len(vars.Global) > 0 || len(vars.Rule) > 0 || len(vars.Local) > 0
	hasV2 := len(vars.LocalV2) > 0

	if !hasV1 && !hasV2 {
		return nil
	}

	if state.Variables.Global == nil {
		state.Variables.Global = make(map[string]any)
	}
	if state.Variables.Rules == nil {
		state.Variables.Rules = make(map[string]map[string]any)
	}
	if state.Rule.Variables.Rule == nil {
		state.Rule.Variables.Rule = make(map[string]any)
	} else {
		for k := range state.Rule.Variables.Rule {
			delete(state.Rule.Variables.Rule, k)
		}
	}
	if state.Rule.Variables.Local == nil {
		state.Rule.Variables.Local = make(map[string]any)
	} else {
		for k := range state.Rule.Variables.Local {
			delete(state.Rule.Variables.Local, k)
		}
	}

	// V1 evaluation (legacy)
	evaluateScope := func(scope string, defs map[string]rulechain.VariableDefinition, assign func(string, any)) error {
		if len(defs) == 0 {
			return nil
		}
		keys := make([]string, 0, len(defs))
		for name := range defs {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			defn := defs[name]
			activation := buildActivation(state)
			value, err := defn.Program.Eval(activation)
			if err != nil {
				return fmt.Errorf("%s.%s: %w", scope, name, err)
			}
			assign(name, value)
		}
		return nil
	}

	if err := evaluateScope("global", vars.Global, func(name string, value any) {
		state.Variables.Global[name] = value
	}); err != nil {
		return err
	}

	if err := evaluateScope("rule", vars.Rule, func(name string, value any) {
		state.Rule.Variables.Rule[name] = value
	}); err != nil {
		return err
	}

	if trimmed := strings.TrimSpace(def.Name); trimmed != "" {
		if len(state.Rule.Variables.Rule) > 0 {
			state.Variables.Rules[trimmed] = cloneAnyMap(state.Rule.Variables.Rule)
		} else {
			delete(state.Variables.Rules, trimmed)
		}
	}

	if err := evaluateScope("local", vars.Local, func(name string, value any) {
		state.Rule.Variables.Local[name] = value
	}); err != nil {
		return err
	}

	// V2 evaluation (hybrid CEL/Template for local variables)
	if hasV2 {
		if err := a.evaluateLocalVariablesV2(vars.LocalV2, state); err != nil {
			return fmt.Errorf("local variables: %w", err)
		}
	}

	return nil
}

// evaluateLocalVariablesV2 evaluates v2 local variables using hybrid CEL/Template evaluator
func (a *ruleExecutionAgent) evaluateLocalVariablesV2(localVars map[string]string, state *pipeline.State) error {
	if len(localVars) == 0 {
		return nil
	}

	if a.ruleEvaluator == nil {
		return fmt.Errorf("rule evaluator not initialized")
	}

	// Build rule context for evaluation
	ctx := buildRuleContext(state)

	// Evaluate variables in sorted order for determinism
	names := make([]string, 0, len(localVars))
	for name := range localVars {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		expression := localVars[name]
		value, err := a.ruleEvaluator.Evaluate(expression, ctx)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		state.Rule.Variables.Local[name] = value

		// Also update ctx.variables for subsequent variable evaluations
		if variables, ok := ctx["variables"].(map[string]any); ok {
			variables[name] = value
		}
	}

	return nil
}

// buildRuleContext creates the context for rule variable evaluation
func buildRuleContext(state *pipeline.State) map[string]any {
	// Build backend context
	backend := map[string]any{
		"requested": state.Backend.Requested,
		"status":    state.Backend.Status,
		"headers":   toAnyMap(state.Backend.Headers),
		"body":      state.Backend.Body,
		"bodyText":  state.Backend.BodyText,
		"error":     state.Backend.Error,
		"accepted":  state.Backend.Accepted,
	}

	// Build auth context
	auth := map[string]any{
		"selected": state.Rule.Auth.Selected,
		"input":    cloneAnyMap(state.Rule.Auth.Input),
		"forward":  cloneAnyMap(state.Rule.Auth.Forward),
	}

	// Build request context
	request := map[string]any{
		"method":  state.Raw.Method,
		"path":    state.Raw.Path,
		"host":    state.Raw.Host,
		"headers": toAnyMap(state.Raw.Headers),
		"query":   toAnyMap(state.Raw.Query),
	}

	// Variables context includes already-evaluated local variables
	variables := cloneAnyMap(state.Rule.Variables.Local)

	return map[string]any{
		"backend":   backend,
		"auth":      auth,
		"vars":      state.VariablesContext(),
		"request":   request,
		"variables": variables,
	}
}

// evaluateExportedVariables evaluates exported variables from the winning outcome
// and makes them available to subsequent rules as .rules.<rule-name>.variables.*
func (a *ruleExecutionAgent) evaluateExportedVariables(ruleName string, resp *rulechain.ResponseDefinition, state *pipeline.State) error {
	if resp == nil || len(resp.ExportedVariables) == 0 {
		return nil
	}

	if a.ruleEvaluator == nil {
		return fmt.Errorf("rule evaluator not initialized")
	}

	// Build rule context for evaluation (same as local variables)
	ctx := buildRuleContext(state)

	// Evaluate variables in sorted order for determinism
	names := make([]string, 0, len(resp.ExportedVariables))
	for name := range resp.ExportedVariables {
		names = append(names, name)
	}
	sort.Strings(names)

	exported := make(map[string]any, len(names))
	for _, name := range names {
		expression := resp.ExportedVariables[name]
		value, err := a.ruleEvaluator.Evaluate(expression, ctx)
		if err != nil {
			return fmt.Errorf("exported variable %s: %w", name, err)
		}
		exported[name] = value
	}

	// Store exported variables for current rule
	state.Rule.Variables.Exported = exported

	// Store in state.Variables.Rules for access by subsequent rules
	if state.Variables.Rules == nil {
		state.Variables.Rules = make(map[string]map[string]any)
	}
	state.Variables.Rules[ruleName] = exported

	return nil
}

func (a *ruleExecutionAgent) applyRuleResponse(ruleName string, resp rulechain.ResponseDefinition, state *pipeline.State) {
	if state == nil {
		return
	}
	if !responseHasOverrides(resp) {
		return
	}

	if state.Response.Headers == nil {
		state.Response.Headers = make(map[string]string)
	}

	headers := make(map[string]string)
	keyMap := make(map[string]string)

	mergeHeaders := func(source map[string]string, overwrite bool) {
		for key, value := range source {
			trimmedKey := strings.TrimSpace(key)
			trimmedValue := strings.TrimSpace(value)
			if trimmedKey == "" {
				continue
			}
			lower := strings.ToLower(trimmedKey)
			if !overwrite {
				if _, ok := headers[lower]; ok {
					continue
				}
			}
			headers[lower] = trimmedValue
			keyMap[lower] = trimmedKey
		}
	}

	mergeHeaders(state.Backend.Headers, false)
	mergeHeaders(state.Response.Headers, true)

	if len(resp.Headers.Allow) > 0 {
		allowed := make(map[string]string)
		allowedKeys := make(map[string]string)
		for _, name := range resp.Headers.Allow {
			trimmed := strings.TrimSpace(name)
			if trimmed == "" {
				continue
			}
			if trimmed == "*" {
				allowed = headers
				allowedKeys = keyMap
				break
			}
			lower := strings.ToLower(trimmed)
			if value, ok := headers[lower]; ok {
				allowed[lower] = value
				allowedKeys[lower] = keyMap[lower]
			}
		}
		headers = allowed
		keyMap = allowedKeys
	}

	if len(resp.Headers.Strip) > 0 {
		for _, name := range resp.Headers.Strip {
			lower := strings.ToLower(strings.TrimSpace(name))
			if lower == "" {
				continue
			}
			delete(headers, lower)
			delete(keyMap, lower)
		}
	}

	if len(resp.Headers.Custom) > 0 {
		keys := make([]string, 0, len(resp.Headers.Custom))
		for name := range resp.Headers.Custom {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			trimmedName := strings.TrimSpace(name)
			if trimmedName == "" {
				continue
			}
			lower := strings.ToLower(trimmedName)
			value := strings.TrimSpace(resp.Headers.Custom[name])
			tmpl := resp.HeaderTemplates[name]
			if tmpl != nil {
				rendered, err := tmpl.Render(state.TemplateContext())
				if err != nil {
					a.logTemplateError("rule response header template rendering failed", ruleName, trimmedName, err, state)
					continue
				}
				if candidate := strings.TrimSpace(rendered); candidate != "" {
					value = candidate
				}
			}
			if value == "" {
				delete(headers, lower)
				delete(keyMap, lower)
				continue
			}
			headers[lower] = value
			keyMap[lower] = trimmedName
		}
	}

	finalHeaders := make(map[string]string, len(headers)+1)
	for lower, value := range headers {
		key := keyMap[lower]
		if key == "" {
			key = lower
		}
		finalHeaders[key] = value
	}
	finalHeaders["X-PassCtrl-Outcome"] = state.Rule.Outcome
	state.Response.Headers = finalHeaders
}

func (a *ruleExecutionAgent) logTemplateError(message, ruleName, key string, err error, state *pipeline.State) {
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
	if ruleName != "" {
		logger = logger.With(slog.String("rule", ruleName))
	}
	if key != "" {
		logger = logger.With(slog.String("key", key))
	}
	logger.Warn(message, slog.Any("error", err))
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

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
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
