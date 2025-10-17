# Runtime Agents Overview

PassCtrl organizes the forward-auth pipeline into specialised "agents". Each agent is
responsible for a slice of the request lifecycle and collaborates with its peers via
explicit context structures. This guide summarizes the role of every agent so you can
map configuration or implementation work to the correct component.

## 1. Server Configuration & Lifecycle
- **Focus**: Load configuration from files, environment variables, and defaults,
  initialize logging, and start the HTTP listener.
- **Highlights**: Enforces `env > file > default` precedence, watches the rules folder
  for hot reloads, and sandboxes template rendering relative to the configured
  template root. Records contributing rule documents in `RuleSources` and exposes
  duplicate/invalid definitions via `SkippedDefinitions` so health/explain endpoints can
  explain why certain rules were disabled. Endpoints with missing rule dependencies
  are also quarantined and annotated (`missing rule dependencies: <rule>`).【F:design/system-agents.md†L5-L27】

## 2. Admission & Raw State
- **Focus**: Authenticate requests, validate trusted proxies, and capture the immutable
  `rawState` snapshot before mutations occur.
- **Highlights**: Enforces trusted proxy policy (stripping headers in development,
  rejecting untrusted peers in production, validating every forwarded hop, and
  normalizing both `X-Forwarded-*` and RFC7239 `Forwarded` headers), short-circuits
  failed authentications, emits structured telemetry, and records credential attributes
  for downstream templates.【F:design/system-agents.md†L29-L47】

## 3. Forward Request Policy
- **Focus**: Curate the headers and query parameters exposed to rules and backend
  calls.
- **Highlights**: Applies allow/strip/custom directives (including `*` wildcards),
  optionally re-exposes the sanitized proxy metadata (`X-Forwarded-*` plus RFC7239
  `Forwarded`) via `forwardProxyHeaders`, and stores the curated view for auditing.【F:design/system-agents.md†L47-L61】

## 4. Rule Chain
- **Focus**: Evaluate the ordered list of rules attached to an endpoint while enforcing
  short-circuit behavior.
- **Highlights**: Tracks latency, variables, and cache participation per rule, recording
  each rule’s outcome and duration to explain why the chain stopped.【F:design/system-agents.md†L61-L73】

## 5. Rule Execution
- **Focus**: Execute an individual rule, including credential handling, backend
  orchestration, pagination, and condition evaluation.
- **Highlights**: Supports credential transformation (`forwardAs`), renders backend
  requests via templates (including `backendApi.body` and `backendApi.bodyFile` scoped to the template sandbox), honors rule-level caching constraints, and now
  evaluates declarative `whenAll`/`failWhen`/`errorWhen` condition blocks passed from
  the chain agent while populating per-rule history.【F:design/system-agents.md†L73-L87】

## 6. Response Policy
- **Focus**: Render the final `/auth` response based on the chain outcome, endpoint
  policy, and rule-provided payloads.
- **Highlights**: Applies default forward-auth statuses, merges header directives, and links responses to chain history in logs.
  The `/auth` body is intentionally near-empty; only explicitly constructed bodies (via rules/templates) are emitted. Use `/explain`
  and debug logs for rich diagnostics.【F:design/system-agents.md†L87-L95】

## 7. Result Caching
- **Focus**: Memoise rule and endpoint outcomes while guarding against stale or
  unsafe payload reuse.
- **Highlights**: Stores only decision metadata, honors per-outcome TTLs, and skips
  caching on backend errors so replay reflects the freshest data. Coordinates with pipeline reload invalidation hooks so clustered caches purge stale entries when definitions change.【F:design/system-agents.md†L97-L107】【F:internal/runtime/runtime.go†L475-L502】

## Collaboration Tips
- Emit correlation IDs across agents for traceability.
- Update design artifacts whenever an agent’s contract or responsibilities change.
- When debugging, inspect the curated request view, chain history, and cache notes to
  understand why a decision was made.【F:design/system-agents.md†L119-L124】
