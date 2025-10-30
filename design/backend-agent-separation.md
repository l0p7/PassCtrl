# Backend Interaction Agent Separation

## Overview

This document captures the design rationale, architecture, and migration strategy for separating backend API execution from the Rule Execution Agent into a dedicated Backend Interaction Agent. This refactoring addresses the single-responsibility principle by isolating HTTP execution concerns from rule orchestration, condition evaluation, and caching logic.

## Motivation

### Problem Statement

The Rule Execution Agent (`internal/runtime/rule_execution_agent.go`) currently owns nine distinct responsibilities:

1. Credential matching against rule `auth` directives
2. Credential transformation via `forwardAs` templates
3. Backend request template rendering
4. **HTTP execution and pagination** ← Target for extraction
5. Per-rule cache checking
6. Per-rule cache storage
7. Variable evaluation (local/rule/global scopes)
8. Condition evaluation (CEL programs for pass/fail/error)
9. Response assembly and header merging

This concentration of concerns violates the single-responsibility principle, complicates testing, and obscures the distinct phases of rule evaluation.

### Benefits of Separation

1. **Single Responsibility**: Each agent has one clear purpose
   - Rule Execution Agent: Orchestrate rule evaluation logic (auth, conditions, caching, variables)
   - Backend Interaction Agent: Execute HTTP requests reliably with pagination

2. **Improved Testability**: Mock the backend agent to test rule logic without HTTP concerns; test pagination and error handling in isolation

3. **Clearer Observability**: Separate log/metric labels distinguish rule orchestration latency from network I/O latency

4. **Simplified Maintenance**: Changes to HTTP client behavior (timeouts, retries, connection pooling) don't touch rule evaluation code

5. **Future Extensibility**: Enables backend-level features (circuit breakers, retries, response streaming) without polluting rule logic

## Architecture

### Agent Responsibilities

#### Backend Interaction Agent (New)

**Single Purpose**: Execute pre-rendered HTTP requests to backend APIs with pagination support.

**Responsibilities**:
- Accept fully-rendered request descriptors (method, URL, headers, query, body)
- Execute HTTP requests via `httpDoer` interface with context deadlines
- Implement pagination protocols:
  - Link-header (RFC 5988) - currently implemented
  - Token-based - future
  - Cursor-based - future
- Parse JSON responses when content-type indicates JSON
- Normalize JSON numbers for consistent CEL evaluation
- Respect `acceptedStatuses` configuration
- Capture per-page state and aggregate pagination results
- Handle network errors, timeouts, malformed responses, oversized bodies
- Populate `state.Backend.*` with responses and errors
- Emit structured logs with `agent: "backend_interaction"` labels

**Explicit Non-Responsibilities** (stay in Rule Execution Agent):
- ❌ Template rendering
- ❌ Credential matching or transformation
- ❌ Cache checking or storage
- ❌ Condition evaluation (CEL)
- ❌ Variable extraction
- ❌ Policy decisions

#### Rule Execution Agent (Updated)

Retains all responsibilities except HTTP execution:

1. Credential matching (`prepareRuleAuth`)
2. Credential transformation (`buildForwardAuth`)
3. **Backend request template rendering** (`renderBackendRequest`) ← Stays here
4. Per-rule cache checking (`checkRuleCache`)
5. **Delegate to Backend Interaction Agent** ← New coordination
6. Per-rule cache storage (`storeRuleCache`)
7. Variable evaluation (`evaluateRuleVariables`, `evaluateExportedVariables`)
8. Condition evaluation (`evaluateConditions`, `evaluateFailConditions`, `evaluateErrorConditions`)
9. Response assembly (`applyRuleResponse`)

### Interface Contract

#### Input: Rendered Backend Request

The `renderedBackendRequest` struct (already exists) serves as the clean interface between agents:

```go
type renderedBackendRequest struct {
    Method  string
    URL     string
    Headers map[string]string
    Query   map[string]string
    Body    string
}
```

**Why This Interface Works**:
- Already separated from execution in current code (lines 52-58)
- Captures all template rendering output
- Enables cache key generation before HTTP execution
- No authentication, context, or state pollution

#### Backend Interaction Agent Interface

```go
type backendInteractionAgent struct {
    client  httpDoer
    logger  *slog.Logger
}

func (a *backendInteractionAgent) Execute(
    ctx context.Context,
    rendered renderedBackendRequest,
    backend rulechain.BackendDefinition,
    state *pipeline.State,
) error
```

**Parameters**:
- `ctx` - Request context with deadline
- `rendered` - Pre-rendered request from Rule Execution Agent
- `backend` - Backend configuration (accepted statuses, pagination)
- `state` - Pipeline state to populate with results

**Return**:
- `error` - Non-fatal errors (network, timeout, parse) stored in `state.Backend.Error`
- Fatal errors (nil state, context cancellation) returned directly

**State Mutations**:
Populates `state.Backend.*`:
- `Requested = true`
- `Status` - HTTP status code (last page)
- `Headers` - Response headers (last page)
- `Body` - Parsed JSON or nil (last page)
- `BodyText` - Raw response text (last page)
- `Accepted` - Whether last page status is accepted
- `Pages` - Array of all paginated responses
- `Error` - Error message if execution failed

### State Hand-Off Semantics

#### Before Backend Agent Call

Rule Execution Agent prepares:
1. `state.Rule.Auth.*` - Selected credential and forward auth
2. `rendered` - Fully-rendered backend request descriptor
3. Cache check complete (hit → skip backend call entirely)

#### Backend Agent Execution

Backend Interaction Agent:
1. Builds `*http.Request` from `rendered` + `backend` config
2. Executes HTTP request(s) with pagination
3. Parses responses and captures errors
4. Populates `state.Backend.*`
5. Returns (error only for fatal issues)

#### After Backend Agent Call

Rule Execution Agent:
1. Checks `state.Backend.Error` for non-fatal errors
2. Evaluates CEL error conditions (`errorWhen`) using backend state
3. Evaluates CEL fail conditions (`failWhen`) using backend state
4. Evaluates CEL pass conditions (`whenAll`) using backend state
5. Extracts variables from backend responses
6. Stores decision in cache (unless error outcome)

### Sequence Diagram

```
┌─────────────┐         ┌──────────────┐         ┌──────────────┐
│ Rule Chain  │         │ Rule Exec    │         │ Backend      │
│   Agent     │         │   Agent      │         │ Interaction  │
└──────┬──────┘         └──────┬───────┘         └──────┬───────┘
       │                       │                        │
       │ evaluateRule()        │                        │
       │──────────────────────>│                        │
       │                       │                        │
       │                  prepareRuleAuth()             │
       │                       ├──┐                     │
       │                       │<─┘                     │
       │                       │                        │
       │               renderBackendRequest()           │
       │                       ├──┐                     │
       │                       │<─┘                     │
       │                       │                        │
       │                  checkRuleCache()              │
       │                       ├──┐                     │
       │                       │<─┘ (miss)              │
       │                       │                        │
       │                       │ Execute(rendered)      │
       │                       │───────────────────────>│
       │                       │                        │
       │                       │                   buildHTTPRequest()
       │                       │                        ├──┐
       │                       │                        │<─┘
       │                       │                        │
       │                       │                   httpDoer.Do()
       │                       │                        ├──┐
       │                       │                        │<─┘
       │                       │                        │
       │                       │                   parsePage()
       │                       │                        ├──┐
       │                       │                        │<─┘
       │                       │                        │
       │                       │            [pagination loop if needed]
       │                       │                        │
       │                       │                   populateState()
       │                       │                        ├──┐
       │                       │                        │<─┘
       │                       │                        │
       │                       │<───────────────────────┤
       │                       │    (state.Backend.*)   │
       │                       │                        │
       │              evaluateRuleVariables()           │
       │                       ├──┐                     │
       │                       │<─┘                     │
       │                       │                        │
       │              evaluateConditions()              │
       │                       ├──┐                     │
       │                       │<─┘                     │
       │                       │                        │
       │                  storeRuleCache()              │
       │                       ├──┐                     │
       │                       │<─┘                     │
       │                       │                        │
       │<──────────────────────┤                        │
       │   (outcome, vars)     │                        │
       │                       │                        │
```

## Implementation Details

### Code Extraction

#### New File: `internal/runtime/backend_interaction_agent.go`

**Extract from `rule_execution_agent.go`**:
- `invokeBackend()` (lines 628-767) → `Execute()` method
- `buildHTTPRequest()` helper (embedded in `invokeBackend`)
- Pagination loop logic
- JSON parsing logic

**New Structure**:
```go
type backendInteractionAgent struct {
    client httpDoer
    logger *slog.Logger
}

func newBackendInteractionAgent(client httpDoer, logger *slog.Logger) *backendInteractionAgent {
    return &backendInteractionAgent{
        client: client,
        logger: logger,
    }
}

func (a *backendInteractionAgent) Execute(
    ctx context.Context,
    rendered renderedBackendRequest,
    backend rulechain.BackendDefinition,
    state *pipeline.State,
) error
```

#### Updated File: `internal/runtime/rule_execution_agent.go`

**Add Field**:
```go
type ruleExecutionAgent struct {
    backendAgent  *backendInteractionAgent  // NEW
    client        httpDoer                  // REMOVE (moved to backendAgent)
    logger        *slog.Logger
    renderer      *templates.Renderer
    ruleEvaluator *expr.HybridEvaluator
    cacheBackend  cache.DecisionCache
    serverMaxTTL  time.Duration
    metrics       metrics.Recorder
}
```

**Update Constructor**:
```go
func newRuleExecutionAgent(
    backendAgent *backendInteractionAgent,  // NEW parameter
    logger *slog.Logger,
    renderer *templates.Renderer,
    ruleEvaluator *expr.HybridEvaluator,
    cacheBackend cache.DecisionCache,
    serverMaxTTL time.Duration,
    metrics metrics.Recorder,
) *ruleExecutionAgent {
    return &ruleExecutionAgent{
        backendAgent:  backendAgent,  // NEW
        // client removed
        logger:        logger,
        renderer:      renderer,
        ruleEvaluator: ruleEvaluator,
        cacheBackend:  cacheBackend,
        serverMaxTTL:  serverMaxTTL,
        metrics:       metrics,
    }
}
```

**Update `evaluateRule()`**:
```go
// Before: a.invokeBackend(ctx, rendered, def.Backend, state)
// After:
if def.Backend.IsConfigured() {
    if err := a.backendAgent.Execute(ctx, rendered, def.Backend, state); err != nil {
        // Only fatal errors return here; backend errors stored in state.Backend.Error
        return "error", "", nil
    }
}
```

**Remove**:
- `invokeBackend()` method (lines 628-767)
- Helper functions embedded in `invokeBackend`

#### Updated File: `internal/runtime/pipeline/pipeline.go`

**Update Pipeline Construction**:
```go
// Create backend interaction agent (shared by all rules)
backendAgent := newBackendInteractionAgent(
    &http.Client{Timeout: 10 * time.Second},
    logger,
)

// Inject into rule execution agent
ruleAgent := newRuleExecutionAgent(
    backendAgent,  // NEW parameter
    logger,
    renderer,
    evaluator,
    cacheBackend,
    serverMaxTTL,
    metrics,
)
```

### Observability Strategy

#### Logging

**Backend Interaction Agent**:
```go
logger := a.logger.With(
    slog.String("agent", "backend_interaction"),
    slog.String("endpoint", state.Endpoint),
    slog.String("correlation_id", state.CorrelationID),
    slog.String("rule", ruleName),
)
```

**Log Events**:
- `backend_request_start` - Method, URL, headers count
- `backend_request_complete` - Status, latency, body size
- `backend_pagination_next` - Page number, next URL
- `backend_error` - Network errors, timeouts, parse failures

**Rule Execution Agent** (existing):
- `rule_evaluation_start`
- `cache_hit` / `cache_miss`
- `condition_evaluation`
- `rule_evaluation_complete`

#### Metrics

**New Metrics** (future enhancement):
- `passctrl_backend_requests_total{endpoint, rule, method, status}` - Counter
- `passctrl_backend_request_duration_seconds{endpoint, rule}` - Histogram
- `passctrl_backend_pages_fetched{endpoint, rule}` - Histogram
- `passctrl_backend_errors_total{endpoint, rule, error_type}` - Counter

**Existing Metrics** (unchanged):
- `passctrl_cache_lookups_total`
- `passctrl_cache_stores_total`

### Testing Strategy

#### Unit Tests: `backend_interaction_agent_test.go`

**Test Cases**:

1. **Basic HTTP Execution**
   - Mock `httpDoer` returns 200 OK
   - Verify `state.Backend.*` populated correctly
   - Verify `Accepted = true` when status in `acceptedStatuses`

2. **JSON Parsing**
   - Content-Type: `application/json` triggers parsing
   - Numbers normalized (JSON float → Go float64)
   - Malformed JSON handled gracefully

3. **Pagination - Link Header**
   - Follow `Link: <url>; rel="next"` headers
   - Respect `maxPages` limit
   - Detect visited URL loops
   - Capture all pages in `state.Backend.Pages`

4. **Error Handling**
   - Network errors → `state.Backend.Error` populated
   - Timeouts → `state.Backend.Error = "context deadline exceeded"`
   - Oversized bodies (>1MB) → truncated with error
   - Invalid response → `state.Backend.Error` populated

5. **Accepted Statuses**
   - Status 200 with `acceptedStatuses: [200]` → `Accepted = true`
   - Status 401 with `acceptedStatuses: [200]` → `Accepted = false`
   - Empty `acceptedStatuses` defaults to `[200]`

6. **Context Cancellation**
   - Cancelled context → immediate return with error
   - No state mutation after cancellation

#### Integration Tests: `rule_execution_agent_test.go`

**Updates Required**:

1. **Mock Backend Agent**
   - Create `mockBackendInteractionAgent` with `Execute()` method
   - Return success/error/timeout scenarios
   - Verify rule agent handles backend errors correctly

2. **Cache Integration**
   - Cache hit → backend agent not called
   - Cache miss → backend agent called
   - Error outcome → cache not stored

3. **Condition Evaluation**
   - Backend error → `errorWhen` condition triggered
   - Backend 401 → `failWhen` condition triggered
   - Backend 200 → `whenAll` condition passed

#### End-to-End Tests

Run existing test suite with real examples:
```bash
go test ./... -v
```

Test with example configurations:
- `examples/basic/config.yaml`
- `examples/pagination/config.yaml`
- `examples/caching/config.yaml`

## Migration Steps

### Phase 1: Create Backend Agent (No Behavior Change)

1. Copy `invokeBackend()` → `backend_interaction_agent.go` as `Execute()`
2. Add `backendInteractionAgent` struct
3. Add unit tests

**Verification**: Tests pass, but backend agent not yet used.

### Phase 2: Integrate Backend Agent

1. Update `ruleExecutionAgent` to include `backendAgent` field
2. Update constructor to accept `backendAgent` parameter
3. Update `pipeline.go` to create and inject backend agent
4. Replace `a.invokeBackend()` calls with `a.backendAgent.Execute()`
5. Remove old `invokeBackend()` method

**Verification**: `go test ./...` passes with no regressions.

### Phase 3: Enhance Testing

1. Create comprehensive backend agent tests
2. Update rule agent tests to use mocked backend agent
3. Verify cache integration still works

**Verification**: Test coverage maintained/improved.

### Phase 4: Documentation

1. Update `design/system-agents.md` ✅ (complete)
2. Create `design/backend-agent-separation.md` ✅ (this document)
3. Update `design/request-flows.md` with new agent sequences

**Verification**: Design docs synchronized with code.

## Backwards Compatibility

### Configuration

**No Changes Required**: The configuration schema remains identical. Users continue to configure:

```yaml
rules:
  - name: example
    backendApi:
      url: https://api.example.com
      method: GET
      acceptedStatuses: [200, 201]
      pagination:
        type: link-header
        maxPages: 5
```

### Behavior

**Identical Request Handling**: The separation is purely internal. Request flows, caching, condition evaluation, and responses remain identical.

### Observability

**Enhanced Logs**: Separate `agent` labels distinguish backend execution from rule evaluation, improving debugging without breaking existing dashboards.

## Future Enhancements

With backend interaction isolated, future features become easier:

1. **Circuit Breakers**: Detect failing backends, open circuit to fail fast
2. **Retry Logic**: Configurable retry strategies (exponential backoff, jitter)
3. **Connection Pooling**: Optimize HTTP client per backend
4. **Request Batching**: Combine multiple backend calls when backends support it
5. **Response Streaming**: Stream large backend responses without buffering
6. **Token-Based Pagination**: Support OAuth pagination tokens
7. **Cursor-Based Pagination**: Support GraphQL-style cursors
8. **Parallel Requests**: Call multiple backends concurrently within a rule

All of these can be added to the Backend Interaction Agent without touching rule evaluation logic.

## Summary

The Backend Interaction Agent separation achieves:

✅ **Single Responsibility**: Each agent has one clear purpose
✅ **Improved Testability**: Isolated HTTP concerns from rule logic
✅ **Zero Configuration Changes**: Users see no differences
✅ **Zero Behavioral Changes**: Request handling identical
✅ **Enhanced Observability**: Separate agent labels improve debugging
✅ **Future Extensibility**: Backend features isolated from rule evaluation

This refactoring upholds PassCtrl's design principles while improving code organization and maintainability.
