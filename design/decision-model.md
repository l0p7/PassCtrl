# Decision Model

The v2 engine should expose a decision pipeline that is easy to audit and reason about. Rather than interleaving execution
details with policy declarations, we break the process into explicit stages. Each stage consumes the structured context from the
previous one and emits a typed result that downstream stages can reference.

## Stage 1: Endpoint Admission & Raw State
- Validate transport assumptions (HTTP method, TLS, host match).
- Enforce trusted proxy rules: reject untrusted peers in production, strip and annotate in development. When the immediate
  peer is trusted, treat the first forwarded hop as the client IP without requiring all intermediate `X-Forwarded-For` hops to
  belong to trusted networks. Prefer the RFC7239 `Forwarded` header when present and keep the first hop consistent across both
  header families.
- Extract the original URL from the `X-Forwarded-*` family (or RFC7239 `Forwarded` header), normalize headers/query parameters, and capture the immutable raw
  request snapshot (`rawState`).
- Evaluate endpoint-level authentication requirements by walking the ordered `authentication.allow` providers (basic, bearer, header, query, none);
  capture every credential that matches and surface them to later stages. When `authentication.required` is `true` (default), failure exits through the
  response policy’s `fail` branch with the configured challenge. When `false`, the pipeline records an unauthenticated admission but continues, leaving
  rules to decide how to treat missing credentials.

## Stage 2: Forward Request Policy
- Start with the raw request snapshot and determine which headers and query parameters rules may see.
- Support an explicit `forwardProxyHeaders` toggle to decide whether sanitized proxy metadata is exposed downstream.
- Apply `allowHeaders` / `stripHeaders` / `customHeaders` (and their query equivalents) to produce the curated request view shared
  with every rule and backend call.
- Persist this curated view so later stages know exactly which client-supplied fields influenced decisions.

## Stage 3: Rule Evaluation
- Execute ordered, named rules sourced from the endpoint’s `rules` list.
- Each rule may include:
  - **auth** — an ordered array of **match groups**, where each group contains a `match` array of credential matchers and an
    optional `forwardAs` array of credential outputs. Match groups implement **AND logic within groups, OR logic between groups**:
    within a single group, ALL matchers must succeed; groups are evaluated sequentially until one fully matches. Each matcher
    specifies a `type` (`basic`, `bearer`, `header`, `query`, or `none`), optional selector attributes like `name`, and optional
    **value constraints** (literal strings or regex patterns delimited by `/`) to filter credentials by their values. When a group
    matches, all matched credentials become accessible via `.auth.input.*` (e.g., `.auth.input.bearer.token`,
    `.auth.input.basic.user`, `.auth.input.header['x-name']`, `.auth.input.query['param']`). The `forwardAs` array contains
    multiple credential outputs (each specifying `type` and type-specific fields like `token`, `user`, `password`, `name`, `value`)
    rendered via Go templates with Sprig helpers. Omitting `forwardAs` enables **pass-through mode** where matched credentials
    forward unchanged. **Credential stripping** is explicit: all credential sources mentioned in ANY match group are stripped from
    the forwarded request before the winning group's outputs are applied. If no group matches and no `type: none` is present, the
    rule fails before any backend call occurs. The synthesized outbound credentials appear under `.auth.forward.*` for CEL access.
  - **backendApi** — the target URL, HTTP method, accepted response status codes, pagination behavior, and the same
    allow/strip/custom controls for headers and query parameters used by the forward policy. Header and query names are literal,
    while request bodies and custom values are rendered via Go templates (with Sprig helpers).
  - **conditions** — pass/fail/error predicates. By default the backend response status drives pass/fail, but authors may supply
    CEL programs that inspect response headers/bodies to override outcomes. Helper functions such as `lookup(map, key)` return
    `null` for missing entries so conditions can probe optional headers, query parameters, or backend payloads without raising
    evaluation errors.
  - **responses** — pass/fail/error response descriptors for exporting variables to subsequent rules. Only variables from the winning outcome are exported and cached. Variable expressions support hybrid CEL/Template evaluation (auto-detected by `{{` presence).
  - **variables** — local/temporary variables for intermediate calculations within a rule. These variables are NOT cached and NOT exported to other rules. They use hybrid CEL/Template evaluation and are only accessible via `.variables.<name>` within the same rule.
- Variable system consists of three tiers:
  - **Endpoint variables** (`.vars.*`) - Configuration-level values defined in endpoint config, available to all rules
  - **Local variables** (`.variables.*`) - Rule-scoped temporaries defined in `variables:` block, ephemeral and not exported
  - **Exported variables** (`.rules.<ruleName>.variables.*`) - Cross-rule data defined in `responses.<outcome>.variables:`, cached with the rule outcome and accessible to subsequent rules and endpoint response templates
- Outcomes are `Pass`, `Fail`, or `Error`. Only `Pass` allows evaluation to continue. Errors short-circuit to the response
  policy’s `error` branch.

## Stage 4: Response Policy
- Select the appropriate category (`pass`, `fail`, or `error`) based on rule outcomes or configuration issues: `pass` fires when
  every rule succeeded, `fail` triggers when a rule returns a failure result, and `error` captures invalid configuration or rule
  errors.
- Within each category, allow templates to read the original raw request snapshot plus any variables emitted by rules.
- Provide `allowHeaders` / `stripHeaders` / `customHeaders` tooling and `body`/`bodyFile` templating to construct the final HTTP
  response. Endpoint response policy owns status codes and bodies, while rule responses may layer additional headers via their
  `responses.*.headers` directives. Header overrides may use Go templates (with Sprig) or JMESPath expressions.
- Default behavior, when a category is unspecified, returns the canonical forward-auth statuses (200 on pass, 401/403 on fail,
  5xx on error). The `/auth` response body remains minimal (outcome, message, endpoint, correlation ID, cache flag) to avoid
  exposing internal state; use `/explain` and logs for detailed diagnostics.

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
