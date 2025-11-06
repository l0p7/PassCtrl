# PassCtrl Codebase Alignment Assessment

**Overall Assessment: GOOD ALIGNMENT WITH SIGNIFICANT DOCUMENTATION GAPS**

**Alignment Score: 78/100**

The implementation is well-structured and implements most stated features correctly. However, there are notable documentation discrepancies that could confuse developers and users.

---

## CRITICAL DOCUMENTATION DISCREPANCY

### Issue: CLAUDE.md States 7 Agents, Design Docs and Code Show 8+ Agents

**CLAUDE.md (lines 94-104) lists SEVEN agents:**
1. Server Configuration & Lifecycle
2. Admission & Raw State  
3. Forward Request Policy
4. Rule Chain
5. Rule Execution
6. Response Policy
7. Result Caching

**design/system-agents.md documents EIGHT agents:**
1. Server Configuration & Lifecycle
2. Admission & Raw State  
3. Forward Request Policy
4. Rule Chain
5. Rule Execution
6. Backend Interaction (MISSING FROM CLAUDE.md) ← NEW AGENT
7. Response Policy
8. Result Caching

**PLUS undocumented in both CLAUDE.md and system-agents.md:**
9. Endpoint Variables Agent (endpointvars package)

**Actual Implementation in code shows 9+ agents:**
- `internal/runtime/server_agent.go` - serverAgent (implicit, manages config/lifecycle)
- `internal/runtime/admission/agent.go` - Admission & Raw State
- `internal/runtime/forwardpolicy/agent.go` - Forward Request Policy  
- `internal/runtime/rulechain/agent.go` - Rule Chain (orchestration)
- `internal/runtime/rule_execution_agent.go` - Rule Execution
- `internal/runtime/backend_interaction_agent.go` - Backend Interaction (MISSING FROM CLAUDE.md)
- `internal/runtime/responsepolicy/agent.go` - Response Policy
- `internal/runtime/resultcaching/agent.go` - Result Caching
- `internal/runtime/endpointvars/agent.go` - Endpoint Variables (UNDOCUMENTED)
- `internal/runtime/instrumentation.go` - instrumentedAgent wrapper

**Root Cause:** The Backend Interaction Agent was separated from Rule Execution Agent (see commit 2230e99, design/backend-agent-separation.md). CLAUDE.md was not updated to reflect this architectural change. Additionally, the Endpoint Variables agent exists but is not mentioned in CLAUDE.md at all.

---

## VERIFIED FEATURES

### 1. Agent Architecture & Separation of Concerns ✓

**Status: IMPLEMENTED CORRECTLY (with caveat)**

All agents have clear separation of concerns:
- Each agent has its own package with an Agent struct implementing pipeline.Agent interface
- Agent interface: `Name() string` and `Execute(context.Context, *http.Request, *State) Result`
- Agents are wrapped with instrumentation layer (instrumentedAgent) for consistent observability
- Agent pipeline orchestrated in runtime.go via endpointRuntime.agents slice

**Code Location:** `internal/runtime/pipeline/state.go` defines the Agent interface

### 2. Credential Matching (Match Groups with AND/OR Logic) ✓

**Status: FULLY IMPLEMENTED**

- Match groups support ordered evaluation with OR logic between groups, AND within groups
- Each group has `Match []AuthMatcherSpec` (AND logic) and `ForwardAs []AuthForwardSpec`
- Matcher types: bearer, basic, header, query, none
- Value constraints support literal strings OR regex patterns (delimited by `/`)
- Compiled in `internal/runtime/rulechain/auth.go`

**Key Code:**
```go
// AuthDirectiveSpec captures the declarative rule authentication directive (match group)
type AuthDirectiveSpec struct {
    Match     []AuthMatcherSpec    // All must match (AND)
    ForwardAs []AuthForwardSpec
}

// Compiled matchers include regex and literal support
type valueMatcher struct {
    literal string
    regex   *regexp.Regexp
}
```

**Evidence:** integration tests in cmd/integration_test.go cover complex match groups

### 3. Multi-Format Credential Emission ✓

**Status: FULLY IMPLEMENTED**

- Credentials can be emitted as bearer token, basic auth (user/password), header, query, or none
- Same credential can be emitted in multiple formats simultaneously via multiple forwardAs entries
- Pass-through mode supported when forwardAs is omitted (reconstructs from matched credentials)

**Code:** `internal/runtime/rule_execution_agent.go:buildForwards()` and `buildPassThroughForwards()`

### 4. Null-Copy Semantics ✓

**Status: IMPLEMENTED (for forward request policy)**

Forward request policy header/query handling uses null-copy semantics:
- `nil` value copies from raw request
- Non-nil value uses static string or template expression

**Code Location:** `internal/runtime/forwardpolicy/agent.go`

Response headers also use null-copy semantics for Response Policy agent.

### 5. Template Sandboxing with Path Traversal Protection ✓

**Status: FULLY IMPLEMENTED**

- Sandbox enforces that template paths must be within `server.templates.templatesFolder`
- Path traversal attempts are rejected (e.g., `../../../etc/passwd`)
- Environment variable access guarded by `templatesAllowEnv` + `templatesAllowedEnv` allowlist

**Code Location:** `internal/templates/sandbox.go`

**Key Method:**
```go
func (s *Sandbox) Resolve(relativePath string) (string, error)
    // Resolves path relative to sandbox root
    // Rejects path traversal attempts
```

### 6. CEL Expression Evaluation ✓

**Status: FULLY IMPLEMENTED**

- CEL programs compiled at config load time
- Supports whenAll, failWhen, errorWhen condition blocks  
- Variables available to CEL include backend responses, matched credentials, scoped variables
- Hybrid evaluator supports both CEL and Go templates (auto-detected by presence of `{{`)

**Code Location:** `internal/expr/hybrid.go`

### 7. Hot-Reload with Cache Invalidation ✓

**Status: FULLY IMPLEMENTED**

- Configuration changes trigger Reload(ctx, bundle) method
- Reload purges decision cache using DeletePrefix with cache namespace/epoch
- Cache ReloadInvalidator interface allows custom backends to handle reload semantics
- Config watcher via fsnotify in config/watch.go

**Code Location:** `internal/runtime/runtime.go:Reload()` (lines 670-698)

### 8. Structured Logging & Correlation IDs ✓

**Status: FULLY IMPLEMENTED**

- All agents emit logs via slog with consistent fields
- Correlation ID propagated from configured header through pipeline
- Log attributes include: component, agent, status, outcome, latency_ms, endpoint, correlation_id
- Request and decision snapshots logged at debug level

**Code Location:** `internal/runtime/runtime.go` (lines 138-245)

### 9. Prometheus Metrics ✓

**Status: FULLY IMPLEMENTED**

- Metrics exposed on /metrics endpoint
- Tracks: auth requests (counters), auth latency (histograms), cache operations
- Per-endpoint metrics with outcome labels (pass/fail/error)
- Cache hits vs misses tracked

**Code Location:** `internal/metrics/metrics.go`

### 10. Caching Invariants ✓

**Status: FULLY IMPLEMENTED**

Decision cache stores only metadata, never backend bodies:
- Entry stores: Decision (pass/fail), Response (status, headers), stored_at, expires_at
- Error outcomes never cached (line 53-58 in resultcaching/agent.go)
- Backend bodies are streamed and discarded after evaluation
- Separate pass/fail TTLs supported via rule config
- 5xx responses not cached

**Code:** `internal/runtime/cache/decision_cache.go` defines Entry structure

### 11. Response Policy with Exported Variables ✓

**Status: FULLY IMPLEMENTED**

- Response templates have access to exported variables via `.response.*` context
- Local variables (temporary calculations) are NOT exposed to response templates
- Endpoint owns response format entirely (status, headers, body templates)
- X-PassCtrl-Outcome header automatically added with rule outcome

**Code Location:** `internal/runtime/responsepolicy/agent.go`

### 12. Backend Pagination ✓

**Status: FULLY IMPLEMENTED**

- Link-header pagination per RFC 5988
- Max pages and visited URL tracking prevents loops
- Pagination results available to CEL conditions
- Fully implemented in Backend Interaction Agent

**Code Location:** `internal/runtime/backend_interaction_agent.go` and `internal/runtime/rulechain/backend.go`

### 13. Variable Scoping (Global, Rule, Local) ✓

**Status: FULLY IMPLEMENTED**

- Global variables: endpoint-level (evaluated once per request)
- Rule variables: exported via responses.pass/fail/error.variables (shared with subsequent rules)
- Local variables: temporary (variables block, not exported)
- State.Variables.Global tracks endpoint-scoped variables
- Rule-exported variables via state.Rule.Variables

**Code:** `internal/runtime/pipeline/state.go` defines VariablesState and RuleVariableState

---

## MINOR ISSUES FOUND

### 1. Endpoint Variables Agent Completely Undocumented ✗

**Severity: Medium**

- Package exists at `internal/runtime/endpointvars/`
- Agent implemented correctly (evaluates endpoint.variables block)
- NOT mentioned in CLAUDE.md
- NOT mentioned in design/system-agents.md
- Not in any design documentation

**Impact:** Developers reading design docs won't know this agent exists

**Code Evidence:** Agent at line 14-90 in internal/runtime/endpointvars/agent.go

### 2. Missing Test Coverage for Endpoint Variables Agent ✗

**Severity: Low**

- endpointvars package has [no test files]
- All other agents have comprehensive tests
- Suggests the agent may be newer and tests not yet added

### 3. Backend Interaction Agent Not Mentioned in CLAUDE.md ✗

**Severity: Medium**

- Agent properly implemented and separated from Rule Execution Agent (good architecture)
- Documented in design/backend-agent-separation.md
- CLAUDE.md still lists 7 agents without Backend Interaction
- CLAUDE.md Rule Execution agent description (line 102) says "backend calls" as if still together

**Impact:** Developer reading CLAUDE.md will have incorrect mental model of agent responsibilities

### 4. Server Agent Not Fully Documented ✗

**Severity: Low**

- serverAgent exists in internal/runtime/server_agent.go (manages config/lifecycle)
- Not explicitly listed as Agent 1 in either CLAUDE.md or design/system-agents.md
- design/system-agents.md describes it but as "Server Configuration & Lifecycle Agent" without explicit serverAgent implementation reference

---

## ARCHITECTURAL STRENGTHS

1. **Clean Agent Boundaries**: Each agent has single responsibility, clear inputs/outputs
2. **Testability**: Agents are mockable via pipeline.Agent interface; comprehensive test coverage
3. **Observability**: Instrumentation layer wraps all agents; consistent logging
4. **Security**: Template sandboxing, credential stripping (fail-closed), path traversal protection
5. **Extensibility**: Cache backends pluggable (memory, Redis/Valkey), new backends can implement ReloadInvalidator
6. **Performance**: Null-copy semantics for headers, streaming response bodies, separate pass/fail TTLs
7. **Configuration Management**: Hot-reload with cache invalidation, validation at load time

---

## MISSING/INCOMPLETE FEATURES

### None found that contradict stated intent

All major features documented in CLAUDE.md and design docs are implemented. No missing implementations detected.

---

## RECOMMENDATIONS FOR IMPROVEMENT

### Immediate (High Priority)

1. **Update CLAUDE.md to list 8 agents, not 7**
   - Add Backend Interaction Agent to line 94+ numbered list
   - Update Rule Execution agent description to clarify it no longer handles HTTP execution
   - Add Endpoint Variables Agent as 5.5 or renumber

2. **Document Endpoint Variables Agent**
   - Add to design/system-agents.md as Agent 0 (pre-rule evaluation) or as a variant
   - Document in CLAUDE.md
   - Add test coverage

3. **Verify Backend Interaction Agent Docs**
   - Confirm design/backend-agent-separation.md is discoverable
   - Link from CLAUDE.md

### Medium Priority

4. **Add Endpoint Variables tests**
   - Create internal/runtime/endpointvars/*_test.go
   - Table-driven tests for variable evaluation with CEL and templates
   - Error cases (invalid CEL, template syntax errors)

5. **Update project overview memory**
   - Note the 8 agents (not 7)
   - Document the Endpoint Variables agent

### Low Priority (Nice to Have)

6. **Design document for agent orchestration pipeline**
   - Visualize agent execution order
   - Document how state flows through agents
   - Show where cache checks/stores happen

7. **Add example configurations** demonstrating each agent's features
   - Match groups with regex
   - Multi-format credential emission
   - Endpoint variables
   - Backend pagination

---

## CODE QUALITY OBSERVATIONS

**Positive:**
- All tests passing (go test ./...)
- golangci-lint clean
- Proper error handling with context wrapping
- Consistent naming conventions
- Good separation of concerns

**Areas for Consideration:**
- Some large methods (e.g., rule_execution_agent.go evaluateRule is complex)
- Template compilation happens at config load - good for perf but could fail late if bad template
- Backend pagination loop could theoretically infinite loop despite safeguards (paranoia, but documented)

---

## CONCLUSION

PassCtrl codebase **strongly aligns with stated intent** with 8+ specialized agents properly implemented. The architecture is sound, separation of concerns is clean, and all documented features work correctly.

**Primary concern:** Documentation is **out of sync with implementation** (7 agents claimed vs 8+ actual). This is correctable but important for developer onboarding.

**Recommended action:** Update CLAUDE.md in next changeset to reflect the 8-agent architecture and add Endpoint Variables Agent documentation.

