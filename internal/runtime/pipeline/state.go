package pipeline

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// Agent represents a runtime component that collaborates on processing an
// incoming request. Each agent observes and mutates the shared State before
// returning its Result snapshot.
type Agent interface {
	Name() string
	Execute(context.Context, *http.Request, *State) Result
}

// Result captures the outcome emitted by an agent during pipeline execution.
type Result struct {
	Name    string         `json:"name"`
	Status  string         `json:"status"`
	Details string         `json:"details,omitempty"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// ServerState notes server lifecycle details so downstream agents can surface
// readiness metadata.
type ServerState struct {
	PipelineReady bool      `json:"pipelineReady"`
	ObservedAt    time.Time `json:"observedAt"`
}

// RequestState preserves the inbound request snapshot for auditing and template
// evaluation.
type RequestState struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Host    string            `json:"host"`
	Headers map[string]string `json:"headers"`
	Query   map[string]string `json:"query"`
}

// AdmissionState records authentication and proxy policy decisions.
type AdmissionState struct {
	Authenticated bool                  `json:"authenticated"`
	Reason        string                `json:"reason,omitempty"`
	CapturedAt    time.Time             `json:"capturedAt"`
	ClientIP      string                `json:"clientIp,omitempty"`
	TrustedProxy  bool                  `json:"trustedProxy"`
	ProxyStripped bool                  `json:"proxyStripped"`
	ForwardedFor  string                `json:"forwardedFor,omitempty"`
	Forwarded     string                `json:"forwarded,omitempty"`
	ProxyNote     string                `json:"proxyNote,omitempty"`
	Decision      string                `json:"decision"`
	Snapshot      map[string]any        `json:"snapshot,omitempty"`
	Allow         AdmissionAllow        `json:"allow"`
	Credentials   []AdmissionCredential `json:"credentials,omitempty"`
}

// ForwardState exposes the curated headers and query parameters the forward
// request policy authorizes.
type ForwardState struct {
	Headers map[string]string `json:"headers"`
	Query   map[string]string `json:"query"`
}

// RuleState summarizes the rule chain execution outcome.
type RuleState struct {
	Outcome       string             `json:"outcome"`
	Reason        string             `json:"reason,omitempty"`
	Executed      bool               `json:"executed"`
	FromCache     bool               `json:"fromCache"`
	EvaluatedAt   time.Time          `json:"evaluatedAt"`
	ShouldExecute bool               `json:"-"`
	History       []RuleHistoryEntry `json:"history,omitempty"`
	Auth          RuleAuthState      `json:"auth"`
	Variables     RuleVariableState  `json:"variables"`
}

// RuleHistoryEntry records the result of a single rule within the chain.
type RuleHistoryEntry struct {
	Name      string         `json:"name"`
	Outcome   string         `json:"outcome"`
	Reason    string         `json:"reason,omitempty"`
	Duration  time.Duration  `json:"duration"`
	Variables map[string]any `json:"variables,omitempty"`
	FromCache bool           `json:"fromCache,omitempty"`
}

// RuleAuthState surfaces the matched authentication directive and forwarding
// metadata for templates and observability.
type RuleAuthState struct {
	Selected string         `json:"selected"`
	Input    map[string]any `json:"input"`
	Forward  map[string]any `json:"forward,omitempty"`
}

// RuleVariableState captures the rule and local variable scopes for the active rule.
type RuleVariableState struct {
	Rule     map[string]any `json:"rule"`
	Local    map[string]any `json:"local"`
	Exported map[string]any `json:"exported"`
}

// ResponseState is the HTTP response composed for the caller.
type ResponseState struct {
	Status    int               `json:"status"`
	Message   string            `json:"message"`
	Headers   map[string]string `json:"headers"`
	Variables map[string]any    `json:"variables"` // Exported variables from decisive rule's responses.*.variables
}

// CacheState captures cache participation information for the request.
type CacheState struct {
	Key       string    `json:"key"`
	Hit       bool      `json:"hit"`
	Decision  string    `json:"decision,omitempty"`
	StoredAt  time.Time `json:"storedAt,omitempty"`
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
	Stored    bool      `json:"stored"`
}

// BackendState reports backend proxy interactions performed during rule
// execution.
type BackendState struct {
	Requested bool               `json:"requested"`
	Status    int                `json:"status"`
	Headers   map[string]string  `json:"headers"`
	Body      any                `json:"body,omitempty"`
	BodyText  string             `json:"bodyText,omitempty"`
	Error     string             `json:"error,omitempty"`
	Accepted  bool               `json:"accepted"`
	Pages     []BackendPageState `json:"pages,omitempty"`
}

// BackendPageState records metadata for additional backend pages requested
// during pagination.
type BackendPageState struct {
	URL      string            `json:"url"`
	Status   int               `json:"status"`
	Headers  map[string]string `json:"headers"`
	Body     any               `json:"body,omitempty"`
	BodyText string            `json:"bodyText,omitempty"`
	Accepted bool              `json:"accepted"`
}

// VariablesState tracks shared variables exposed across rules and responses.
type VariablesState struct {
	Global      map[string]any            `json:"global"`
	Rules       map[string]map[string]any `json:"rules"`
	Environment map[string]string         `json:"environment"`
	Secrets     map[string]string         `json:"secrets"`
}

// State is the shared context threaded through every agent in the pipeline.
type State struct {
	cacheKey string
	plan     any

	Endpoint      string `json:"endpoint"`
	CorrelationID string `json:"correlationId"`

	Server    ServerState    `json:"server"`
	Request   RequestState   `json:"request"`
	Admission AdmissionState `json:"admission"`
	Forward   ForwardState   `json:"forwardRequest"`
	Rule      RuleState      `json:"rule"`
	Response  ResponseState  `json:"response"`
	Cache     CacheState     `json:"cache"`
	Backend   BackendState   `json:"backend"`
	Variables VariablesState `json:"variables"`
}

// AdmissionAllow mirrors the endpoint authentication configuration so rules can
// reason about permitted credential sources.
type AdmissionAllow struct {
	Authorization []string `json:"authorization"`
	Header        []string `json:"header"`
	Query         []string `json:"query"`
	None          bool     `json:"none"`
}

// AdmissionCredential records a credential that satisfied the admission policy.
type AdmissionCredential struct {
	Type     string `json:"type"`
	Name     string `json:"name,omitempty"`
	Value    string `json:"value,omitempty"`
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Source   string `json:"source,omitempty"`
}

// NewState captures the inbound request metadata and initializes the shared
// state for a pipeline evaluation.
func NewState(r *http.Request, endpoint, cacheKey, correlationID string) *State {
	headers := make(map[string]string)
	for name, values := range r.Header {
		if len(values) == 0 {
			continue
		}
		headers[strings.ToLower(name)] = values[0]
	}
	query := make(map[string]string)
	for name, values := range r.URL.Query() {
		if len(values) == 0 {
			continue
		}
		query[strings.ToLower(name)] = values[0]
	}
	return &State{
		cacheKey:      cacheKey,
		Endpoint:      endpoint,
		CorrelationID: correlationID,
		Request: RequestState{
			Method:  r.Method,
			Path:    r.URL.Path,
			Host:    r.Host,
			Headers: headers,
			Query:   query,
		},
		Forward: ForwardState{
			Headers: make(map[string]string),
			Query:   make(map[string]string),
		},
		Response: ResponseState{
			Headers: make(map[string]string),
		},
		Cache: CacheState{Key: cacheKey},
		Backend: BackendState{
			Headers: make(map[string]string),
		},
		Rule: RuleState{
			Auth: RuleAuthState{
				Input:   make(map[string]any),
				Forward: make(map[string]any),
			},
			Variables: RuleVariableState{
				Rule:  make(map[string]any),
				Local: make(map[string]any),
			},
		},
		Variables: VariablesState{
			Global:      make(map[string]any),
			Rules:       make(map[string]map[string]any),
			Environment: make(map[string]string),
			Secrets:     make(map[string]string),
		},
	}
}

// CacheKey exposes the underlying cache key derived for the request.
func (s *State) CacheKey() string { return s.cacheKey }

// SetPlan stores an agent-specific execution plan on the shared state.
func (s *State) SetPlan(plan any) { s.plan = plan }

// Plan retrieves the agent-specific execution plan stored on the state.
func (s *State) Plan() any { return s.plan }

// ClearPlan removes any stored execution plan from the state.
func (s *State) ClearPlan() { s.plan = nil }

// TemplateContext exposes a map suitable for template execution, capturing the
// full pipeline state snapshot.
func (s *State) TemplateContext() map[string]any {
	if s == nil {
		return map[string]any{}
	}
	// Build response context with variables flattened to top level
	responseCtx := map[string]any{
		"status":  s.Response.Status,
		"message": s.Response.Message,
		"headers": s.Response.Headers,
	}
	// Flatten response variables to .response.<varname> for endpoint templates
	if s.Response.Variables != nil {
		for k, v := range s.Response.Variables {
			responseCtx[k] = v
		}
	}

	ctx := map[string]any{
		"endpoint":      s.Endpoint,
		"correlationId": s.CorrelationID,
		"server":        s.Server,
		"request":       s.Request,
		"admission":     s.Admission,
		"forward":       s.Forward,
		"rule":          s.Rule,
		"response":      responseCtx,
		"cache":         s.Cache,
		"backend":       s.Backend,
	}
	ctx["auth"] = s.Rule.Auth.templateContext()
	ctx["variables"] = s.VariablesContext()
	ctx["chain"] = s.Rule.History
	ctx["state"] = s
	return ctx
}

func (a RuleAuthState) templateContext() map[string]any {
	input := a.Input
	if input == nil {
		input = map[string]any{}
	}
	forward := a.Forward
	if forward == nil {
		forward = map[string]any{}
	}
	return map[string]any{
		"selected": a.Selected,
		"input":    input,
		"forward":  forward,
	}
}

func (s *State) VariablesContext() map[string]any {
	if s == nil {
		return map[string]any{
			"endpoint":    map[string]any{},
			"local":       map[string]any{},
			"rule":        map[string]any{},
			"environment": map[string]string{},
			"secrets":     map[string]string{},
		}
	}
	endpoint := cloneAnyMap(s.Variables.Global)
	local := cloneAnyMap(s.Rule.Variables.Local)
	rules := make(map[string]any, len(s.Variables.Rules))
	for name, vars := range s.Variables.Rules {
		rules[name] = cloneAnyMap(vars)
	}
	environment := cloneStringMap(s.Variables.Environment)
	secrets := cloneStringMap(s.Variables.Secrets)
	return map[string]any{
		"endpoint":    endpoint,
		"local":       local,
		"rule":        rules,
		"environment": environment,
		"secrets":     secrets,
	}
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

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
