# Decision Model

The v2 engine should expose a decision pipeline that is easy to audit and reason about. Rather than interleaving execution
details with policy declarations, we break the process into explicit stages. Each stage consumes the structured context from the
previous one and emits a typed result that downstream stages can reference.

## Stage 1: Endpoint Admission & Raw State
- Validate transport assumptions (HTTP method, TLS, host match).
- Enforce trusted proxy rules: reject untrusted peers in production, strip and annotate in development.
- Extract the original URL from the `X-Forwarded-*` family (or RFC7239 `Forwarded` header), normalize headers/query parameters, and capture the immutable raw
  request snapshot (`rawState`).
- Evaluate endpoint-level authentication requirements before any rule logic runs; failure exits through the response policy’s
  `fail` branch with the configured challenge.

## Stage 2: Forward Request Policy
- Start with the raw request snapshot and determine which headers and query parameters rules may see.
- Support an explicit `forwardProxyHeaders` toggle to decide whether sanitized proxy metadata is exposed downstream.
- Apply `allowHeaders` / `stripHeaders` / `customHeaders` (and their query equivalents) to produce the curated request view shared
  with every rule and backend call.
- Persist this curated view so later stages know exactly which client-supplied fields influenced decisions.

## Stage 3: Rule Evaluation
- Execute ordered, named rules sourced from the endpoint’s `rules` list.
- Each rule may include:
  - **auth** — an ordered list of accepted credential directives. Each entry names a source `type`
    (`basic`, `bearer`, `header`, `query`, or `none`), optional selector attributes such as `name`, and an inline `forwardAs`
    block when the credential should be transformed before reaching the backend. Omitting `forwardAs` forwards the credential in
    its original shape (e.g., Basic remains Basic). When present, `forwardAs` may declare a new credential `type` and populate
    fields such as `token`, `user`, `password`, `name`, or `value` using Go templates (with Sprig helpers). The matched
    credential is surfaced to templates as `.auth.input.*`, enabling rewrites like converting a Basic password into a Bearer
    token or prefixing a captured header value.
  - **backendApi** — the target URL, HTTP method, accepted response status codes, pagination behavior, and the same
    allow/strip/custom controls for headers and query parameters used by the forward policy. Header and query names are literal,
    while request bodies and custom values are rendered via Go templates (with Sprig helpers).
  - **conditions** — pass/fail/error predicates. By default the backend response status drives pass/fail, but authors may supply
    CEL programs that inspect response headers/bodies to override outcomes. Helper functions such as `lookup(map, key)` return
    `null` for missing entries so conditions can probe optional headers, query parameters, or backend payloads without raising
    evaluation errors.
  - **responses** — pass/fail/error response descriptors containing status codes, header directives, and bodies. Header values and bodies may use Go templates (with Sprig helpers) independent of the rule-condition pipeline.
  - **variables** — extractions scoped as `global`, `rule`, or `local` for sharing data between rules. Each `from` directive is a CEL program evaluated against the rule context.
- Variable scopes behave as follows:
  - `global` variables are visible to all rules as `.variables.<name>` (or `variables.<name>`) and may be overwritten by later
    rules that export a value with the same key.
  - `rule` variables appear to other rules as `rules.<ruleName>.variables.<name>` and resolve within the rule itself as
    `.variables.<name>`.
  - `local` variables exist only for the rule that defined them (`.variables.<name>` inside the rule) and are never exposed to
    subsequent rules.
- Outcomes are `Pass`, `Fail`, or `Error`. Only `Pass` allows evaluation to continue. Errors short-circuit to the response
  policy’s `error` branch.

## Stage 4: Response Policy
- Select the appropriate category (`pass`, `fail`, or `error`) based on rule outcomes or configuration issues: `pass` fires when
  every rule succeeded, `fail` triggers when a rule returns a failure result, and `error` captures invalid configuration or rule
  errors.
- Within each category, allow templates to read the original raw request snapshot plus any variables emitted by rules.
- Provide `allowHeaders` / `stripHeaders` / `customHeaders` tooling and `body`/`bodyFile` templating to construct the final HTTP
  response, starting from the backend status code and header set captured from the decisive rule unless explicitly overridden.
  Header overrides may use Go templates (with Sprig) or JMESPath expressions.
- Default behavior, when a category is unspecified, returns the canonical forward-auth statuses (200 on pass, 401/403 on fail,
  5xx on error) without backend leakage.

## Stage 5: Result Caching
- Treat caching as a control-plane concern: rules may declare cache hints, but the runtime persists only the decision artifacts
  (pass/fail/error outcome, exported variables, and the rendered rule response metadata).
- Each rule exposes a `cache` block that controls memoization:
  - `followCacheControl` determines whether to honor backend `Cache-Control` directives when computing TTLs.
  - `passTTL` and `failTTL` (durations) override the backend TTL for the respective outcomes; omitting a value disables caching
    for that outcome.
  - Error outcomes (including backend 5xx responses) are never cached regardless of configuration.
- Honor the shorter of the backend `Cache-Control` duration and any explicit TTL when `followCacheControl` is true; otherwise
  use the provided TTLs. Do **not** cache raw backend response bodies or header sets beyond what is necessary for the originating request.
- Allow endpoints to specify a `cacheResultDuration` that memoizes the entire rule chain outcome (including response policy
  output) until its TTL expires or any participating rule’s cache entry lapses, whichever comes first. Endpoint-level caches
  inherit the “no 5xx” rule and track rule-level fail caches the same way—if a participating rule’s fail cache expires or is
  disabled, the endpoint cache re-evaluates.
- Expose cache participation in decision records so operators can distinguish fresh evaluations from cached results, see whether
  pass or fail caches applied, and audit which variables were reused.

### Decision Record Schema
Every request should produce a machine-readable record containing:
- Request metadata (endpoint, method, client identity, correlation ID).
- Authentication result and credential attributes.
- Forward request policy outcome (allowed headers/query parameters and proxy metadata visibility).
- Each rule’s outcome, variables, backend call summaries, and latency.
- Final response details.

This schema underpins CLI tooling, dashboards, and automated audits that make the v2 system transparent while remaining flexible.
