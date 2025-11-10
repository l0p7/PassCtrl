# System Agents

PassCtrl models the runtime as a collaboration between nine specialised agents. Each agent aligns with the building blocks
summarized in the main README and expanded upon in the `design/` documents. The sections below capture the contract, inputs,
outputs, and operational concerns for each participant.

## 1. Server Configuration & Lifecycle Agent
- **Purpose**: Own the HTTP server bootstrap, load and watch configuration, and surface shared runtime services (logging,
  correlation IDs, template sandboxing).
- **Inputs**: Top-level `server` configuration, environment variables (when explicitly allowed), filesystem events for watched
  paths.
- **Outputs**: Initialized logger, hydrated configuration structs, watcher callbacks that refresh endpoint/rule state, and the
  running HTTP listener.
- **Key Behaviors**:
  - Load configuration with [`koanf`](https://github.com/knadh/koanf), merging files, env overrides, and flags while exposing a
    consistent accessor API. Respect the precedence `env > file > default` so containerised deployments can override values via
    environment variables. Register filesystem watchers when `rulesFolder` is set and reload endpoint/rule configuration on
    change.
  - Bind the HTTP listener to the configured `listen.address`/`listen.port` pair and propagate correlation IDs sourced from the
    configured header into structured logs and tracing spans.
  - Enforce the template sandbox by resolving template paths relative to `templates.templatesFolder`, rejecting attempts to escape the
    directory, and applying the `templates.templatesAllowEnv` + `templates.templatesAllowedEnv` guard before exposing environment variables to the
    templating engine.
  - Validate configuration aggressively: reject duplicate endpoint names, disable conflicting rules, and terminate on unknown
    server-level keys. When rule templates or CEL programs fail to parse or extra keys are present, log the issue, disable
    the rule, and mark dependent endpoints unhealthy (5xx) without crashing the process or caching the error state. Any successful
    config reload that touches an endpoint or rule must purge the associated caches so subsequent requests re-evaluate with the
    latest configuration.
  - Validate configuration invariants (e.g., `rulesFolder` xor `rulesFile`) and surface errors before starting request handling.

## 2. Admission & Raw State Agent
- **Purpose**: Authenticate the caller, enforce trusted proxy rules, capture the immutable `rawState` snapshot, and render
  complete 401 challenge responses for admission failures before any policy logic executes.
- **Inputs**: Raw HTTP request, endpoint authentication posture, trusted proxy configuration, optional response customization config.
- **Outputs**: Auth result (`pass`/`fail`), credential attributes surfaced to templates as `.auth.input.*`, the recorded
  request snapshot available to downstream agents, and complete HTTP response for admission failures.
- **Observability**: Persist the admission decision snapshot (outcome, reason, client metadata) so later agents and audit
  tooling can reconstruct why the request was accepted or denied.
- **Key Behaviors**:
  - Reject untrusted proxies in production; optionally strip and annotate in development mode.
  - Normalise and validate both the legacy `X-Forwarded-*` family **and** RFC7239 `Forwarded` header, keeping the first hop in
    sync across both representations before surfacing trusted client metadata.
  - Evaluate `authentication.allow` providers (basic, bearer, header, query, none), capture every credential that matches, and
    expose the full set to downstream rules while failing fast when no providers are satisfied.
  - **Render complete admission failure response** when `required: true` and credentials are missing:
    - Set default 401 status and `WWW-Authenticate` header from `challenge` config (Basic, Bearer, or Digest)
    - Apply optional `authentication.response` config overrides: custom status, additional headers, templated body
    - Merge custom headers with challenge header (never replace `WWW-Authenticate`)
    - Render body template if `body` or `bodyFile` configured, otherwise use default "authentication required" message
  - **Short-circuit pipeline on admission failure**: return immediately after rendering response, skipping forward policy,
    endpoint variables, rule chain, and response policy agents (performance optimization, prevents ~7 unnecessary agent executions)
  - Emit structured telemetry identifying client metadata, authentication outcome, proxy evaluation, and short-circuit events.

## 3. Forward Request Policy Agent
- **Purpose**: Sanitize and forward proxy metadata headers when configured.
- **Inputs**: Admission snapshot (`rawState`), endpoint `forwardRequestPolicy.forwardProxyHeaders` flag.
- **Outputs**: Sanitized proxy metadata (`X-Forwarded-*` and RFC7239 `Forwarded`) stored in `state.Forward.Headers` when `forwardProxyHeaders` is enabled.
- **Key Behaviors**:
  - When `forwardProxyHeaders: true`, sanitize and forward `X-Forwarded-For`, `X-Forwarded-Proto`, `X-Forwarded-Host`, and `Forwarded` headers.
  - Skip empty or whitespace-only values during sanitization.
  - Normalize all header names to lowercase for consistent access.
  - Header and query parameter selection for backends is handled directly by backend definitions using **null-copy semantics**: `nil` value copies from raw request, non-nil value uses static string or template expression.

## 4. Endpoint Variables Agent
- **Purpose**: Evaluate endpoint-level variables once per request and make them available to all rules in the chain.
- **Inputs**: Endpoint `variables` configuration (map of variable names to expressions), curated request view from Admission Agent.
- **Outputs**: Evaluated variables stored in `state.Variables.Global` for access by all rules via `.endpoint.variables.*` template context.
- **Key Behaviors**:
  - Evaluate each endpoint variable using the **hybrid evaluator** which auto-detects expression type:
    - **CEL expressions**: Used when expression does not contain `{{` template delimiters
    - **Go templates**: Used when expression contains `{{` template syntax
  - Build request context from admission state, exposing standard fields (headers, method, path, query parameters, client metadata) for both CEL and template evaluation.
  - Continue evaluation on individual variable errors (fail-soft behavior): when a variable expression fails, log a warning, set that variable to empty string, and continue evaluating remaining variables.
  - Store all evaluated variables in global scope (`state.Variables.Global`) before rule chain execution begins.
  - Skip execution entirely when no endpoint variables are configured (returns `skipped` status).
  - Emit debug logs with variable count on successful evaluation.
- **Location**: `internal/runtime/endpointvars/`
- **Observability**: Emits `agent: "endpoint_variables"` logs with variable counts and evaluation errors.

## 5. Rule Chain Agent
- **Purpose**: Orchestrate ordered rule execution, enforce short-circuit semantics, and manage exported variables.
- **Inputs**: Curated request view, endpoint variables, exported variables from prior rules (including cache hits), endpoint rule list.
- **Outputs**: Aggregate rule history, accumulated exported variables, final chain outcome (`Pass`, `Fail`, or `Error`).
- **Key Behaviors**:
  - Evaluate rules sequentially; stop on first non-pass result while capturing per-rule outcomes, durations, and variables for auditing.
  - Track per-rule latency, outcomes, exported variables, cache participation, and backend call summaries.
  - Accumulate exported variables from each rule (via `responses.<outcome>.variables`) and make them available to subsequent rules via `.rules.<ruleName>.variables.*`.

## 6. Rule Execution Agent
- **Purpose**: Orchestrate an individual rule's evaluation from credential intake through condition evaluation and
  response assembly, delegating backend HTTP execution to the Backend Interaction Agent.
- **Inputs**: Rule configuration, curated request view, scoped variables, optional cached decision hints.
- **Outputs**: Rule outcome, rendered responses (status, headers, bodies), exported variables.
- **Key Behaviors**:
  - Accept credentials via ordered **match groups**, where each group contains a `match` array (credential matchers with AND logic)
    and optional `forwardAs` array (credential outputs).
  - **Extract credentials** from admission state, organizing them by type (bearer, basic, headers map, query map) for efficient matching.
  - **Evaluate match groups sequentially** (OR between groups, AND within groups): for each group, check if ALL matchers succeed;
    first complete match wins. Each matcher specifies type, optional name, and optional value constraints (literal or regex patterns).
  - **Value matching**: For matchers with value constraints, test credential values against literal strings or compiled regex patterns
    (delimited by `/`). Multiple patterns use OR logic (any match succeeds).
  - **Build template context** from all matched credentials in winning group, exposing `.auth.input.bearer.token`,
    `.auth.input.basic.user/.password`, `.auth.input.header['x-name']` (lowercase keys), `.auth.input.query['param']`.
  - **Render credential outputs**: When `forwardAs` is present, render each output using Go templates. When `forwardAs` is omitted,
    enable **pass-through mode** by reconstructing forwards from matched credentials.
  - **Strip all credential sources** mentioned in ANY match group from forwarded request, then apply winning group's outputs
    (explicit credential stripping for fail-closed security).
  - Render backend request templates (URL, method, headers, query params, body) using curated context and matched credentials,
    producing fully-rendered request descriptor for Backend Interaction Agent.
  - Delegate HTTP execution and pagination to Backend Interaction Agent, receiving populated backend state in return.
  - Evaluate pass/fail/error conditions via CEL using backend responses and scoped variables.
  - **Implement Tier 1 (per-rule) caching**: Check cache before backend calls using compound key (`baseKey|ruleName|backendHash|upstreamVarsHash`); store outcome, exported variables, and response headers after evaluation. Error outcomes bypass caching. This tier enables granular optimization by reusing individual rule decisions across different request contexts (see Section 9 for two-tier caching architecture).
  - Evaluate declarative `whenAll`/`failWhen`/`errorWhen` condition blocks, populating execution history and per-rule reasons.
  - **Extract variables** via `variables` (local temporaries) and `responses.*.variables` (exported):
    - **Local variables** (`variables`): Temporary calculations available only within the rule, not cached or exported
    - **Exported variables** (`responses.pass/fail/error.variables`): Shared with subsequent rules AND available to endpoint response templates. Cached with decision outcomes.

## 7. Backend Interaction Agent
- **Purpose**: Execute HTTP requests to backend APIs with pagination support, capturing responses and errors without evaluating policy logic.
- **Inputs**: Fully-rendered backend request descriptor (`method`, `url`, `headers`, `query`, `body`), backend configuration (accepted statuses, pagination settings), pipeline state.
- **Outputs**: Populated `state.Backend.*` fields including status, headers, body (parsed JSON when applicable), pagination results, and any execution errors.
- **Key Behaviors**:
  - Accept pre-rendered request descriptors from the Rule Execution Agent—no template evaluation, credential matching, or condition logic.
  - Execute HTTP requests using the configured client with context deadline enforcement and timeout handling.
  - Implement pagination protocols (link-header per RFC 5988, with future support for token and cursor-based pagination) with safety bounds (max pages, visited URL tracking to prevent loops).
  - Parse JSON responses automatically when content-type indicates JSON, normalizing numbers for consistent CEL evaluation.
  - Respect backend `acceptedStatuses` configuration to determine success vs. failure without policy evaluation.
  - Capture per-page state during pagination, exposing all pages and the last page's details to the Rule Execution Agent for condition evaluation.
  - Handle network errors, timeouts, malformed responses, and oversized bodies gracefully, recording errors in `state.Backend.Error` for the Rule Execution Agent to convert into policy outcomes.
  - Emit structured logs with `agent: "backend_interaction"` labels, tracking HTTP method, URL, status, latency, pagination metrics, and correlation IDs.
  - Never cache responses, evaluate conditions, or make policy decisions—purely responsible for reliable HTTP execution and response capture.

## 8. Response Policy Agent
- **Purpose**: Render the final `/auth` response (pass, fail, or error) using endpoint policy and variables explicitly exported by the decisive rule.
- **Inputs**: Chain outcome (pass/fail/error), endpoint response policy configuration, exported variables from decisive rule.
- **Outputs**: HTTP response for the caller, including status, headers, and body.
- **Key Behaviors**:
  - **Endpoints own the response format entirely**: status codes, headers, and body templates are defined in endpoint `responsePolicy.pass/fail/error` blocks.
  - Default to canonical forward-auth statuses (200 OK for pass, 403 Forbidden for fail, 502 Bad Gateway for error) when overrides are absent.
  - **Exported variables are available to endpoint templates**: Rules export variables via `responses.pass/fail/error.variables` blocks. These variables are shared with subsequent rules AND available to endpoint response templates via `.response.*` context.
  - **Local variables are NOT exposed**: Variables used for temporary calculations (`variables` block) remain internal to the rule and are not accessible to endpoints.
  - Render status, headers, and body using Go templates with access to: `.endpoint`, `.correlationId`, `.auth.input.*`, `.backend.*` (from decisive rule), `.response.*` (exported variables from decisive rule), and standard context fields.
  - Apply **null-copy semantics** for response headers: `nil` value copies from raw request headers, non-nil value uses static string or template expression. Empty template results are omitted from the response.
  - Automatically add `X-PassCtrl-Outcome` header containing the rule outcome (pass/fail/error) when outcome is present.
  - Emit structured logs tying the response to the chain history and curated request view.

## 9. Result Caching Agent (Two-Tier Architecture)
- **Purpose**: Memoise rule and endpoint decisions using a two-tier caching architecture for maximum performance in this critical hot path. PassCtrl implements both **per-rule caching** (fine-grained) and **endpoint-level caching** (coarse-grained) to minimize backend calls and evaluation overhead.

### Two-Tier Caching Architecture

PassCtrl employs a **two-tier caching strategy** to optimize authorization latency:

**Tier 1: Per-Rule Caching** (implemented by Rule Execution Agent)
- **Granularity**: Individual rule decisions within a chain
- **Cache Key**: `baseKey|ruleName|backendHash|upstreamVarsHash`
  - `baseKey`: User credential + endpoint + path (security isolation)
  - `ruleName`: Specific rule being evaluated
  - `backendHash`: Hash of rendered backend request (method, URL, headers, body)
  - `upstreamVarsHash`: Hash of upstream exported variables (when `cache.strict: true`)
- **Stores**: Rule outcome, reason, exported variables, response headers
- **Location**: `internal/runtime/rule_execution_agent.go` (checkRuleCache/storeRuleCache methods)
- **Purpose**: Skip redundant backend API calls for individual rules that were already evaluated in this context
- **Performance Win**: Reduces backend calls even when endpoint cache misses (e.g., different credentials accessing same endpoint)

**Tier 2: Endpoint-Level Caching** (implemented by Result Caching Agent)
- **Granularity**: Entire rule chain outcome for an endpoint
- **Cache Key**: `baseKey` only (credential + endpoint + path)
- **Stores**: Final chain outcome (pass/fail), response status/message/headers
- **Location**: `internal/runtime/resultcaching/agent.go`
- **Purpose**: Skip entire rule chain evaluation when final outcome is known
- **Performance Win**: Fastest path - bypasses all rule evaluation and backend calls for identical requests

### Cache Tier Interaction Example

**Scenario**: Endpoint `/api/data` with 3-rule chain: `[rate-limit, verify-key, check-quota]`

```
Request 1 (user=alice, path=/api/data):
  - Endpoint cache: MISS
  - Per-rule cache: MISS for all rules
  - Executes: rate-limit (backend) → verify-key (backend) → check-quota (backend)
  - Stores: 3 per-rule cache entries + 1 endpoint cache entry

Request 2 (user=alice, path=/api/data, identical):
  - Endpoint cache: HIT → Return cached response (skip all rules)
  - Backend calls: 0

Request 3 (user=bob, path=/api/data):
  - Endpoint cache: MISS (different credential = different cache key)
  - Per-rule cache:
    - rate-limit: HIT (same backend request)
    - verify-key: MISS (different API key) → backend call
    - check-quota: HIT (same backend request)
  - Backend calls: 1 (instead of 3)
```

### Shared Caching Invariants (Both Tiers)

- **Inputs**: Rule/endpoint `cache` blocks, backend cache headers (when `followCacheControl` is true), decision artifacts.
- **Outputs**: Cached pass/fail outcomes, reused variables, audit records noting cache hits or misses.
- **Key Behaviors**:
  - **Store only decision metadata**: Outcome, variables, rendered response descriptors. Never persist backend response bodies beyond active request.
  - **Error outcomes never cached**: 5xx responses and error outcomes return TTL=0 (hardcoded highest precedence).
  - **TTL Hierarchy** (both tiers enforce the same policy):
    1. Error outcomes → 0 (never cache, highest precedence)
    2. Backend Cache-Control header (if `followCacheControl: true`)
    3. Rule manual TTL (`cache.passTTL`, `cache.failTTL`)
    4. Endpoint TTL ceiling (`cache.resultTTL`)
    5. Server max TTL ceiling (lowest precedence)
    - System applies **minimum** of all applicable ceilings
  - **Security isolation**: Both tiers use credential-based cache keys preventing user cross-contamination
  - **Cache invalidation**: Both tiers use epoch-based keys; config reloads increment epoch and purge old entries
  - **Proxy header handling**: `cache.includeProxyHeaders` flag controls whether proxy metadata affects cache keys (default: true for security)
  - **Surface cache participation** in observability events so operators can distinguish fresh evaluations from memoized results

### Rationale for Two-Tier Architecture

This dual-tier approach is an **intentional performance optimization** for PassCtrl's critical authentication hot path:

1. **Endpoint cache** provides the fastest path for identical requests (common in CDN/load balancer scenarios)
2. **Per-rule cache** provides granular optimization when requests vary slightly (different users, different credentials, different query params)
3. **Real-world impact**: Rule chains with 5+ rules calling backends can see 80%+ reduction in backend calls
4. **Already implemented and tested**: Both tiers properly enforce all caching invariants (no 5xx caching, TTL hierarchy, security isolation)

The added complexity is justified by measurable latency improvements in production authorization scenarios where backend calls dominate request time.

## Collaboration & Observability
- A shared instrumentation layer wraps every runtime agent to emit structured logs (`log/slog`) with consistent fields
  (`component`, `agent`, `status`, `outcome`, `latency_ms`, `endpoint`, `correlation_id`) and to publish the final pipeline
  completion metrics for each request.
- Agents share context through explicit structures: `endpointContext`, `chainContext`, and `ruleContext`, matching the diagrams in
  [`uml-diagrams.md`](uml-diagrams.md).
- Response construction echoes the active `correlation_id` in both headers and payloads so operators can trace caller-visible
  responses back to the pipeline execution trail.
- Changes to agent responsibilities must be reflected in the design artifacts to keep documentation authoritative.

These agents form the contract for implementation work inside PassCtrl and provide a shared vocabulary when discussing behavior,
observability, and future enhancements.
