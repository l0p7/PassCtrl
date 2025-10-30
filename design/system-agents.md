# System Agents

PassCtrl models the runtime as a collaboration between eight specialised agents. Each agent aligns with the building blocks
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
- **Purpose**: Authenticate the caller, enforce trusted proxy rules, and capture the immutable `rawState` snapshot before any
  policy logic executes.
- **Inputs**: Raw HTTP request, endpoint authentication posture, trusted proxy configuration.
- **Outputs**: Auth result (`pass`/`fail`), credential attributes surfaced to templates as `.auth.input.*`, and the recorded
  request snapshot available to downstream agents.
- **Observability**: Persist the admission decision snapshot (outcome, reason, client metadata) so later agents and audit
  tooling can reconstruct why the request was accepted or denied.
- **Key Behaviors**:
  - Reject untrusted proxies in production; optionally strip and annotate in development mode.
  - Normalise and validate both the legacy `X-Forwarded-*` family **and** RFC7239 `Forwarded` header, keeping the first hop in
    sync across both representations before surfacing trusted client metadata.
  - Evaluate `authentication.allow` providers (basic, bearer, header, query, none), capture every credential that matches, and
    expose the full set to downstream rules while failing fast when no providers are satisfied.
  - Emit a `WWW-Authenticate` response using the configured challenge when admission fails and a challenge is defined.
  - Issue the configured failure response when admission fails, short-circuiting the rest of the pipeline.
  - Emit structured telemetry identifying client metadata, authentication outcome, and proxy evaluation.

## 3. Forward Request Policy Agent
- **Purpose**: Curate which headers and query parameters rules and backends may see, preserving intent transparency.
- **Inputs**: Admission snapshot (`rawState`), endpoint `forwardRequestPolicy` directives.
- **Outputs**: Curated request view shared with every rule, plus the sanitized proxy metadata (`X-Forwarded-*` and RFC7239 `Forwarded`) when `forwardProxyHeaders` is enabled.
- **Key Behaviors**:
  - Apply allow/strip/custom directives with the documented template evaluation order.
  - Persist the curated view for audit logs and rule variable extraction.
  - Record its decisions so caching and response policy can prove which inputs influenced downstream outcomes.

## 4. Rule Chain Agent
- **Purpose**: Orchestrate ordered rule execution, enforce short-circuit semantics, and manage scoped variables.
- **Inputs**: Curated request view, global variables from prior evaluations (including cache hits), endpoint rule list.
- **Outputs**: Aggregate rule history, merged variables, final chain outcome (`Pass`, `Fail`, or `Error`).
- **Key Behaviors**:
  - Evaluate rules sequentially; stop on first non-pass result while capturing per-rule outcomes, durations, and variables for auditing.
  - Track per-rule latency, outcomes, exported variables, cache participation, and backend call summaries.
  - Surface variables to later rules using the `global`, `rule`, and `local` scopes defined in the design.

## 5. Rule Execution Agent
- **Purpose**: Orchestrate an individual rule's evaluation from credential intake through condition evaluation and
  response assembly, delegating backend HTTP execution to the Backend Interaction Agent.
- **Inputs**: Rule configuration, curated request view, scoped variables, optional cached decision hints.
- **Outputs**: Rule outcome, rendered responses (status, headers, bodies), exported variables.
- **Key Behaviors**:
  - Accept credentials via the ordered `auth` directives, with optional `forwardAs` transformations.
  - Evaluate rule authentication directives sequentially, forwarding the first matched credential (or failing early when none
    match and no `type: none` fallback exists) according to the configured `forwardAs` block.
  - Render backend request templates (URL, method, headers, query params, body) using the curated context and matched credentials, producing a fully-rendered request descriptor for the Backend Interaction Agent.
  - Delegate HTTP execution and pagination to the Backend Interaction Agent, receiving populated backend state in return.
  - Evaluate pass/fail/error conditions via CEL using backend responses and scoped variables.
  - Honor rule-level caching directives (checking before backend calls, storing after evaluation) and ensure error outcomes bypass caching.
  - Evaluate declarative `whenAll`/`failWhen`/`errorWhen` condition blocks, populating execution history and per-rule reasons for downstream explanation.
  - Extract and export variables to global/rule/local scopes from backend responses and CEL evaluations.

## 6. Backend Interaction Agent
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

## 7. Response Policy Agent
- **Purpose**: Render the final `/auth` response (pass, fail, or error) using endpoint policy, rule outputs, and curated context.
- **Inputs**: Chain outcome, decisive rule response payload, endpoint response policy configuration, stored variables.
- **Outputs**: HTTP response for the caller, including status, headers, and body.
- **Key Behaviors**:
  - Default to canonical forward-auth statuses when overrides are absent.
  - Respect rule- and endpoint-level header directives, using templates for rendered values.
  - Emit structured logs tying the response to the chain history and curated request view.

## 8. Result Caching Agent
- **Purpose**: Memoise rule and endpoint decisions while upholding strict invariants around error handling and payload storage.
- **Inputs**: Rule/endpoint `cache` blocks, backend cache headers (when `followCacheControl` is true), decision artifacts.
- **Outputs**: Cached pass/fail outcomes, reused variables, audit records noting cache hits or misses.
- **Key Behaviors**:
  - Store only decision metadata (outcome, variables, rendered response descriptors); never persist backend bodies beyond the
    active evaluation.
  - Honor separate pass/fail TTLs and drop entries when backend signals 5xx or when TTLs expire.
  - Surface cache participation in observability events so operators can distinguish fresh evaluations from memoized results.

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
