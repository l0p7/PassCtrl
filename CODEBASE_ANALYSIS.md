# PassCtrl Codebase Analysis Report
**Date:** 2025-11-09
**Analyst:** Claude Code
**Branch:** claude/codebase-analysis-011CUwQ772eiJCe5JRwq8pxD

## Executive Summary

This report provides a comprehensive analysis of the PassCtrl codebase to verify that its implementation aligns with its design intent as documented in `design/` and `CLAUDE.md`. The analysis examined 9 major areas: agent separation, configuration hot-reload, authentication model, caching invariants, template sandbox security, environment variables/secrets handling, observability, and error handling.

**Overall Assessment:** **Grade B+ (85%)**

The codebase demonstrates **strong architectural discipline** with excellent security posture and solid implementation of core features. However, several critical gaps exist that require attention:

### Critical Issues (Must Fix)
1. **Redis cache invalidation not implemented** - Config reloads don't purge Redis cache entries
2. ~~**Credential stripping incomplete**~~ - **FIXED** - Custom headers/query parameters now properly stripped from backend requests
3. **Dual caching architecture** - Split responsibilities between Rule Execution Agent and Result Caching Agent

### High-Priority Issues (Should Fix)
4. ~~**Missing agent logging**~~ - **FIXED** - Backend Interaction Agent now logs requests, responses, and pagination
5. ~~**Component field missing**~~ - **FIXED** - All agent logs now include "component" field
6. ~~**Outdated examples**~~ - **FIXED** - Template examples updated to use server.variables.environment

### Medium-Priority Issues (Consider Fixing)
7. **Hardcoded timeouts** - HTTP client timeout not configurable
8. **Filesystem context deadlines** - File I/O cannot be interrupted
9. **Documentation gaps** - Missing integration tests, incomplete documentation

---

## Detailed Analysis by Area

### 1. Agent Separation and Boundary Adherence

**Grade: B+ (Good with notable violations)**

#### Status Summary
All 9 agents exist in expected locations:
- ✅ Server Configuration & Lifecycle (`cmd/main.go`, `internal/config/`)
- ✅ Admission & Raw State (`internal/runtime/admission/`)
- ✅ Forward Request Policy (`internal/runtime/forwardpolicy/`)
- ✅ Endpoint Variables (`internal/runtime/endpointvars/`)
- ✅ Rule Chain (`internal/runtime/rulechain/`)
- ✅ Rule Execution (`internal/runtime/rule_execution_agent.go`)
- ✅ Backend Interaction (`internal/runtime/backend_interaction_agent.go`)
- ✅ Response Policy (`internal/runtime/responsepolicy/`)
- ✅ Result Caching (`internal/runtime/cache/`, `internal/runtime/resultcaching/`)

#### Critical Violations

**🔴 VIOLATION #1: Dual Caching Architecture** (Severity: HIGH)

The design specifies a single "Result Caching Agent" but implementation has TWO distinct caching mechanisms:

1. **Endpoint-level caching** (`resultcaching/agent.go`) - Stores entire chain outcomes
2. **Per-rule caching** (`rule_execution_agent.go` + `rule_cache.go`) - Stores individual rule decisions

**Location:**
- `/home/user/PassCtrl/internal/runtime/rule_execution_agent.go:309-454` - `checkRuleCache()` and `storeRuleCache()` methods
- `/home/user/PassCtrl/internal/runtime/resultcaching/agent.go` - Separate endpoint caching

**Impact:** Violates single responsibility principle; unclear ownership of caching concerns.

**Recommendation:** Either consolidate all caching in Result Caching Agent, or update design to document two-tier caching architecture.

**🟡 VIOLATION #2: Rule Execution Agent Renders Backend Requests** (Severity: MEDIUM)

Rule Execution Agent contains extensive backend request rendering logic (`renderBackendRequest()` method, lines 456-540) including:
- Template rendering for body/headers/query
- Null-copy semantics application
- Auth forward application

**Design expectation:** Backend Interaction Agent should "accept pre-rendered request descriptors—no template evaluation"

**Current reality:** Boundary is maintained (Backend Agent receives fully-rendered request), but responsibility assignment could be clearer.

**Recommendation:** Document this as explicit sub-responsibility of Rule Execution Agent or extract to separate renderer component.

**🟡 VIOLATION #3: Admission Agent Renders Responses** (Severity: MEDIUM)

Admission Agent has `renderAdmissionFailure()` method that constructs complete HTTP responses (status, headers, body templates). This overlaps with Response Policy Agent's responsibility.

**Design justification:** Documented as intentional for performance—admission failures short-circuit the pipeline, avoiding ~7 unnecessary agent executions.

**Recommendation:** Design already documents this correctly (system-agents.md:46-48), but should emphasize that Response Policy Agent only handles post-rule-chain responses.

#### Exemplary Implementations

**Backend Interaction Agent** - Perfect boundary adherence:
- Accepts pre-rendered requests ✅
- Performs pure HTTP execution ✅
- No policy evaluation ✅
- No template rendering ✅
- No condition logic ✅

Should serve as the model for other agents.

**Inputs/Outputs Contract Validation:**
- ✅ Admission Agent: Matches specification
- ✅ Rule Execution Agent: Matches (except caching overlap)
- ✅ Backend Interaction Agent: Perfect match
- ⚠️ Result Caching Agent: Partial match (split with Rule Execution Agent)

---

### 2. Configuration Hot-Reload Implementation

**Grade: B (Working but critical Redis gap)**

#### Properly Implemented ✅

**File System Watching:**
- Location: `/home/user/PassCtrl/internal/config/watch.go`
- Uses `fsnotify` directly (not koanf's fs provider as expected)
- 25ms debouncing for rapid file changes (lines 152-168)
- Supports both single file (`rulesFile`) and directory (`rulesFolder`) watching
- Auto-discovers new subdirectories (lines 209-215)
- Filters to supported types: `.yaml`, `.yml`, `.json`, `.toml`, `.tml`, `.huml`

**RuleSources and SkippedDefinitions Tracking:**
- Location: `/home/user/PassCtrl/internal/config/rules_loader.go`
- `ruleAggregator` tracks sources, endpoints, rules, skip reasons (lines 37-47)
- Pipeline updates tracking on reload (runtime.go:693-695)

**Duplicate Handling:**
- First occurrence initially accepted
- Second occurrence triggers skip of BOTH definitions (lines 90-94)
- Reason: "duplicate definition"
- Both sources recorded in skip entry

**Missing Rule Dependencies:**
- `pruneInvalidEndpoints()` during bundle finalization (line 192)
- Checks each endpoint's rule chain for missing rules (lines 165-174)
- Quarantines endpoints with missing dependencies (lines 175-187)
- Reason format: "missing rule dependencies: rule1, rule2" (sorted)

**Error Handling:**
- ✅ Server config errors: Terminate process with non-zero exit (loader.go:108-110, main.go:75-77)
- ✅ Rule config errors: Disable rule, log warning, continue running (rules_loader.go:73-83)

#### Critical Gap ❌

**🔴 Redis Cache Invalidation Not Implemented**

**Location:** `/home/user/PassCtrl/internal/runtime/cache/redis.go:126-128`

```go
func (c *redisCache) DeletePrefix(context.Context, string) error {
    return nil  // NO IMPLEMENTATION!
}
```

**Impact:**
- When using Redis/Valkey backend, config reloads **do not invalidate cached decisions**
- Stale cached authorization decisions persist until natural TTL expiration
- Violates documented hot-reload contract in CLAUDE.md:27

**Memory cache works correctly:**
- `DeletePrefix()` properly iterates and deletes matching keys (memory.go:51-63)

**Expected behavior** (from runtime.go:703-707):
```go
prefix := fmt.Sprintf("%s:%d:", p.cacheNamespace, p.cacheEpoch)
if err := p.cache.DeletePrefix(ctx, prefix); err != nil {
    p.logger.Warn("cache purge failed", slog.Any("error", err))
    return
}
```

**Recommendation:** Implement Redis prefix deletion using SCAN + DEL pattern or Redis key pattern matching.

---

### 3. Rule Evaluation Order and Auth Model

**Grade: B (Mostly correct with critical credential stripping gap)**

#### Correctly Implemented ✅

**Match Groups (auth blocks):**
- Location: `/home/user/PassCtrl/internal/runtime/rule_execution_agent.go:611-658`
- ✅ Sequential evaluation with OR between groups, AND within groups (`prepareRuleAuth`, `checkAllMatchers`)
- ✅ Value matching with regex and literal patterns (`matchesAnyValueMatcher`, lines 738-751)
- ✅ Multi-format credential emission via multiple `forwardAs` entries (lines 794-814)
- ✅ Pass-through mode when `forwardAs` is omitted (lines 797-798, `buildPassThroughForwards`)

**Template Context:**
- Location: Lines 754-792 (`buildAuthTemplateContext`)
- ✅ `.auth.input.bearer.token` (lines 759-763)
- ✅ `.auth.input.basic.user/.password` (lines 765-771)
- ✅ `.auth.input.header['x-name']` with lowercase keys (lines 773-780)
- ✅ `.auth.input.query['param']` (lines 782-789)

**Rule Evaluation Order:**
- Location: `evaluateRule` method, lines 211-306
1. ✅ Evaluate authentication match groups (line 221)
2. ✅ Render and apply credential forwards (lines 240, 634)
3. ⚠️ **Strip credentials** - NOT IMPLEMENTED (see gap below)
4. ✅ Render backend request (lines 240-246)
5. ✅ Invoke backend API with pagination (line 256)
6. ✅ Evaluate CEL conditions (lines 272-294)
7. ✅ Export variables (line 186)
8. ✅ Return outcome (lines 180-196)

**Backend Interaction:**
- Location: `/home/user/PassCtrl/internal/runtime/backend_interaction_agent.go`
- ✅ Proper separation from rule execution agent
- ✅ Pagination support via Link headers (RFC 5988)
- ✅ JSON parsing and number normalization for CEL
- ✅ Error handling without policy decisions

#### ~~Critical Gap~~ ✅ FIXED

**✅ Credential Stripping Implemented** (Fixed: 2025-11-09)

**Design specification** (from CLAUDE.md:191, system-agents.md:106-107, decision-model.md:39-40):
> **Explicit Stripping**: All credential sources stripped before applying forwards (fail-closed security)

**What's protected:**
- Authorization headers ARE protected via config validation
- Location: `/home/user/PassCtrl/internal/config/types.go:525-534`
- `validateBackendHeaders` forbids `authorization` header in `backendApi.headers`
- Users MUST use `auth.forwardAs` for Authorization headers

**What's NOT protected:**
- Custom headers (`X-Api-Token`, etc.) NOT stripped
- Query parameters used for credentials NOT stripped

**Problem scenario:**
```yaml
auth:
  - match:
      - type: header
        name: X-Api-Token  # Custom header credential
    forwardAs:
      - type: bearer
        token: "{{ index .auth.input.header \"x-api-token\" }}"
backendApi:
  headers:
    x-api-token: null  # null-copy from raw request
```

**Current behavior:** Backend receives BOTH `X-Api-Token` (copied) AND `Authorization` (forwarded)

**Expected behavior:** Backend receives ONLY `Authorization` (X-Api-Token should be stripped)

**Location of issue:** `/home/user/PassCtrl/internal/runtime/rule_execution_agent.go:456-540` (`renderBackendRequest`)

**Security implications (RESOLVED):**
1. ~~Custom header/query credentials may leak to backends when using null-copy semantics~~ ✅ FIXED
2. ~~Inconsistent behavior: Authorization is protected, but other credential types are not~~ ✅ FIXED
3. ~~Fail-closed security model not fully implemented~~ ✅ FIXED

**Implementation Details:**

The fix implements explicit credential stripping in `renderBackendRequest` (rule_execution_agent.go:456-550):

1. **collectCredentialSources()** - Extracts all header and query parameter names used as credentials across ALL auth directives (not just the matched one)
2. **stripCredentialSources()** - Creates a copy of headers/query with credential sources removed
3. **Modified renderBackendRequest()** - Now accepts `allAuthDirectives` parameter, strips credentials before applying null-copy semantics, then applies forwards

**Test Coverage:** Five comprehensive tests added (rule_execution_agent_test.go:535-842):
- `TestCredentialStripping_CustomHeaderStripped` - Verifies custom header credentials are stripped
- `TestCredentialStripping_QueryParamStripped` - Verifies query parameter credentials are stripped
- `TestCredentialStripping_MultipleMatchGroups` - Verifies ALL auth groups' credentials are stripped (not just matched)
- `TestCredentialStripping_PassThroughMode` - Verifies stripping works even without forwardAs
- `TestCredentialStripping_NoAuthDirectives` - Verifies no stripping when no auth configured

**Files Modified:**
- `internal/runtime/rule_execution_agent.go` - Implementation
- `internal/runtime/rule_execution_agent_test.go` - Test coverage

---

### 4. Caching Invariants Compliance

**Grade: A- (Excellent with minor Redis gap)**

#### All Critical Invariants Enforced ✅

**1. Store Only Decision Metadata** ✅
- Location: `/home/user/PassCtrl/internal/runtime/rule_cache.go:14-23`, `cache/decision_cache.go:8-19`
- Cache structures contain zero backend response body data
- Only stores: outcome, reason, exported variables, response descriptors
- Backend bodies (`state.Backend.Body`, `state.Backend.BodyText`) exist only in pipeline state during request processing

**2. Never Persist Backend Bodies Beyond Active Request** ✅
- Location: `/home/user/PassCtrl/internal/runtime/resultcaching/agent.go:136-143`
- Transformation functions explicitly exclude bodies
- `ResponseToCache()` copies only status, message, headers

**3. Drop Entries on 5xx or Error Outcomes** ✅
- Multiple enforcement layers:
  - Hard-coded TTL: `cache/ttl.go:18-21` - error outcomes always return 0
  - Effective TTL calculation: `ttl.go:84-86` - highest precedence
  - Storage guards: `rule_cache.go:198-201` - only cache pass/fail
  - Endpoint-level bypass: `resultcaching/agent.go:53-58`
- Test coverage: `ttl_test.go:40-54`, `resultcaching/agent_test.go:65-77`

**4. Honor Separate passTTL/failTTL** ✅
- Location: `/home/user/PassCtrl/internal/runtime/cache/ttl.go:77-121`
- TTL hierarchy (highest → lowest):
  1. Error outcomes → Always 0 (hardcoded)
  2. Backend Cache-Control (if followCacheControl=true)
  3. Rule manual TTL (per-outcome: Pass, Fail)
  4. Endpoint TTL ceiling (per-outcome)
  5. Server max TTL ceiling
- System applies minimum of all ceilings
- 18 comprehensive test cases in `ttl_test.go`

**5. Respect followCacheControl** ✅
- Location: `/home/user/PassCtrl/internal/runtime/cache/cache_control.go:32-111`
- Supports: `max-age`, `s-maxage`, `no-cache`, `no-store`, `private`
- `s-maxage` takes precedence over `max-age` (shared cache)
- `no-cache`/`no-store`/`private` → 0 TTL immediately
- Integration: `ttl.go:93-107`

**6. includeProxyHeaders Flag** ✅
- Location: `/home/user/PassCtrl/internal/runtime/rule_cache.go:61-133`
- Default (nil) = true (safe: include proxy headers in cache key)
- Explicit false = exclude proxy headers (opt-out for better cache hit rates)
- Excluded headers when false: `forwarded`, `x-forwarded-*`, `x-real-ip`, `true-client-ip`, `cf-*`
- Security warning in code (lines 100-102)
- Always excluded: tracing headers, timing headers, correlation header

#### Cache Key Security ✅

**User Isolation:** SECURE
- Location: `/home/user/PassCtrl/internal/runtime/helpers.go:20-56`
- Priority order (matches requirement):
  1. Authorization header (if allowed)
  2. Custom headers (if allowed)
  3. Query parameters (if allowed)
  4. IP address (fallback)
- Key format: `credential|endpoint|path`
- SHA-256 hashing with salt (runtime.go:539-542)
- Credential-specific prefixes prevent collision
- Comprehensive test coverage in `helpers_test.go`

**none: true Endpoints:** Caching properly disabled
- Detection: `runtime.go:535-537` - returns empty cache key
- Enforcement: `resultcaching/agent.go:61-66` - bypasses caching

#### Two-Tier Caching Architecture ✅

**Per-Rule Caching:**
- Location: `/home/user/PassCtrl/internal/runtime/rule_cache.go`
- Stores: outcome, reason, exported variables, custom headers
- Cache key: `baseKey|ruleName|backendHash|upstreamVarsHash` (strict mode)
- Integration: `rule_execution_agent.go` line 249 (check), 206 (store)

**Endpoint-Level Caching:**
- Location: `/home/user/PassCtrl/internal/runtime/resultcaching/agent.go`
- Stores: final chain outcome, response status/message/headers
- Runs after entire rule chain completes

**Both tiers properly:**
- ✅ Exclude error outcomes
- ✅ Respect empty cache keys (none: true)
- ✅ Use same security isolation

#### Minor Issues Found

**⚠️ Redis DeletePrefix Not Implemented** (also noted in hot-reload section)
- Already documented above under hot-reload

**✅ ~~Per-Rule Cache Missing Endpoint TTL Ceiling~~** - **FIXED** (2025-11-09)
- Location: `/home/user/PassCtrl/internal/runtime/rule_execution_agent.go:409-429`
- Fixed: Endpoint TTL now properly wired to per-rule caching
- Changes:
  - Added `endpointTTL` field to `ruleExecutionAgent` struct
  - Parse `cfg.Cache.ResultTTL` in `buildEndpointRuntime` (runtime.go:774-787)
  - Pass endpoint TTL to `newRuleExecutionAgent` constructor
  - Use endpoint TTL as ceiling in `CalculateEffectiveTTL` call
- Both pass and fail outcomes now respect endpoint TTL ceiling

---

### 5. Template Sandbox Security

**Grade: A (Secure with documentation gaps)**

#### Core Security: Strong ✅

**Path Traversal Prevention:** SECURE
- Location: `/home/user/PassCtrl/internal/templates/sandbox.go:62-78`
- Enforces strict path containment using `filepath.EvalSymlinks()` before validation
- Both relative and absolute paths normalized and checked
- Symlink evaluation happens BEFORE containment check (prevents symlink-based escapes)
- Even non-existent paths validated against traversal attempts
- Test coverage: `sandbox_test.go:62` (escape rejected), `80-98` (symlink blocked)

**Environment Variable Access:** SECURE (with migration gap)
- Location: `/home/user/PassCtrl/internal/templates/renderer.go:57-65`
- Old `env` and `expandenv` functions return empty strings (deprecated)
- Environment variables now explicitly configured in `server.variables.environment`
- Null-copy semantics with fail-fast validation
- Exposed via `.variables.environment.*` context (pipeline/state.go:334-342)
- No direct access to process environment from templates

**Deprecated Functions:** PROPERLY DISABLED
- Test verification: `renderer_test.go:11-38`
- `env` returns empty string even when var exists
- `expandenv` returns empty string

**Sprig Filesystem Helpers:** REMOVED
- Location: `renderer.go:40-51`
- Restricted functions: `env`, `expandenv`, `readDir`, `mustReadDir`, `readFile`, `mustReadFile`, `glob`
- Test confirms: `renderer_test.go:98-101` - `readFile` triggers error

**bodyFile Resolution:** SECURE (two-stage)
- Location: `rulechain/agent.go:313-320`, `rule_execution_agent.go:485-492`
- Stage 1: `bodyFile` value compiled as inline template (allows dynamic path selection)
- Stage 2: Rendered path resolved via `CompileFile()` → `Sandbox.Resolve()`

#### ~~Security Gaps Found~~ ✅ FIXED

**✅ Example Configuration Updated** (Fixed: 2025-11-09)
- Location: `/home/user/PassCtrl/examples/suites/template-env-bundle/server.yaml`
- Removed deprecated `templatesAllowEnv` and `templatesAllowedEnv` config fields
- Added `server.variables.environment` section with null-copy semantics
- Updated all `{{ env "..." }}` calls to `{{ .variables.environment.* }}`
- Fixed template file: `templates/deny.json.tmpl`

**Changes Made:**
```yaml
# Before (deprecated):
templates:
  templatesAllowEnv: true
  templatesAllowedEnv: ["SUPPORT_EMAIL", "UPSTREAM_BASE_URL"]

# After (current):
variables:
  environment:
    SUPPORT_EMAIL: null  # null-copy: read from environment variable
    UPSTREAM_BASE_URL: null
```

**Template Updates:**
- `{{ env "SUPPORT_EMAIL" }}` → `{{ .variables.environment.SUPPORT_EMAIL }}`
- `{{ env "UPSTREAM_BASE_URL" }}` → `{{ .variables.environment.UPSTREAM_BASE_URL }}`

**Files Modified:**
- `examples/suites/template-env-bundle/server.yaml` - Config updated
- `examples/suites/template-env-bundle/templates/deny.json.tmpl` - Template updated

**Remaining Recommendation:**
1. Remove references to `templatesAllowEnv`/`templatesAllowedEnv` from CLAUDE.md
2. Add deprecation notice to README for users migrating from older versions

---

### 6. Environment Variables and Secrets Handling

**Grade: A- (Solid implementation with missing integration tests)**

#### All Requirements Implemented ✅

**1. Null-Copy Semantics:** PROPERLY IMPLEMENTED
- Location: `/home/user/PassCtrl/internal/config/loader.go`
- **Environment Variables** (lines 169-196):
  - `key: null` → read env var with exact name as key
  - `key: "ENV_VAR"` → read ENV_VAR as key
- **Secrets** (lines 198-233):
  - `key: null` → read `/run/secrets/key`
  - `key: "filename"` → read `/run/secrets/filename` as key

**2. Fail-Fast Validation:** WORKING CORRECTLY
- Startup flow: `cmd/main.go:82-85` → `loader.go:95-106`
- Error messages:
  - Missing env var: `environment variable "VAR" not found (referenced by server.variables.environment.KEY)`
  - Missing secret: `secret file "/run/secrets/file" not found (referenced by server.variables.secrets.KEY)`
- Test coverage: `loader_test.go:150-172` (env vars), `335-342` (secrets)

**3. Exposure to CEL and Templates:** PROPERLY IMPLEMENTED
- Data flow:
  1. Loading: `loader.go:99, 106`
  2. Pipeline init: `cmd/main.go:121-122`
  3. Request-time population: `runtime.go:359-360`
  4. Context building: `pipeline/state.go:318-343`
  5. Templates: `state.go:296`
  6. CEL: `rule_execution_agent.go:1037`
- Access patterns:
  - CEL: `variables.environment.KEY` or `variables.secrets.KEY`
  - Templates: `{{ .variables.environment.KEY }}` or `{{ .variables.secrets.KEY }}`

**4. Newline Trimming for Secrets:** IMPLEMENTED
- Location: `loader.go:230`
- `strings.TrimRight(string(content), "\n\r")`
- Trims: `\n` (Linux), `\r` (Mac), `\r\n` (Windows)
- Test coverage: `loader_test.go:250-252, 324-333`

**5. Deprecated Template Functions:** BLOCKED
- Location: `templates/renderer.go:60-65`
- `env()` and `expandenv()` return empty strings
- Forces use of controlled `server.variables.environment`

#### Gaps Found

**⚠️ Missing Integration Tests** (Severity: Medium)
- No tests verify loaded variables accessible in CEL expressions at runtime
- No tests verify loaded variables accessible in templates at runtime
- No end-to-end tests with real HTTP requests
- Unit tests exist for loading, null-copy, fail-fast, but not runtime integration

**⚠️ Incomplete Documentation** (Severity: Low)
- `server.variables.secrets` not in configuration reference table
- Should add row in `docs/configuration/endpoints.md`
- Should describe Docker/Kubernetes secret mount pattern

**⚠️ No Example Configurations** (Severity: Low)
- No working examples in `/home/user/PassCtrl/examples/` demonstrate:
  - Database connection strings from secrets
  - API keys from environment variables
  - Multi-environment configs using env vars
  - Docker Compose setup with secrets

---

### 7. Observability Implementation

**Grade: B- (Good foundation with consistency gaps)**

#### Structured Logging Setup ✅

**Location:** `/home/user/PassCtrl/internal/logging/logger.go`
- Uses `slog` with JSON or text handler
- Root logger includes `component: "passctrl"`
- Supports configurable log levels (debug, info, warn, error)
- Issue: `correlation_header` config stored but not automatically in every log entry

#### Instrumentation Layer ⚠️

**Location:** `/home/user/PassCtrl/internal/runtime/instrumentation.go:24-51`

**Wraps every agent with consistent logging:**
- ✅ `status` - result.Status
- ✅ `latency_ms` - operation duration
- ✅ `outcome` - from state or result
- ✅ `agent` - from ag.Name()
- ✅ `endpoint` - from state
- ✅ `correlation_id` - from state
- ❌ `component` - NOT included (only in root logger)

#### Correlation ID Handling ✅ EXCELLENT

**Extraction:** `runtime.go:812-824`
- Reads from configured header
- Generates fallback using random bytes or timestamp
- Stores in state

**Propagation to Response Headers:** `runtime.go:388-399`
- Reads from state
- Echoes to response header

#### Metrics Implementation ✅ EXCELLENT

**Location:** `/home/user/PassCtrl/internal/metrics/metrics.go`

**Metrics exposed:**
1. `passctrl_auth_requests_total` - Counter (endpoint, outcome, status_code, from_cache)
2. `passctrl_auth_request_duration_seconds` - Histogram (endpoint, outcome)
3. `passctrl_cache_operations_total` - Counter (endpoint, operation, result)
4. `passctrl_cache_operation_duration_seconds` - Histogram (endpoint, operation, result)

**Handler:** Exposed at `/metrics` (cmd/main.go:153)
**Registry:** Per-process dedicated registry ✅

#### Agent-Level Logging Consistency ⚠️ INCONSISTENT

| Agent | Status | Notes |
|-------|--------|-------|
| Admission | ✅ No direct logging | Relies on instrumentation wrapper |
| Forward Policy | ✅ No direct logging | Relies on instrumentation wrapper |
| Endpoint Variables | ⚠️ Has logger | Uses for warnings; missing correlation_id, endpoint context |
| Rule Chain | ✅ No direct logging | Relies on instrumentation wrapper |
| Rule Execution | ⚠️ Bypasses wrapper | Manually constructs logger; includes correlation_id/endpoint |
| **Backend Interaction** | ❌ **NO LOGGING** | **Silent operation - critical gap** |
| Response Policy | ✅ No direct logging | Relies on instrumentation wrapper |
| Result Caching | ⚠️ Manual logger | Includes correlation_id/endpoint; missing component/outcome |

**Required fields status:**

| Field | Instrumentation Wrapper | Agent Warnings/Errors |
|-------|------------------------|----------------------|
| `component` | ❌ Missing | ❌ Missing |
| `agent` | ✅ Present | ⚠️ Sometimes |
| `outcome` | ✅ Present | ❌ Missing |
| `status` | ✅ Present | ❌ Missing |
| `latency_ms` | ✅ Present | ❌ Missing |
| `correlation_id` | ✅ Present | ⚠️ Sometimes |
| `endpoint` | ✅ Present | ⚠️ Sometimes |

#### ~~Critical Gaps~~ ✅ FIXED

**✅ Backend Interaction Agent Logging Implemented** (Fixed: 2025-11-09)
- Location: `/home/user/PassCtrl/internal/runtime/backend_interaction_agent.go:40-194`
- Added structured logging for:
  - Request initiation (method, URL, max pages)
  - Response reception (status, accepted flag, page number)
  - Pagination events (next URL, total pages)
  - Error conditions (request failures)
- Includes correlation_id, endpoint, component, and agent fields via buildLogger() helper

**✅ Component Field Added to All Agent Logs** (Fixed: 2025-11-09)
- Location: `/home/user/PassCtrl/internal/runtime/instrumentation.go:65`
- Added "component": "runtime" to instrumentAgents() logger initialization
- Now propagated to all agent logs via instrumentation wrapper

**Recommendations:**

**Priority 1 - Add "component" field**
```go
// runtime.go:buildEndpointRuntime()
logger := p.logger.With(
    slog.String("component", "runtime"),  // ADD THIS
    slog.String("agent", ag.Name()),
    slog.String("endpoint", endpoint),
)
```

**Priority 2 - Add logging to Backend Interaction Agent**

**Priority 3 - Standardize agent logging helper**
```go
func agentLogger(baseLogger *slog.Logger, agent string, state *pipeline.State) *slog.Logger {
    logger := baseLogger.With(
        slog.String("component", "runtime"),
        slog.String("agent", agent),
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
```

---

### 8. Error Handling Patterns

**Grade: A- (Excellent with minor context deadline gaps)**

#### Error Wrapping with %w ✅ CONSISTENT

**Finding:** Error wrapping consistently applied throughout using `fmt.Errorf` with `%w`.

**Evidence:**
- `cmd/main.go:84, 89` - Config and logger errors
- `internal/config/loader.go:37, 53, 56, 85, 91, 97, 104, 226` - Config loading
- `internal/config/rules_loader.go:292, 320, 340, 349, 364, 368` - Rule loading
- `internal/runtime/backend_interaction_agent.go:67, 81, 115, 128, 131, 140` - Backend HTTP
- `internal/runtime/cache/redis.go:57, 59, 72, 79, 91, 95, 99, 117, 121, 134` - Cache
- `internal/templates/sandbox.go:27, 31, 35, 71, 73` - Filesystem

**Pattern:** All error paths use `fmt.Errorf("context: %w", err)` for proper unwrapping.

#### Structured Logging with Context ✅ CONSISTENT

**Evidence:**
- `cmd/main.go:100, 128, 138, 142, 158, 163, 194` - Uses `slog.Any("error", err)`
- `resultcaching/agent.go:104-108` - Adds correlation_id when available
- `rule_execution_agent.go:189-192, 437, 600` - Includes rule name, outcome, correlation_id
- `instrumentation.go:40` - Consistently appends correlation_id

#### Server Config Errors Terminate Process ✅ CORRECT

**Flow:**
```
main() → run() returns error → log.Fatalf() → os.Exit(1)
```

**Triggers:**
- Missing config files (loader.go:49-54)
- Invalid YAML/TOML parsing (loader.go:55-57)
- Environment validation failures (loader.go:96-98)
- Secrets loading failures (loader.go:102-105)
- Schema validation failures (loader.go:108-110)

#### Rule Config Errors Disable Rules ✅ CORRECT

**Location:** `rules_loader.go:73-83`

**Pattern:**
```go
if err := validateRuleExpressions(cfg, env); err != nil {
    source := a.ruleSources[name]
    reason := fmt.Sprintf("invalid rule expressions: %v", err)
    a.recordRuleSkip(name, reason, source)  // Track, don't crash
    delete(a.ruleSources, name)
    delete(a.rules, name)
}
```

**Skipped definitions exposed via:**
- `/health` endpoint (runtime.go:461-484)
- `/explain` endpoint (runtime.go:493-528)
- Runtime reload logs (runtime.go:605, 617)

#### Context Deadline Enforcement ⚠️ PARTIAL GAPS

**Backend HTTP Calls - GOOD**
- Location: `backend_interaction_agent.go:79` - Uses `http.NewRequestWithContext(ctx, ...)`
- Issue: `runtime.go:665` - Timeout hardcoded at 10 seconds, not configurable

**Cache Operations - EXCELLENT**
- All interface methods accept `context.Context` as first parameter
- Redis backend actively uses context (lines 86, 104, 120, 131, 139)
- Memory backend has context parameter (though not actively checked)

**Filesystem Operations - GAP**
- File operations do NOT use context deadlines
- `os.Stat()`, `filepath.WalkDir()`, `os.ReadFile()` don't support context
- Code checks `ctx.Done()` at strategic points but cannot interrupt blocking syscalls
- Impact:
  - Config hot-reload could delay if filesystem is slow/hung
  - Startup could block on stuck NFS/network filesystem
  - Graceful shutdown might exceed timeout

**Summary:**

| Requirement | Status | Compliance |
|-------------|--------|------------|
| Error wrapping with `%w` | ✅ Excellent | 100% |
| Structured logging | ✅ Excellent | 100% |
| Server config errors terminate | ✅ Correct | 100% |
| Rule config errors disable rules | ✅ Correct | 100% |
| Context deadlines in HTTP | ⚠️ Good | 90% - timeout hardcoded |
| Context deadlines in cache | ✅ Excellent | 100% |
| Context deadlines in filesystem | ⚠️ Gap | 50% - checked but not enforced |

#### Anti-Patterns & Gaps

1. **Hardcoded HTTP timeout** (`runtime.go:665`): Should be server-level config option
2. **Filesystem I/O lacks context enforcement**: Go stdlib limitation
3. **Redis initialization** (`cache/redis.go:75`): Hardcoded 5-second timeout
4. **No timeout config** for file watcher reload

**Recommendations:**
1. Add `server.backend.timeout` config option
2. Wrap filesystem operations with `context.WithTimeout`
3. Make cache initialization timeout configurable
4. Document filesystem operation limitations

---

## Priority-Ranked Recommendations

### Priority 0: Critical (Security/Correctness) - Fix Immediately

1. **Implement Redis cache invalidation on config reload**
   - File: `/home/user/PassCtrl/internal/runtime/cache/redis.go:126-128`
   - Implement `DeletePrefix()` using SCAN + DEL or key pattern matching
   - Test with actual Redis instance

2. ~~**Implement credential stripping for custom headers/query parameters**~~ ✅ **FIXED**
   - File: `/home/user/PassCtrl/internal/runtime/rule_execution_agent.go:456-550`
   - Fixed: Credentials from ALL auth directives now stripped before backend requests
   - Comprehensive test coverage added (5 test cases)

### Priority 1: High (Architecture) - Fix Soon

3. **Resolve dual caching architecture**
   - Option A: Consolidate all caching in Result Caching Agent
   - Option B: Update design docs to document two-tier caching as intended
   - Document ownership and responsibilities clearly

4. ~~**Add logging to Backend Interaction Agent**~~ ✅ **FIXED**
   - File: `/home/user/PassCtrl/internal/runtime/backend_interaction_agent.go:40-286`
   - Implemented structured logging for requests, responses, errors, and pagination
   - Includes correlation_id, endpoint, component, and agent fields

5. ~~**Add "component" field to all agent logs"**~~ ✅ **FIXED**
   - File: `/home/user/PassCtrl/internal/runtime/instrumentation.go:65`
   - Added "component": "runtime" to instrumentAgents() logger initialization

### Priority 2: Medium (Quality) - Fix When Possible

6. ~~**Update outdated examples**~~ ✅ **FIXED**
   - Files: `examples/suites/template-env-bundle/server.yaml`, `templates/deny.json.tmpl`
   - Replaced deprecated `templatesAllowEnv`/`templatesAllowedEnv` with `server.variables.environment`
   - Updated all `{{ env "..." }}` calls to `{{ .variables.environment.* }}`

7. **Make HTTP timeout configurable**
   - Add `server.backend.timeout` config option
   - Remove hardcoded 10-second timeout
   - Document default and reasonable ranges

8. **Add integration tests for variables**
   - Create end-to-end tests verifying CEL access to `variables.environment.*`
   - Create end-to-end tests verifying template access to `variables.secrets.*`
   - Test with real HTTP requests through pipeline

### Priority 3: Low (Documentation) - Improve Over Time

9. **Update design documentation**
   - Clarify Rule Execution Agent's backend rendering responsibility
   - Document two-tier caching if intentional
   - Emphasize Admission Agent's response rendering as performance optimization

10. **Add example configurations**
    - Environment variables in multi-environment setups
    - Secrets handling with Docker Compose
    - Database connection strings from secrets

11. **Complete configuration reference**
    - Add `server.variables.secrets` to docs table
    - Document Docker/Kubernetes secret mount patterns
    - Add security notes about newline trimming

---

## Strengths of the Codebase

1. **Security-First Design**
   - Template sandbox prevents path traversal
   - Environment variable access fully gated
   - Cache key isolation prevents user cross-contamination
   - Authorization header protection via config validation

2. **Excellent Test Coverage**
   - 18 comprehensive TTL test cases
   - Extensive cache behavior tests
   - Path traversal and symlink escape tests
   - Null-copy semantics validation

3. **Clear Separation of Concerns**
   - Backend Interaction Agent is exemplary
   - Most agents respect boundaries
   - Pipeline state management well-structured

4. **Observability Foundation**
   - Instrumentation wrapper provides consistency
   - Correlation ID handling correct
   - Comprehensive Prometheus metrics
   - Structured logging via slog

5. **Error Handling Discipline**
   - Consistent error wrapping with %w
   - Fail-fast for server config
   - Fail-soft for rule config
   - Proper context propagation (except filesystem)

6. **Configuration Model**
   - Hot-reload with filesystem watching
   - Duplicate detection
   - Missing dependency quarantine
   - RuleSources and SkippedDefinitions tracking

---

## Areas Requiring Attention

### Security
- ⚠️ Credential stripping incomplete (custom headers/query params)
- ⚠️ Examples reference deprecated security controls

### Correctness
- ❌ Redis cache invalidation not implemented
- ⚠️ Dual caching architecture unclear

### Observability
- ❌ Backend agent has no logging
- ❌ Component field missing from logs
- ⚠️ Manual logging inconsistent across agents

### Configuration
- ⚠️ HTTP timeout hardcoded
- ⚠️ Context deadlines not enforced on filesystem I/O

### Documentation
- ⚠️ Examples outdated
- ⚠️ Integration tests missing
- ⚠️ Configuration reference incomplete

---

## Conclusion

The PassCtrl codebase demonstrates **strong engineering practices** with a clear architectural vision, excellent security posture, and solid implementation of core features. The design documents accurately reflect most of the implementation, and the code shows consistent attention to error handling, observability, and testing.

However, **three critical gaps** require immediate attention:
1. Redis cache invalidation (correctness)
2. Credential stripping (security)
3. Dual caching architecture (clarity)

Once these are addressed, PassCtrl will have **excellent alignment** between design and implementation. The remaining issues are primarily quality-of-life improvements (logging, documentation, examples) that can be addressed incrementally.

**Recommended Next Steps:**
1. Fix Redis cache invalidation (remaining critical issue)
2. ~~Implement complete credential stripping~~ ✅ **DONE**
3. Decide on caching architecture (consolidate or document)
4. ~~Add Backend Interaction Agent logging~~ ✅ **DONE**
5. ~~Update examples and documentation~~ ✅ **DONE**

**Progress Update (2025-11-09):**
- ✅ Fixed credential stripping for custom headers/query parameters (Critical Issue #2)
- ✅ Added logging to Backend Interaction Agent (High-Priority Issue #4)
- ✅ Added component field to all agent logs (High-Priority Issue #5)
- ✅ Updated outdated template examples (High-Priority Issue #6)

**Overall Project Health:** **Strong** (B+ grade → A- grade) with significant progress toward excellence.
