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

type ruleExecutionAgent struct {
	client   httpDoer
	logger   *slog.Logger
	renderer *templates.Renderer
}

func newRuleExecutionAgent(client httpDoer, logger *slog.Logger, renderer *templates.Renderer) *ruleExecutionAgent {
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

func (a *ruleExecutionAgent) evaluateRule(ctx context.Context, def rulechain.Definition, state *pipeline.State) (string, string, *rulechain.ResponseDefinition) {
	resetBackendState(&state.Backend)
	state.Rule.Auth = pipeline.RuleAuthState{
		Input:   make(map[string]any),
		Forward: make(map[string]any),
	}
	state.Rule.Variables.Rule = make(map[string]any)
	state.Rule.Variables.Local = make(map[string]any)

	selection, authStatus, authReason := a.prepareRuleAuth(def.Auth, state)
	if authStatus != "" {
		switch authStatus {
		case "fail":
			reason := a.ruleMessage(def.FailTemplate, def.FailMessage, authReason, state)
			return "fail", reason, selectRuleResponse(def, "fail")
		case "error":
			reason := a.ruleMessage(def.ErrorTemplate, def.ErrorMessage, authReason, state)
			return "error", reason, selectRuleResponse(def, "error")
		default:
			return authStatus, authReason, selectRuleResponse(def, authStatus)
		}
	}

	if def.Backend.IsConfigured() {
		if err := a.invokeBackend(ctx, def.Backend, selection, state); err != nil {
			state.Backend.Error = err.Error()
			reason := a.ruleMessage(def.ErrorTemplate, def.ErrorMessage, fmt.Sprintf("backend request failed: %v", err), state)
			return "error", reason, selectRuleResponse(def, "error")
		}
	} else {
		state.Backend.Accepted = true
	}

	if err := a.evaluateRuleVariables(def, state); err != nil {
		reason := a.ruleMessage(def.ErrorTemplate, def.ErrorMessage, fmt.Sprintf("variable extraction failed: %v", err), state)
		return "error", reason, selectRuleResponse(def, "error")
	}

	activation := buildActivation(state)

	if matched, source, err := evaluateProgramList(def.Conditions.Error, activation, false); err != nil {
		return "error", fmt.Sprintf("error condition %s evaluation failed: %v", source, err), selectRuleResponse(def, "error")
	} else if matched {
		reason := a.ruleMessage(def.ErrorTemplate, def.ErrorMessage, fmt.Sprintf("error condition matched: %s", source), state)
		return "error", reason, selectRuleResponse(def, "error")
	}

	if matched, source, err := evaluateProgramList(def.Conditions.Fail, activation, false); err != nil {
		return "error", fmt.Sprintf("fail condition %s evaluation failed: %v", source, err), selectRuleResponse(def, "error")
	} else if matched {
		reason := a.ruleMessage(def.FailTemplate, def.FailMessage, fmt.Sprintf("fail condition matched: %s", source), state)
		return "fail", reason, selectRuleResponse(def, "fail")
	}

	if matched, source, err := evaluateProgramList(def.Conditions.Pass, activation, true); err != nil {
		return "error", fmt.Sprintf("pass condition %s evaluation failed: %v", source, err), selectRuleResponse(def, "error")
	} else if matched {
		reason := a.ruleMessage(def.PassTemplate, def.PassMessage, fmt.Sprintf("pass conditions satisfied: %s", source), state)
		return "pass", reason, selectRuleResponse(def, "pass")
	}

	if len(def.Conditions.Pass) > 0 {
		return "fail", a.ruleMessage(def.FailTemplate, def.FailMessage, "required pass condition not satisfied", state), selectRuleResponse(def, "fail")
	}

	if def.Backend.IsConfigured() && !state.Backend.Accepted {
		fallback := "backend response not accepted"
		if state.Backend.Status != 0 {
			fallback = fmt.Sprintf("backend response not accepted: status %d", state.Backend.Status)
		}
		return "fail", a.ruleMessage(def.FailTemplate, def.FailMessage, fallback, state), selectRuleResponse(def, "fail")
	}

	return "pass", a.ruleMessage(def.PassTemplate, def.PassMessage, "rule evaluated without explicit outcome", state), selectRuleResponse(def, "pass")
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

func (a *ruleExecutionAgent) invokeBackend(ctx context.Context, backend rulechain.BackendDefinition, authSel *ruleAuthSelection, state *pipeline.State) error {
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

		if err := applyRuleAuthSelection(req, authSel); err != nil {
			return fmt.Errorf("apply rule authentication: %w", err)
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

func applyRuleAuthSelection(req *http.Request, sel *ruleAuthSelection) error {
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
		req.Header.Set("Authorization", "Basic "+credential)
	case "bearer":
		if sel.forward.Token == "" {
			return fmt.Errorf("bearer credential missing token")
		}
		req.Header.Set("Authorization", "Bearer "+sel.forward.Token)
	case "header":
		if sel.forward.Name == "" {
			return fmt.Errorf("header credential missing name")
		}
		req.Header.Set(sel.forward.Name, sel.forward.Value)
	case "query":
		if sel.forward.Name == "" {
			return fmt.Errorf("query credential missing name")
		}
		values := req.URL.Query()
		values.Set(sel.forward.Name, sel.forward.Value)
		req.URL.RawQuery = values.Encode()
	default:
		return fmt.Errorf("unsupported credential forward type %s", sel.forward.Type)
	}

	return nil
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
	if len(vars.Global) == 0 && len(vars.Rule) == 0 && len(vars.Local) == 0 {
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
