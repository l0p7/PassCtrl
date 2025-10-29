package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/l0p7/passctrl/internal/config"
	"github.com/l0p7/passctrl/internal/metrics"
	"github.com/l0p7/passctrl/internal/runtime/admission"
	"github.com/l0p7/passctrl/internal/runtime/cache"
	"github.com/l0p7/passctrl/internal/runtime/endpointvars"
	"github.com/l0p7/passctrl/internal/runtime/forwardpolicy"
	"github.com/l0p7/passctrl/internal/runtime/pipeline"
	"github.com/l0p7/passctrl/internal/runtime/responsepolicy"
	"github.com/l0p7/passctrl/internal/runtime/rulechain"
	"github.com/l0p7/passctrl/internal/templates"
)

const (
	defaultCacheTTL       = 30 * time.Second
	defaultCacheNamespace = "passctrl:decision:v1"
)

type PipelineOptions struct {
	Cache              cache.DecisionCache
	CacheTTL           time.Duration
	CacheEpoch         int
	CacheKeySalt       string
	CacheNamespace     string
	Endpoints          map[string]config.EndpointConfig
	Rules              map[string]config.RuleConfig
	RuleSources        []string
	SkippedDefinitions []config.DefinitionSkip
	TemplateSandbox    *templates.Sandbox
	CorrelationHeader  string
	Metrics            metrics.Recorder
}

type Pipeline struct {
	logger            *slog.Logger
	cache             cache.DecisionCache
	cacheTTL          time.Duration
	cacheEpoch        int
	cacheSalt         []byte
	cacheNamespace    string
	correlationHeader string
	metrics           metrics.Recorder

	mu sync.RWMutex

	endpoints        map[string]*endpointRuntime
	defaultEndpoint  *endpointRuntime
	usingFallback    bool
	ruleSources      []string
	skipped          []config.DefinitionSkip
	templateRenderer *templates.Renderer
}

type endpointRuntime struct {
	name       string
	authConfig admission.Config
	agents     []pipeline.Agent
}

type endpointContextKey struct{}

func NewPipeline(logger *slog.Logger, opts PipelineOptions) *Pipeline {
	if logger == nil {
		logger = slog.Default()
	}
	ttl := opts.CacheTTL
	if ttl <= 0 {
		ttl = defaultCacheTTL
	}
	epoch := opts.CacheEpoch
	if epoch <= 0 {
		epoch = 1
	}
	namespace := opts.CacheNamespace
	if namespace == "" {
		namespace = defaultCacheNamespace
	}
	decisionCache := opts.Cache
	if decisionCache == nil {
		decisionCache = cache.NewMemory(ttl)
	}

	p := &Pipeline{
		logger:            logger.With(slog.String("agent", "pipeline")),
		cache:             decisionCache,
		cacheTTL:          ttl,
		cacheEpoch:        epoch,
		cacheSalt:         []byte(opts.CacheKeySalt),
		cacheNamespace:    namespace,
		correlationHeader: strings.TrimSpace(opts.CorrelationHeader),
		metrics:           opts.Metrics,
		endpoints:         make(map[string]*endpointRuntime),
	}

	p.templateRenderer = templates.NewRenderer(opts.TemplateSandbox)
	p.configureEndpoints(opts.Endpoints, opts.Rules)
	p.ruleSources = cloneStringSlice(opts.RuleSources)
	p.skipped = cloneDefinitionSkips(opts.SkippedDefinitions)
	return p
}

func (p *Pipeline) Close(ctx context.Context) error {
	if p.cache == nil {
		return nil
	}
	return p.cache.Close(ctx)
}

// RequestWithEndpointHint ensures downstream agent selection honors an
// endpoint hint that originated from the routing layer.
func (p *Pipeline) RequestWithEndpointHint(r *http.Request, endpoint string) *http.Request {
	if r == nil || strings.TrimSpace(endpoint) == "" {
		return r
	}
	ctx := context.WithValue(r.Context(), endpointContextKey{}, endpoint)
	return r.WithContext(ctx)
}

func (p *Pipeline) logDebugRequestSnapshot(r *http.Request, logger *slog.Logger, state *pipeline.State) {
	if r == nil || logger == nil || state == nil {
		return
	}
	ctx := r.Context()
	if !logger.Enabled(ctx, slog.LevelDebug) {
		return
	}

	attrs := []slog.Attr{
		slog.String("method", state.Raw.Method),
		slog.String("path", state.Raw.Path),
	}
	if state.Raw.Host != "" {
		attrs = append(attrs, slog.String("host", state.Raw.Host))
	}
	if remote := strings.TrimSpace(r.RemoteAddr); remote != "" {
		attrs = append(attrs, slog.String("remote_addr", remote))
	}
	if forwardedFor := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwardedFor != "" {
		attrs = append(attrs, slog.String("forwarded_for", forwardedFor))
	}
	if forwarded := strings.TrimSpace(r.Header.Get("Forwarded")); forwarded != "" {
		attrs = append(attrs, slog.String("forwarded", forwarded))
	}
	attrs = append(attrs,
		slog.Int("header_count", len(state.Raw.Headers)),
		slog.Int("query_count", len(state.Raw.Query)),
	)
	if _, ok := state.Raw.Headers["authorization"]; ok {
		attrs = append(attrs, slog.Bool("authorization_present", true))
	}
	if _, ok := state.Raw.Headers["cookie"]; ok {
		attrs = append(attrs, slog.Bool("cookie_present", true))
	}

	logger.LogAttrs(ctx, slog.LevelDebug, "auth request snapshot", attrs...)
}

func (p *Pipeline) logDebugDecisionSnapshot(ctx context.Context, logger *slog.Logger, state *pipeline.State) {
	if logger == nil || state == nil {
		return
	}
	if !logger.Enabled(ctx, slog.LevelDebug) {
		return
	}

	attrs := []slog.Attr{
		slog.String("admission_decision", state.Admission.Decision),
		slog.Bool("admission_authenticated", state.Admission.Authenticated),
		slog.Bool("admission_trusted_proxy", state.Admission.TrustedProxy),
		slog.Bool("admission_proxy_stripped", state.Admission.ProxyStripped),
		slog.Int("admission_credential_count", len(state.Admission.Credentials)),
		slog.Int("forward_header_count", len(state.Forward.Headers)),
		slog.Int("forward_query_count", len(state.Forward.Query)),
		slog.String("rule_outcome", state.Rule.Outcome),
		slog.Bool("rule_executed", state.Rule.Executed),
		slog.Bool("rule_from_cache", state.Rule.FromCache),
		slog.String("cache_key", state.Cache.Key),
		slog.Bool("cache_hit", state.Cache.Hit),
		slog.Bool("cache_stored", state.Cache.Stored),
		slog.Bool("backend_requested", state.Backend.Requested),
		slog.Bool("backend_accepted", state.Backend.Accepted),
		slog.Int("backend_page_count", len(state.Backend.Pages)),
		slog.Int("response_status", state.Response.Status),
	}
	if state.Admission.Reason != "" {
		attrs = append(attrs, slog.String("admission_reason", state.Admission.Reason))
	}
	if state.Admission.ClientIP != "" {
		attrs = append(attrs, slog.String("admission_client_ip", state.Admission.ClientIP))
	}
	if state.Admission.ProxyNote != "" {
		attrs = append(attrs, slog.String("admission_proxy_note", state.Admission.ProxyNote))
	}
	if state.Admission.ForwardedFor != "" {
		attrs = append(attrs, slog.String("admission_forwarded_for", state.Admission.ForwardedFor))
	}
	if state.Admission.Forwarded != "" {
		attrs = append(attrs, slog.String("admission_forwarded", state.Admission.Forwarded))
	}
	if state.Rule.Reason != "" {
		attrs = append(attrs, slog.String("rule_reason", state.Rule.Reason))
	}
	if len(state.Rule.History) > 0 {
		attrs = append(attrs, slog.Any("rule_history", summarizeRuleHistory(state.Rule.History)))
	}
	if state.Cache.Decision != "" {
		attrs = append(attrs, slog.String("cache_decision", state.Cache.Decision))
	}
	if !state.Cache.StoredAt.IsZero() {
		attrs = append(attrs, slog.Time("cache_stored_at", state.Cache.StoredAt))
	}
	if !state.Cache.ExpiresAt.IsZero() {
		attrs = append(attrs, slog.Time("cache_expires_at", state.Cache.ExpiresAt))
	}
	if state.Backend.Status != 0 {
		attrs = append(attrs, slog.Int("backend_status", state.Backend.Status))
	}
	if state.Backend.Error != "" {
		attrs = append(attrs, slog.String("backend_error", state.Backend.Error))
	}
	if state.Response.Message != "" {
		attrs = append(attrs, slog.String("response_message", state.Response.Message))
	}

	logger.LogAttrs(ctx, slog.LevelDebug, "auth decision snapshot", attrs...)
}

func summarizeRuleHistory(entries []pipeline.RuleHistoryEntry) []map[string]any {
	if len(entries) == 0 {
		return nil
	}
	summary := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		item := map[string]any{
			"name":        entry.Name,
			"outcome":     entry.Outcome,
			"reason":      entry.Reason,
			"duration_ms": float64(entry.Duration) / float64(time.Millisecond),
		}
		if len(entry.Variables) > 0 {
			item["variables"] = cloneInterfaceMap(entry.Variables)
		}
		summary = append(summary, item)
	}
	return summary
}

// EndpointExists reports whether an endpoint with the provided name is
// configured in the active pipeline snapshot.
func (p *Pipeline) EndpointExists(name string) bool {
	_, ok := p.lookupEndpoint(name)
	return ok
}

// WriteError emits a JSON error payload that mirrors auth/explain/health
// responses and includes the currently available endpoints when appropriate.
func (p *Pipeline) WriteError(w http.ResponseWriter, status int, message string) {
	if status <= 0 {
		status = http.StatusInternalServerError
	}
	payload := map[string]any{"error": message}
	names := p.endpointNames()
	if len(names) > 0 {
		payload["availableEndpoints"] = names
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		p.logger.Error("error response encode failed", slog.Any("error", err))
	}
}

func (p *Pipeline) endpointForRequest(r *http.Request) (*endpointRuntime, string, int, string) {
	if hint := endpointHintFromContext(r.Context()); hint != "" {
		runtime, ok := p.lookupEndpoint(hint)
		if !ok {
			return nil, "", http.StatusNotFound, fmt.Sprintf("endpoint %q not found", hint)
		}
		return runtime, runtime.name, http.StatusOK, ""
	}

	p.mu.RLock()
	endpointCount := len(p.endpoints)
	defaultEndpoint := p.defaultEndpoint
	p.mu.RUnlock()

	if endpointCount == 0 {
		if defaultEndpoint != nil {
			return defaultEndpoint, defaultEndpoint.name, http.StatusOK, ""
		}
		return nil, "", http.StatusInternalServerError, "no endpoints configured"
	}

	name := strings.TrimSpace(r.URL.Query().Get("endpoint"))
	if name == "" {
		name = strings.TrimSpace(r.Header.Get("X-PassCtrl-Endpoint"))
	}

	if name == "" {
		if defaultEndpoint != nil {
			return defaultEndpoint, defaultEndpoint.name, http.StatusOK, ""
		}
		return nil, "", http.StatusBadRequest, "endpoint parameter required"
	}

	runtime, ok := p.lookupEndpoint(name)
	if !ok {
		return nil, "", http.StatusNotFound, fmt.Sprintf("endpoint %q not found", name)
	}
	return runtime, runtime.name, http.StatusOK, ""
}

func endpointHintFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	hint, _ := ctx.Value(endpointContextKey{}).(string)
	return strings.TrimSpace(hint)
}

// ServeAuth executes the configured pipeline agents for an auth request and
// renders the structured decision payload.
func (p *Pipeline) ServeAuth(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	endpointRuntime, endpointName, errStatus, errMsg := p.endpointForRequest(r)
	if endpointRuntime == nil {
		p.WriteError(w, errStatus, errMsg)
		return
	}

	correlationID := p.requestCorrelationID(r)
	cacheKey := p.deriveCacheKey(r, endpointRuntime)
	state := pipeline.NewState(r, endpointName, cacheKey, correlationID)

	reqLogger := p.logger.With(
		slog.String("endpoint", endpointName),
		slog.String("correlation_id", correlationID),
	)

	p.logDebugRequestSnapshot(r, reqLogger, state)

	for _, ag := range endpointRuntime.agents {
		// Agents publish their observable state via the shared pipeline.State.
		_ = ag.Execute(r.Context(), r, state)
	}

	if state.Response.Status == 0 {
		state.Response.Status = http.StatusInternalServerError
		state.Response.Message = "pipeline did not render a response"
	}

	if p.correlationHeader != "" {
		if state.Response.Headers == nil {
			state.Response.Headers = make(map[string]string)
		}
		state.Response.Headers[p.correlationHeader] = correlationID
	}

	for k, v := range state.Response.Headers {
		w.Header().Set(k, v)
	}
	if p.correlationHeader != "" {
		w.Header().Set(p.correlationHeader, correlationID)
	}
	// Render a near-empty response body. Only intentionally constructed messages
	// (typically from configured rule/endpoint templates) are echoed. Otherwise
	// the body remains empty. Detailed diagnostics are available via /explain and logs.
	body := strings.TrimSpace(state.Response.Message)
	if body == "" {
		body = strings.TrimSpace(state.Rule.Reason)
		// Filter out auto-generated reasons to avoid leaking internals.
		lower := strings.ToLower(body)
		autoGeneratedReasonSubstrings := []string{
			"condition matched",
			"conditions satisfied",
			"required pass condition",
			"evaluated without explicit outcome",
			"backend request failed",
			"evaluation failed",
		}
		for _, substr := range autoGeneratedReasonSubstrings {
			if strings.Contains(lower, substr) {
				body = ""
				break
			}
		}
	}

	// Set headers and status before writing body (if any).
	if body != "" {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
	}
	w.WriteHeader(state.Response.Status)
	if body != "" {
		if _, err := io.WriteString(w, body); err != nil {
			reqLogger.Error("auth response write failed", slog.Any("error", err))
			return
		}
	}

	duration := time.Since(start)
	statusText := http.StatusText(state.Response.Status)

	p.logDebugDecisionSnapshot(r.Context(), reqLogger, state)

	reqLogger.Info("pipeline completed",
		slog.String("status", statusText),
		slog.Int("http_status", state.Response.Status),
		slog.String("outcome", state.Rule.Outcome),
		slog.Float64("latency_ms", float64(duration)/float64(time.Millisecond)),
		slog.Bool("from_cache", state.Cache.Hit),
	)
	if p.metrics != nil {
		p.metrics.ObserveAuth(endpointName, state.Rule.Outcome, state.Response.Status, state.Cache.Hit, duration)
	}
}

// ServeHealth returns the aggregated runtime health including cache statistics
// and rule provenance details.
func (p *Pipeline) ServeHealth(w http.ResponseWriter, r *http.Request) {
	cacheSize, err := p.cache.Size(r.Context())
	if err != nil {
		p.logger.Error("cache size query failed", slog.Any("error", err))
		cacheSize = 0
	}
	healthStatus, sources, skipped, fallback := p.healthSnapshot()
	status := map[string]any{
		"status":       healthStatus,
		"cacheEntries": cacheSize,
		"observedAt":   time.Now().UTC(),
	}
	if fallback {
		status["usingFallback"] = true
	}
	if len(sources) > 0 {
		status["ruleSources"] = sources
	}
	if len(skipped) > 0 {
		status["skippedDefinitions"] = skipped
	}
	if names := p.endpointNames(); len(names) > 0 {
		status["availableEndpoints"] = names
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		p.logger.Error("health encode failed", slog.Any("error", err))
	}
}

// ServeExplain reports the observable pipeline metadata to callers requesting
// diagnostics.
func (p *Pipeline) ServeExplain(w http.ResponseWriter, r *http.Request) {
	cacheSize, err := p.cache.Size(r.Context())
	if err != nil {
		p.logger.Error("cache size query failed", slog.Any("error", err))
		cacheSize = 0
	}
	status, sources, skipped, fallback := p.healthSnapshot()
	payload := struct {
		Status             string                  `json:"status"`
		ObservedAt         time.Time               `json:"observedAt"`
		CacheEntries       int64                   `json:"cacheEntries"`
		Endpoint           string                  `json:"endpoint,omitempty"`
		UsingFallback      bool                    `json:"usingFallback,omitempty"`
		RuleSources        []string                `json:"ruleSources,omitempty"`
		SkippedDefinitions []config.DefinitionSkip `json:"skippedDefinitions,omitempty"`
		AvailableEndpoints []string                `json:"availableEndpoints,omitempty"`
	}{
		Status:       status,
		ObservedAt:   time.Now().UTC(),
		CacheEntries: cacheSize,
	}
	if fallback {
		payload.UsingFallback = true
	}
	if hint := endpointHintFromContext(r.Context()); hint != "" {
		payload.Endpoint = hint
	}
	if len(sources) > 0 {
		payload.RuleSources = sources
	}
	if len(skipped) > 0 {
		payload.SkippedDefinitions = skipped
	}
	if names := p.endpointNames(); len(names) > 0 {
		payload.AvailableEndpoints = names
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		p.logger.Error("explain encode failed", slog.Any("error", err))
	}
}

func (p *Pipeline) deriveCacheKey(r *http.Request, ep *endpointRuntime) string {
	// Disable caching for endpoints that allow anonymous authentication
	// to prevent cache poisoning when rules use request-specific data
	if ep.authConfig.Allow.None {
		return ""
	}

	raw := cacheKeyFromRequest(r, ep.name, &ep.authConfig)
	sum := sha256.Sum256(append(p.cacheSalt, []byte(raw)...))
	encoded := base64.RawURLEncoding.EncodeToString(sum[:])
	return fmt.Sprintf("%s:%d:%s", p.cacheNamespace, p.cacheEpoch, encoded)
}

func (p *Pipeline) endpointNames() []string {
	p.mu.RLock()
	defaultEndpoint := p.defaultEndpoint
	endpoints := make([]*endpointRuntime, 0, len(p.endpoints))
	for _, runtime := range p.endpoints {
		endpoints = append(endpoints, runtime)
	}
	p.mu.RUnlock()

	names := make([]string, 0, len(endpoints)+1)
	seen := map[string]struct{}{}
	if defaultEndpoint != nil {
		names = append(names, defaultEndpoint.name)
		seen[defaultEndpoint.name] = struct{}{}
	}
	for _, runtime := range endpoints {
		if _, ok := seen[runtime.name]; ok {
			continue
		}
		names = append(names, runtime.name)
	}
	sort.Strings(names)
	return names
}

func (p *Pipeline) healthSnapshot() (string, []string, []config.DefinitionSkip, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	status := "ok"
	if p.usingFallback || len(p.skipped) > 0 {
		status = "degraded"
	}
	sources := cloneStringSlice(p.ruleSources)
	skipped := cloneDefinitionSkips(p.skipped)
	return status, sources, skipped, p.usingFallback
}

func (p *Pipeline) lookupEndpoint(name string) (*endpointRuntime, bool) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil, false
	}
	key := strings.ToLower(trimmed)
	p.mu.RLock()
	runtime, ok := p.endpoints[key]
	p.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return runtime, true
}

func (p *Pipeline) configureEndpoints(endpoints map[string]config.EndpointConfig, rules map[string]config.RuleConfig) {
	p.endpoints = make(map[string]*endpointRuntime)
	p.defaultEndpoint = nil
	p.usingFallback = false

	compiledRules, err := compileConfiguredRules(rules, p.templateRenderer)
	if err != nil {
		p.logger.Warn("failed to compile configured rules", slog.Any("error", err))
		compiledRules = map[string]rulechain.Definition{}
	}

	if len(endpoints) == 0 {
		p.installFallbackEndpoint()
		return
	}

	for name, cfg := range endpoints {
		runtime, err := p.buildEndpointRuntime(name, cfg, compiledRules)
		if err != nil {
			p.logger.Warn("endpoint configuration skipped", slog.String("endpoint", name), slog.Any("error", err))
			continue
		}
		key := strings.ToLower(runtime.name)
		p.endpoints[key] = runtime
	}

	if len(p.endpoints) == 1 {
		for _, runtime := range p.endpoints {
			p.defaultEndpoint = runtime
		}
		return
	}

	if len(p.endpoints) == 0 {
		p.installFallbackEndpoint()
		return
	}
	p.defaultEndpoint = nil
}

func (p *Pipeline) installFallbackEndpoint() {
	ruleExecutionLogger := p.logger.With(
		slog.String("agent", "rule_execution"),
		slog.String("endpoint", "default"),
	)
	trusted := defaultTrustedNetworks()
	defaultAuthConfig := admission.Config{
		Required: false,
		Allow: admission.AllowConfig{
			Authorization: []string{"basic", "bearer"},
		},
	}
	fwdLogger := p.logger.With(
		slog.String("agent", "forward_request_policy"),
		slog.String("endpoint", "default"),
	)
	fwdPolicy, err := forwardpolicy.New(forwardpolicy.DefaultConfig(), p.templateRenderer, fwdLogger)
	if err != nil {
		p.logger.Error("failed to create forward policy agent for default endpoint", slog.String("error", err.Error()))
		fwdPolicy, _ = forwardpolicy.New(forwardpolicy.DefaultConfig(), nil, fwdLogger)
	}
	agents := []pipeline.Agent{
		&serverAgent{},
		admission.New(trusted, false, defaultAuthConfig),
		fwdPolicy,
		rulechain.NewAgent(rulechain.DefaultDefinitions(p.templateRenderer)),
		newRuleExecutionAgent(nil, ruleExecutionLogger, p.templateRenderer, p.cache, p.cacheTTL, p.metrics),
		responsepolicy.NewWithConfig(responsepolicy.Config{Endpoint: "default", Renderer: p.templateRenderer}),
	}
	runtime := &endpointRuntime{
		name:       "default",
		authConfig: defaultAuthConfig,
		agents:     p.instrumentAgents("default", agents),
	}
	p.endpoints[strings.ToLower(runtime.name)] = runtime
	p.defaultEndpoint = runtime
	p.usingFallback = true
}

// Reload swaps the active rule bundle and clears cached decisions so subsequent
// requests evaluate against the latest configuration snapshot.
func (p *Pipeline) Reload(ctx context.Context, bundle config.RuleBundle) {
	if ctx == nil {
		ctx = context.Background()
	}

	p.mu.Lock()
	p.configureEndpoints(bundle.Endpoints, bundle.Rules)
	p.ruleSources = cloneStringSlice(bundle.Sources)
	p.skipped = cloneDefinitionSkips(bundle.Skipped)
	p.mu.Unlock()

	if p.cache == nil {
		p.logger.Info("configuration reloaded", slog.String("event", "rules_reload"))
		return
	}

	prefix := fmt.Sprintf("%s:%d:", p.cacheNamespace, p.cacheEpoch)
	if err := p.cache.DeletePrefix(ctx, prefix); err != nil {
		p.logger.Warn("cache purge failed", slog.Any("error", err), slog.String("cache_prefix", prefix))
		return
	}
	if invalidator, ok := p.cache.(cache.ReloadInvalidator); ok {
		scope := cache.ReloadScope{Namespace: p.cacheNamespace, Epoch: p.cacheEpoch, Prefix: prefix}
		if err := invalidator.InvalidateOnReload(ctx, scope); err != nil {
			p.logger.Warn("cache reload invalidation failed", slog.Any("error", err), slog.String("cache_prefix", prefix))
		}
	}
	p.logger.Info("configuration reloaded", slog.String("event", "rules_reload"), slog.String("cache_prefix", prefix))
}

func (p *Pipeline) buildEndpointRuntime(name string, cfg config.EndpointConfig, compiled map[string]rulechain.Definition) (*endpointRuntime, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil, errors.New("endpoint name required")
	}

	ruleDefs := make([]rulechain.Definition, 0, len(cfg.Rules))
	for _, ref := range cfg.Rules {
		ruleName := strings.TrimSpace(ref.Name)
		if ruleName == "" {
			continue
		}
		def, ok := compiled[ruleName]
		if !ok {
			return nil, fmt.Errorf("rule %q not available", ruleName)
		}
		ruleDefs = append(ruleDefs, def)
	}

	trusted := append(defaultTrustedNetworks(), admission.ParseCIDRs(cfg.ForwardProxyPolicy.TrustedProxyIPs)...)
	authConfig := admissionConfigFromEndpoint(cfg.Authentication)

	// Build endpoint variables agent (evaluates endpoint.variables before rules)
	var endpointVarsAgent pipeline.Agent
	if len(cfg.Variables) > 0 {
		evAgent, err := endpointvars.New(cfg.Variables, p.templateRenderer, p.logger.With(slog.String("agent", "endpoint_variables"), slog.String("endpoint", trimmed)))
		if err != nil {
			return nil, fmt.Errorf("build endpoint variables agent: %w", err)
		}
		endpointVarsAgent = evAgent
	}

	fwdPolicy, err := forwardpolicy.New(
		forwardPolicyFromConfig(cfg.ForwardRequestPolicy),
		p.templateRenderer,
		p.logger.With(slog.String("agent", "forward_request_policy"), slog.String("endpoint", trimmed)),
	)
	if err != nil {
		return nil, fmt.Errorf("build forward policy agent: %w", err)
	}

	agents := []pipeline.Agent{
		&serverAgent{},
		admission.New(trusted, cfg.ForwardProxyPolicy.DevelopmentMode, authConfig),
		fwdPolicy,
	}

	// Add endpoint variables agent if configured
	if endpointVarsAgent != nil {
		agents = append(agents, endpointVarsAgent)
	}

	agents = append(agents,
		rulechain.NewAgent(ruleDefs),
		newRuleExecutionAgent(nil, p.logger.With(slog.String("agent", "rule_execution"), slog.String("endpoint", trimmed)), p.templateRenderer, p.cache, p.cacheTTL, p.metrics),
		responsepolicy.NewWithConfig(responsepolicy.Config{
			Endpoint: trimmed,
			Renderer: p.templateRenderer,
			Pass: responsepolicy.CategoryConfig{
				Status:   cfg.ResponsePolicy.Pass.Status,
				Body:     cfg.ResponsePolicy.Pass.Body,
				BodyFile: cfg.ResponsePolicy.Pass.BodyFile,
				Headers: forwardpolicy.CategoryConfig{
					Allow:  append([]string{}, cfg.ResponsePolicy.Pass.Headers.Allow...),
					Strip:  append([]string{}, cfg.ResponsePolicy.Pass.Headers.Strip...),
					Custom: cloneStringMap(cfg.ResponsePolicy.Pass.Headers.Custom),
				},
			},
			Fail: responsepolicy.CategoryConfig{
				Status:   cfg.ResponsePolicy.Fail.Status,
				Body:     cfg.ResponsePolicy.Fail.Body,
				BodyFile: cfg.ResponsePolicy.Fail.BodyFile,
				Headers: forwardpolicy.CategoryConfig{
					Allow:  append([]string{}, cfg.ResponsePolicy.Fail.Headers.Allow...),
					Strip:  append([]string{}, cfg.ResponsePolicy.Fail.Headers.Strip...),
					Custom: cloneStringMap(cfg.ResponsePolicy.Fail.Headers.Custom),
				},
			},
			Error: responsepolicy.CategoryConfig{
				Status:   cfg.ResponsePolicy.Error.Status,
				Body:     cfg.ResponsePolicy.Error.Body,
				BodyFile: cfg.ResponsePolicy.Error.BodyFile,
				Headers: forwardpolicy.CategoryConfig{
					Allow:  append([]string{}, cfg.ResponsePolicy.Error.Headers.Allow...),
					Strip:  append([]string{}, cfg.ResponsePolicy.Error.Headers.Strip...),
					Custom: cloneStringMap(cfg.ResponsePolicy.Error.Headers.Custom),
				},
			},
		}),
	)

	runtime := &endpointRuntime{
		name:       trimmed,
		authConfig: authConfig,
		agents:     p.instrumentAgents(trimmed, agents),
	}
	if p.defaultEndpoint == nil {
		p.defaultEndpoint = runtime
	}
	return runtime, nil
}

func (p *Pipeline) requestCorrelationID(r *http.Request) string {
	if r != nil && p.correlationHeader != "" {
		if candidate := strings.TrimSpace(r.Header.Get(p.correlationHeader)); candidate != "" {
			return candidate
		}
	}

	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func forwardPolicyFromConfig(cfg config.EndpointForwardRequestPolicyConfig) forwardpolicy.Config {
	return forwardpolicy.Config{
		ForwardProxyHeaders: cfg.ForwardProxyHeaders,
		Headers: forwardpolicy.CategoryConfig{
			Allow:  append([]string{}, cfg.Headers.Allow...),
			Strip:  append([]string{}, cfg.Headers.Strip...),
			Custom: cloneStringMap(cfg.Headers.Custom),
		},
		Query: forwardpolicy.CategoryConfig{
			Allow:  append([]string{}, cfg.Query.Allow...),
			Strip:  append([]string{}, cfg.Query.Strip...),
			Custom: cloneStringMap(cfg.Query.Custom),
		},
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneInterfaceMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneDefinitionSkips(in []config.DefinitionSkip) []config.DefinitionSkip {
	if len(in) == 0 {
		return nil
	}
	out := make([]config.DefinitionSkip, len(in))
	for i, skip := range in {
		cloned := config.DefinitionSkip{
			Kind:   skip.Kind,
			Name:   skip.Name,
			Reason: skip.Reason,
		}
		if len(skip.Sources) > 0 {
			cloned.Sources = make([]string, len(skip.Sources))
			copy(cloned.Sources, skip.Sources)
		}
		out[i] = cloned
	}
	return out
}

func defaultTrustedNetworks() []netip.Prefix {
	return admission.ParseCIDRs([]string{"127.0.0.0/8", "::1/128"})
}

func compileConfiguredRules(rules map[string]config.RuleConfig, renderer *templates.Renderer) (map[string]rulechain.Definition, error) {
	if len(rules) == 0 {
		return map[string]rulechain.Definition{}, nil
	}

	compiled := make(map[string]rulechain.Definition, len(rules))
	for name, cfg := range rules {
		trimmedName := strings.TrimSpace(name)
		if trimmedName == "" {
			continue
		}

		specs := []rulechain.DefinitionSpec{{
			Name:        trimmedName,
			Description: cfg.Description,
			Auth:        buildRuleAuthSpec(cfg.Auth),
			Conditions: rulechain.ConditionSpec{
				Pass:  append([]string{}, cfg.Conditions.Pass...),
				Fail:  append([]string{}, cfg.Conditions.Fail...),
				Error: append([]string{}, cfg.Conditions.Error...),
			},
			Backend: rulechain.BackendDefinitionSpec{
				URL:                 cfg.BackendAPI.URL,
				Method:              cfg.BackendAPI.Method,
				ForwardProxyHeaders: cfg.BackendAPI.ForwardProxyHeaders,
				Headers: forwardpolicy.CategoryConfig{
					Allow:  append([]string{}, cfg.BackendAPI.Headers.Allow...),
					Strip:  append([]string{}, cfg.BackendAPI.Headers.Strip...),
					Custom: cloneStringMap(cfg.BackendAPI.Headers.Custom),
				},
				Query: forwardpolicy.CategoryConfig{
					Allow:  append([]string{}, cfg.BackendAPI.Query.Allow...),
					Strip:  append([]string{}, cfg.BackendAPI.Query.Strip...),
					Custom: cloneStringMap(cfg.BackendAPI.Query.Custom),
				},
				Body:     cfg.BackendAPI.Body,
				BodyFile: cfg.BackendAPI.BodyFile,
				Accepted: append([]int{}, cfg.BackendAPI.AcceptedStatuses...),
				Pagination: rulechain.BackendPaginationSpec{
					Type:     cfg.BackendAPI.Pagination.Type,
					MaxPages: cfg.BackendAPI.Pagination.MaxPages,
				},
			},
			PassMessage:  "",
			FailMessage:  "",
			ErrorMessage: "",
			Responses:    buildRuleResponsesSpec(cfg.Responses),
			Variables:    buildRuleVariablesSpec(cfg.Variables),
		}}

		defs, err := rulechain.CompileDefinitions(specs, renderer)
		if err != nil {
			return nil, fmt.Errorf("compile rule %s: %w", name, err)
		}
		if len(defs) == 0 {
			continue
		}
		compiled[trimmedName] = defs[0]
	}
	return compiled, nil
}

func buildRuleAuthSpec(directives []config.RuleAuthDirective) []rulechain.AuthDirectiveSpec {
	if len(directives) == 0 {
		return nil
	}
	specs := make([]rulechain.AuthDirectiveSpec, 0, len(directives))
	for _, directive := range directives {
		specs = append(specs, rulechain.AuthDirectiveSpec{
			Type: strings.TrimSpace(directive.Type),
			Name: strings.TrimSpace(directive.Name),
			Forward: rulechain.AuthForwardSpec{
				Type:     strings.TrimSpace(directive.ForwardAs.Type),
				Name:     directive.ForwardAs.Name,
				Value:    directive.ForwardAs.Value,
				Token:    directive.ForwardAs.Token,
				User:     directive.ForwardAs.User,
				Password: directive.ForwardAs.Password,
			},
		})
	}
	return specs
}

func buildRuleResponsesSpec(cfg config.RuleResponsesConfig) rulechain.ResponsesSpec {
	return rulechain.ResponsesSpec{
		Pass:  buildRuleResponseSpec(cfg.Pass),
		Fail:  buildRuleResponseSpec(cfg.Fail),
		Error: buildRuleResponseSpec(cfg.Error),
	}
}

func buildRuleResponseSpec(cfg config.RuleResponseConfig) rulechain.ResponseSpec {
	return rulechain.ResponseSpec{
		Headers: forwardpolicy.CategoryConfig{
			Allow:  append([]string{}, cfg.Headers.Allow...),
			Strip:  append([]string{}, cfg.Headers.Strip...),
			Custom: cloneStringMap(cfg.Headers.Custom),
		},
		Variables: cloneStringMap(cfg.Variables),
	}
}

func buildRuleVariablesSpec(cfg config.RuleVariablesConfig) rulechain.VariablesSpec {
	// V2 schema: cfg is map[string]string for local variables
	// These will be evaluated with hybrid CEL/Template evaluator
	return rulechain.VariablesSpec{
		LocalV2: cfg,
	}
}

func admissionConfigFromEndpoint(cfg config.EndpointAuthenticationConfig) admission.Config {
	required := true
	if cfg.Required != nil {
		required = *cfg.Required
	}
	return admission.Config{
		Required: required,
		Allow: admission.AllowConfig{
			Authorization: cloneStringSlice(cfg.Allow.Authorization),
			Header:        cloneStringSlice(cfg.Allow.Header),
			Query:         cloneStringSlice(cfg.Allow.Query),
			None:          cfg.Allow.None,
		},
		Challenge: admission.ChallengeConfig{
			Type:    cfg.Challenge.Type,
			Realm:   cfg.Challenge.Realm,
			Charset: cfg.Challenge.Charset,
		},
	}
}
