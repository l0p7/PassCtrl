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

// RawState preserves the inbound request snapshot for auditing and template
// evaluation.
type RawState struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Host    string            `json:"host"`
	Headers map[string]string `json:"headers"`
	Query   map[string]string `json:"query"`
}

// AdmissionState records authentication and proxy policy decisions.
type AdmissionState struct {
	Authenticated bool           `json:"authenticated"`
	Reason        string         `json:"reason,omitempty"`
	CapturedAt    time.Time      `json:"capturedAt"`
	ClientIP      string         `json:"clientIp,omitempty"`
	TrustedProxy  bool           `json:"trustedProxy"`
	ProxyStripped bool           `json:"proxyStripped"`
	ForwardedFor  string         `json:"forwardedFor,omitempty"`
	Forwarded     string         `json:"forwarded,omitempty"`
	ProxyNote     string         `json:"proxyNote,omitempty"`
	Decision      string         `json:"decision"`
	Snapshot      map[string]any `json:"snapshot,omitempty"`
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
}

// RuleHistoryEntry records the result of a single rule within the chain.
type RuleHistoryEntry struct {
	Name     string        `json:"name"`
	Outcome  string        `json:"outcome"`
	Reason   string        `json:"reason,omitempty"`
	Duration time.Duration `json:"duration"`
}

// ResponseState is the HTTP response composed for the caller.
type ResponseState struct {
	Status  int               `json:"status"`
	Message string            `json:"message"`
	Headers map[string]string `json:"headers"`
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

// State is the shared context threaded through every agent in the pipeline.
type State struct {
	cacheKey string
	plan     any

	Endpoint      string `json:"endpoint"`
	CorrelationID string `json:"correlationId"`

	Server    ServerState    `json:"server"`
	Raw       RawState       `json:"rawState"`
	Admission AdmissionState `json:"admission"`
	Forward   ForwardState   `json:"forwardRequest"`
	Rule      RuleState      `json:"rule"`
	Response  ResponseState  `json:"response"`
	Cache     CacheState     `json:"cache"`
	Backend   BackendState   `json:"backend"`
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
		Raw: RawState{
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
	ctx := map[string]any{
		"endpoint":      s.Endpoint,
		"correlationId": s.CorrelationID,
		"server":        s.Server,
		"raw":           s.Raw,
		"admission":     s.Admission,
		"forward":       s.Forward,
		"rule":          s.Rule,
		"response":      s.Response,
		"cache":         s.Cache,
		"backend":       s.Backend,
	}
	ctx["state"] = s
	return ctx
}
