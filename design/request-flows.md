# Request Flows

This document captures the canonical flows we expect the v2 runtime to support. Each flow assumes the endpoint first establishes
its admission requirements (trusted proxies, authentication, and raw state capture), applies the forward request policy to
prepare what rules may see, evaluates the ordered rules, and finally renders a response through the response policy. Unless a field is documented as static, configuration-driven strings use CEL (Common Expression Language) for conditions and variable extraction, while Go templates (with Sprig helpers) render headers, query parameters, and response bodies. The runtime exposes helpers such as `lookup(map, key)` so conditions can safely inspect optional headers, query parameters, or backend fields without triggering evaluation errors.

## 1. Authentication Gateway
1. Client calls `/auth` on an endpoint configured for credential validation.
2. Runtime enforces trusted proxy policy, extracts the original URL from `X-Forwarded-*` (or RFC7239 `Forwarded`), and records the raw request state
   (method, headers, query, body digests) as `rawState`.
3. Endpoint-level authentication evaluates credentials; failure exits through the response policy's `fail` branch.
4. Forward request policy sanitizes proxy metadata headers (`X-Forwarded-*` and RFC7239 `Forwarded`) when `forwardProxyHeaders`
   is enabled. Backend request headers and query parameters use **null-copy semantics**: `nil` value copies from raw request,
   non-nil value uses static string or rendered template expression.
5. A single rule validates credentials (e.g., static secret, OIDC introspection) and emits an allow/deny decision using only the
   curated request data.
6. On **allow**, the response policy’s `pass` block returns HTTP 200 with optional metadata headers; on **deny**, it returns the
   configured `fail` status and challenge.

### Notes
- Audit logs capture which rule matched and why (e.g., token expired) alongside the raw state snapshot ID and curated request map.

## 2. Authorization With Backend Signal
1. Client calls `/auth` with an access token and contextual headers.
2. Runtime authenticates the token and builds the raw state snapshot.
3. Forward request policy sanitizes proxy headers when `forwardProxyHeaders` is enabled; backend request configuration uses
   null-copy semantics to select headers and query parameters from the raw request.
4. The rules execute in order; the backend authorization rule performs the call (following any pagination the backend requires)
   using null-copy header/query selection from the raw request, relies on its ordered `auth` directives (each either forwarding
   the matched credential as-is or applying a `forwardAs` mapping that can reference `.auth.input.*` to transform it for the
   backend), and exports variables (such as subject attributes) with the appropriate scopes. Rule conditions compile as CEL and
   execute directly against the backend response and pipeline state, variable extractors use CEL (or Go templates when detected by
   `{{` presence), and response header/body values render via Go templates with Sprig helpers.
5. If allowed, the response policy `pass` block may add headers or body content using the same templating options while keeping
   the decisive rule’s status code and header set unless overridden.
6. When the rule (or backend) supplies cache directives, the runtime stores only the decision artifacts—outcome, exported
   variables, and rendered response metadata—for the permitted duration, respecting the rule’s `cache` block (including separate
   pass/fail TTLs and whether to follow backend headers).

### Notes
- Backend calls may reuse cached decisions without replaying the HTTP exchange; the backend response body is never cached beyond
  the lifetime of the originating request, and 5xx results are never cached.
- Fail outcomes can be cached when a rule supplies a `failTTL`, but endpoints drop back to live evaluation once that TTL elapses
  or when the backend omits cache headers and the configuration chooses not to override them.
- Response policy defaults still return standard forward-auth statuses when not customized.

## 3. Health and Explain
1. Operators or orchestrators call `/health` (alias `/healthz`) to ensure the endpoint compiled successfully.
2. Scoped probes target `/<endpoint>/healthz` (accepting `/<endpoint>/health` as a compatibility alias) so orchestrators can
   focus on a single endpoint.
3. Developers call `/explain` (development mode only) to inspect how rules were compiled and what cost was assigned.
4. Neither path executes rule chains; they surface configuration or compilation issues instead.

### Notes
- `/health` should degrade gracefully when dependencies (e.g., schema registries) are unavailable but configuration is valid.
- `/explain` exposes the same decision graph used for documentation and tooling.
- Both surfaces report the active rule sources, skipped definitions, and whether the runtime is using the fallback endpoint so
  operators can diagnose reload issues without replaying `/auth`.
- Endpoints may memoize `/auth` decisions via their own `cacheResultDuration`, carrying forward pass or fail results until the
  endpoint TTL or any contributing rule TTL expires. Health/explain calls always report live configuration state and never reuse
  cached artifacts for backend 5xx results.

These flows form the baseline scenarios we optimize for in v2, keeping the runtime predictable while still accommodating complex
policies where required.
