# Endpoint and Rule YAML Skeleton

This document captures the proposed YAML layout for Rest ForwardAuth v2 server, endpoint, and rule configuration. Optional keys are annotated with `# optional`. Unless otherwise noted, omission of an optional block falls back to the conservative defaults described in [decision-model.md](decision-model.md) and [request-flows.md](request-flows.md).

Conditional predicates and variable extractions are expressed as CEL programs that compile at configuration load, while string values such as headers, query parameters, and bodies continue to use Go `text/template` fragments (with the full Sprig helper set available). When a field calls out a specific evaluation order, that rule takes precedence; for example, rule conditions run as CEL directly against the prepared activation without first rendering templates. The runtime’s CEL environment includes helpers like `lookup(map, key)` that return `null` for missing entries so rules can safely probe optional headers, query parameters, variables, or backend fields.

## Server Object

```yaml
server:
  listen:
    address: "0.0.0.0"             # optional — bind interface; 0.0.0.0 listens on all addresses
    port: 8080                      # optional — TCP port for HTTP server
  logging:
    level: info                     # optional — e.g., debug|info|warn|error
    format: json                    # optional — json or text output
    correlationHeader: "X-Request-ID"  # optional — header captured/emitted for request correlation
  rules:
    rulesFolder: "./rules"         # optional — directory watched for YAML changes when set
    rulesFile: ""                  # optional — static YAML file loaded once at startup when set
  templates:
    templatesFolder: "./templates" # optional — root directory for template lookups (jail)
  variables:
    environment:                   # optional — environment variables loaded at startup
      timezone: TZ                 # explicit mapping: exposes as variables.environment.timezone
      HOME: null                   # null-copy: exposes as variables.environment.HOME
    secrets:                       # optional — Docker secrets from /run/secrets/ loaded at startup
      db_password: null            # null-copy: reads /run/secrets/db_password
      api_key: api_token           # reads /run/secrets/api_token, exposes as variables.secrets.api_key
```

### Notes
- `listen.address` and `listen.port` define the socket the server binds to; defaults may map to the Go HTTP server defaults if
  omitted. When running behind a proxy, operators can target loopback or unix sockets by extending this block.
- The `logging` block controls the global logger. `correlationHeader` names the inbound request header used to seed correlation
  IDs; when present, the runtime also emits the same header on responses. Implementers should surface this value in structured
  logs and tracing spans.
- The `config` block governs how endpoint and rule configuration files are sourced. The runtime accepts YAML, TOML, or HUML
  documents (detected by file extension) and relies on `koanf` parsers to normalize them into a shared internal structure:
  - When `rulesFolder` is set (default `./rules`), the runtime recursively loads YAML/TOML/HUML documents from that directory and
    watches for changes. File watching is expected to use [`koanf`](https://github.com/knadh/koanf) with the `fs` provider plus a
    filesystem watcher so edits hot-reload without restarts.
  - When `rulesFile` is set (YAML/TOML/HUML), the runtime loads that single file at startup and does **not** re-read it automatically. Setting
    both `rulesFolder` and `rulesFile` should be treated as invalid configuration.
- Rule and endpoint definitions must be unique across all loaded documents. When duplicate rule names are detected, the runtime
  skips every conflicting definition, logs a warning/error, and marks any dependent endpoints unhealthy (responding with 5xx).
  Duplicate endpoint names trigger an immediate 5xx response for that endpoint, surface a health failure, and are logged as
  configuration errors. These conditions should never be cached because they stem from configuration issues rather than backend
  behavior. The loader records each quarantine inside `SkippedDefinitions` so health surfaces can explain which files and
  names collided.
- Endpoint validation ensures every rule referenced in its `rules` chain was loaded successfully. Missing or disabled rules cause
  the endpoint to serve 5xx responses and advertise an unhealthy status until the configuration is corrected. The loader
  annotates these cases as `SkippedDefinitions` entries (`missing rule dependencies: <rule>`) so operators can fix the right
  document without re-parsing raw YAML.
- Any configuration change that affects an endpoint, its rule chain, or individual rules must invalidate cached decisions for that
  endpoint. Rule outputs can feed later rules, so hot-reloading configurations without clearing caches risks serving stale or
  inconsistent results.
- Rule parsing must tolerate operator mistakes: invalid templates or CEL programs disable the rule and emit structured
  warnings without stopping the server. Extra or unrecognized keys inside a rule definition are treated the same way—log the
  offending keys, disable the rule, and continue running so operators can fix the config.
- Server-level configuration is stricter. Unknown or invalid keys in the top-level `server` block are logged and should cause the
  process to terminate with a non-zero exit code so container orchestrators notice the failure.
- The `templates.templatesFolder` value establishes the root path for response and request templates. All template lookups are resolved
  relative to this directory, and path traversal outside the folder must be rejected to keep template rendering sandboxed.
- `variables.environment` configures environment variables loaded at startup and exposed as `variables.environment.*` in both CEL expressions
  and templates. Uses null-copy semantics: `key: null` reads the environment variable named `key`, while `key: "ENV_VAR"` reads `ENV_VAR`
  and exposes it as `variables.environment.key`. All referenced environment variables must exist at startup or the server will fail to start
  with a configuration error. This fail-fast approach ensures required environment variables are present before accepting any traffic.
- `variables.secrets` configures Docker secrets (or any file-based secrets) loaded from `/run/secrets/` at startup and exposed as
  `variables.secrets.*` in both CEL expressions and templates. Uses null-copy semantics: `key: null` reads `/run/secrets/key`, while
  `key: "filename"` reads `/run/secrets/filename` and exposes it as `variables.secrets.key`. Trailing newlines and carriage returns are
  automatically trimmed (Docker adds these to secret files). All referenced secret files must exist and be readable at startup or the server
  will fail to start. This fail-fast approach is ideal for Docker/Kubernetes deployments where secrets are mounted as files.
- These server-level settings complement the endpoint and rule configuration described below. The runtime should load the full
  configuration via `koanf` so it can merge environment overrides, watch folders when supported, and provide consistent access to
  configuration values across packages. Configuration precedence follows Docker-friendly expectations: environment variables win
  over file-sourced values, which in turn override hard-coded defaults.

## Endpoint Object

```yaml
endpoints:
  <endpoint-name>:
    description: ""                    # optional — human-readable summary
    authentication:                    # optional — omit when anonymous access is permitted
      required: false                  # optional — defaults to true; set false to allow rules to run without credentials
      allow:                           # required when authentication is declared; at least one provider must be enabled
        authorization: ["basic", "bearer"]  # optional — list of Authorization header schemes (valid: "basic", "bearer")
        header: []                     # optional — header names inspected for tokens (e.g., ["X-Api-Key"])
        query: []                      # optional — query parameter names for tokens (e.g., ["token", "api_key"])
        none: false                    # optional — allow anonymous requests to proceed
      challenge:                       # optional — WWW-Authenticate response for failures
        type: bearer                   # optional — basic|bearer (controls header syntax)
        realm: ""                      # optional — realm advertised to clients
        charset: "UTF-8"               # optional — only valid for basic challenges
      response:                        # optional — customize admission failure responses (401)
        status: 401                    # optional — override default 401 status
        headers:                       # optional — additional headers merged with WWW-Authenticate
          retry-after: "120"           # e.g., rate limiting hint
          x-auth-hint: "Bearer token required"
        body: |                        # optional — inline template for response body
          {"error": "authentication_required", "hint": "POST /login"}
        bodyFile: "templates/401.json" # optional — template file (alternative to inline body)

### Admission Response Customization (`authentication.response`)

The optional `response` block customizes the HTTP response rendered when admission fails (credentials missing and `required: true`).
Admission failures **short-circuit the pipeline**, skipping forward policy, endpoint variables, rule chain, and response policy
agents for performance (~7 agent executions avoided).

**Default Behavior (without `response` config):**
- Status: `401 Unauthorized`
- Headers: `WWW-Authenticate` from `challenge` config (e.g., `Basic realm="api"`)
- Body: `"authentication required"`

**Custom Behavior (with `response` config):**
```yaml
authentication:
  required: true
  challenge:
    type: basic
    realm: "api"
  response:
    status: 401              # Optional override (default 401)
    headers:                 # Merged with WWW-Authenticate (never replaced)
      retry-after: "120"
      x-auth-hint: "Use POST /login to obtain credentials"
    body: |                  # Inline template (supports full pipeline state)
      {
        "error": "authentication_required",
        "realm": "{{ .admission.challenge.realm }}",
        "endpoint": "{{ .endpoint }}"
      }
    bodyFile: "401.json"     # Alternative: template file path
```

**Key Properties:**
- **Headers merge**: Custom headers from `response.headers` are added to (not replacing) the `WWW-Authenticate` header
- **Template support**: Both `body` and `bodyFile` support Go templates with Sprig helpers and full pipeline state context
- **Fail-closed**: If template rendering fails, falls back to default "authentication required" message
- **Backwards compatible**: Omitting `response` block maintains existing behavior

**Use Cases:**
- Custom error messages for different endpoint types (JSON API vs HTML web app)
- Rate limiting hints (`Retry-After` header)
- Redirect hints for browser-based flows
- Localized error messages via template conditionals

### Anonymous Authentication (`none: true`)

Setting `allow.none: true` permits unauthenticated requests to pass admission and reach the rule chain. This is useful for:
- **Context enrichment**: Forwarding authentication status to backends via custom headers
- **Optional authentication**: Providing differentiated service levels for authenticated vs anonymous users

**Important limitations:**
- Endpoints with `none: true` have **caching automatically disabled** to prevent cache poisoning
- All anonymous requests execute the full rule chain on every invocation
- For truly public paths that don't require PassCtrl evaluation, configure your ingress to bypass PassCtrl entirely

**Anti-pattern:**
```yaml
authentication:
  allow:
    none: true
rules:
  - name: check-ip  # ⚠️ Request-specific logic with none=true creates security risk
    conditions:
      pass:
        - request.remoteAddr in ["10.0.0.1"]
```

When `none: true` is combined with rules that evaluate request-specific data (IP addresses, user agents, etc.), the automatic cache disablement prevents one anonymous user from benefiting from another's cached decision, which could bypass security checks.

### Cache Key Security

Cache keys are generated by extracting the credential from the request in the same priority order as admission:
1. Authorization header (if allowed)
2. Custom headers (if allowed)
3. Query parameters (if allowed)
4. IP address (fallback when no credentials are present)

This ensures that different users never share cache entries, preventing cache poisoning attacks. For example, users authenticated via `x-session-token` header will have isolated cache entries based on their token value, not their IP address.

    forwardProxyPolicy:                # optional — omit to trust only the direct client IP
      trustedProxyIPs: []              # optional — list of CIDRs that may set X-Forwarded-*
      developmentMode: false           # optional — strip instead of reject on untrusted peers
    forwardRequestPolicy:              # required — curates what rules/backends may see
      forwardProxyHeaders: false       # optional — expose sanitized X-Forwarded-* downstream
      headers:                         # optional — null-copy semantics: null = copy from request, value = static/template
        x-request-id: null             # copy from request (null-copy)
        x-custom-header: "static"      # static value
        x-templated: "{{ .request.headers.authorization }}"  # template value
      query:                           # optional — null-copy semantics: null = copy from request, value = static/template
        page: null                     # copy from request (null-copy)
        limit: "100"                   # static value override
    rules:                             # required — ordered evaluation list
      - name: rule-a                   # required per entry — references `rules.rule-a`
    responsePolicy:                    # optional — defaults to forward-auth statuses
      pass:                            # optional — executed when all rules pass
        status: 200                    # optional — override default HTTP 200
        body: ""                       # optional — templated body for `/auth`
        bodyFile: ""                   # optional — render body from template file
        headers:                       # optional — null-copy semantics: null = copy from raw, value = static/template
          x-request-id: null           # copy from raw request (null-copy)
          x-auth-status: "success"     # static value
          x-user-id: "{{ .response.user_id }}"  # template from response variables
      fail:                            # optional — when any rule returns `Fail`
        status: 403                    # optional — override default 403
        body: ""                       # optional — templated body for `/auth`
        bodyFile: ""                   # optional — render body from template file
        headers:                       # optional — null-copy semantics: null = copy from raw, value = static/template
          x-auth-status: "denied"      # static value
      error:                           # optional — configuration or rule `Error`
        status: 502                    # optional — override default 502
        body: ""                       # optional — templated body for `/auth`
        bodyFile: ""                   # optional — render body from template file
        headers:                       # optional — null-copy semantics: null = copy from raw, value = static/template
          x-error: "backend-unavailable"  # static error indicator
    cache:                             # optional — endpoint-level memoization controls
      resultTTL: 0s                    # optional — alias for cacheResultDuration
```

### Null-Copy Header and Query Parameter Semantics

PassCtrl uses **null-copy semantics** for headers and query parameters in three contexts:
- `forwardRequestPolicy` (endpoint-level curation of what rules see)
- `backendApi` (rule-level backend requests)
- `responsePolicy` (endpoint-level response headers)

**Semantics**:
- `null` value — Copy from raw incoming request (null-copy)
- Non-null value — Static string or template expression

**Examples**:

```yaml
# Forward Request Policy - curate what rules see
forwardRequestPolicy:
  headers:
    x-request-id: null              # Copy from client request
    x-api-version: "v1"             # Static value
    x-user-agent: "{{ .request.headers.user-agent }}"  # Template
  query:
    page: null                      # Copy from client request
    limit: "100"                    # Override with static value

# Backend API - headers for backend requests
backendApi:
  headers:
    x-trace-id: null                # Copy from curated request
    content-type: "application/json"  # Static value
    authorization: null             # DO NOT USE - use auth.forwardAs instead
  query:
    user_id: null                   # Copy from curated request
    format: "json"                  # Static value

# Response Policy - headers for client response
responsePolicy:
  pass:
    headers:
      x-request-id: null            # Echo back from raw request
      x-auth-status: "success"      # Static value
      x-user-id: "{{ .response.user_id }}"  # Template from response variables
```

**Key Behaviors**:
- **Normalization**: All header names normalized to lowercase for consistent access
- **Empty values**: Empty template results are omitted from output (not sent as empty strings)
- **Missing keys**: Null-copy of missing header/query param silently omitted (no error)
- **Security**: Authorization headers in `backendApi.headers` are rejected at config validation — use `auth.forwardAs` instead
- **Template context**: Access incoming request via `.request.headers` and `.request.query` in templates

**Migration from allow/strip/custom**:
- Old `allow: ["x-foo"]` → New `x-foo: null`
- Old `custom: {x-bar: "value"}` → New `x-bar: "value"`
- Strip behavior: Simply omit the header/query key (not in map = not forwarded)

## Rule Object

```yaml
rules:
  rule-a:
    description: ""                    # optional — human-readable summary
    auth:                              # optional — omit to inherit endpoint admission result; array of match groups
      # Simple bearer token acceptance (pass-through when forwardAs is omitted)
      - match:
          - type: bearer

      # Transform basic auth to bearer token
      - match:
          - type: basic
        forwardAs:
          - type: bearer
            token: "{{ .auth.input.basic.password }}"

      # Match bearer token with value constraint (regex pattern)
      - match:
          - type: bearer
            value: "/^admin-/"         # Regex pattern for admin tokens
        forwardAs:
          - type: bearer
            token: "ADMIN-{{ .auth.input.bearer.token }}"

      # Compound admission: require BOTH bearer token AND username header
      - match:
          - type: bearer
          - type: header
            name: X-Username
        forwardAs:
          - type: basic                # Compose basic auth from multiple sources
            user: "{{ index .auth.input.header \"x-username\" }}"
            password: "{{ .auth.input.bearer.token }}"

      # Multiple outputs: emit credentials in multiple formats simultaneously
      - match:
          - type: header
            name: PRIVATE-TOKEN
        forwardAs:
          - type: bearer               # Emit as bearer token
            token: "{{ index .auth.input.header \"private-token\" }}"
          - type: header               # AND as custom header
            name: X-Api-Key
            value: "{{ index .auth.input.header \"private-token\" }}"
          - type: query                # AND as query parameter
            name: api_key
            value: "{{ index .auth.input.header \"private-token\" }}"

      # Value matching with multiple patterns (literal and regex)
      - match:
          - type: bearer
            value: ["literal-token", "/^pattern-/", "/service-[0-9]+$/"]
        forwardAs:
          - type: bearer
            token: "{{ .auth.input.bearer.token }}"

      # Synthesize credentials when none provided (type: none)
      - match:
          - type: none
        forwardAs:
          - type: basic
            user: service
            password: "{{ .variables.api_key }}"
    backendApi:                        # optional — omit when the rule is static
      url: "https://api.example"       # required when backendApi is present
      method: GET                      # optional — default GET
      forwardProxyHeaders: false       # optional — reuse sanitized proxy headers
      headers:                         # optional — null-copy semantics: null = copy from raw, value = static/template
        x-request-id: null             # copy from raw request (null-copy)
        content-type: "application/json"  # static value
        x-api-key: "{{ .auth.input.bearer.token }}"  # template value (DO NOT use for Authorization - use auth.forwardAs)
      query:                           # optional — null-copy semantics: null = copy from raw, value = static/template
        user_id: null                  # copy from raw request (null-copy)
        limit: "50"                    # static value
      body: ""                         # optional — templated request body (per page when paginating)
      bodyFile: ""                     # optional — templated path to a body template file (rendered per request)
      acceptedStatuses: [200]          # optional — success codes (default 2xx)
      pagination:                      # optional — pagination handling strategy
        type: link-header              # optional — e.g., link-header, token
        maxPages: 1                    # optional — safety bound
    conditions:                        # optional — defaults to backend status
      pass: []                         # optional — CEL predicates overriding pass; compiled at load and executed against the rule activation
      fail: []                         # optional — CEL predicates overriding fail; compiled at load and executed against the rule activation
      error: []                        # optional — CEL predicates overriding error; compiled at load and executed against the rule activation
    responses:                         # optional — outcome-specific variable exports
      pass:
        variables:                     # optional — exported to subsequent rules AND endpoint response templates
          user_id: backend.body.userId        # CEL or template expression
          tier: backend.body.tier
          display_name: backend.body.displayName
          email: backend.body.email
      fail:
        variables: {}                  # optional — exported to subsequent rules AND endpoint templates
      error:
        variables: {}                  # optional — exported to subsequent rules AND endpoint templates
    variables:                         # optional — local/temporary variables for rule logic only (not cached, not exported)
      temp_user_id: backend.body.userId           # CEL expression (no {{)
      temp_tier: backend.body.tier
      cache_key: "user:{{ .backend.body.userId }}"  # Template (contains {{)
    cache:                             # optional — decision memoization
      followCacheControl: false        # optional — honor backend cache headers
      passTTL: 0s                      # optional — cache duration for pass outcomes
      failTTL: 0s                      # optional — cache duration for fail outcomes
      includeProxyHeaders: true        # optional — include proxy headers in cache key (default: true)
```

### Notes
- Rules referenced inside an endpoint's `rules` list must have corresponding entries under `rules:`.
- Endpoint caches expire immediately when any contributing rule cache lapses; 5xx/error outcomes are never cached.
- **Cache Key Proxy Headers** (`includeProxyHeaders`):
  - When `true` (default): Proxy headers in backend requests are included in the cache key hash
  - When `false`: Proxy headers are excluded from cache key computation
  - **Security Warning**: Setting `includeProxyHeaders: false` can cause cache correctness issues if backends use client IP, geo-location, or proxy metadata for decision-making
  - Excluded headers: `x-forwarded-for`, `x-real-ip`, `true-client-ip`, `cf-connecting-ip`, `forwarded`, and other proxy/CDN headers
  - Use `includeProxyHeaders: false` **only** when certain backends don't rely on proxy headers for responses
  - When in doubt, leave as `true` (default) to prevent data leakage or access control bypass
- The `auth` block is an ordered array of **match groups**. Each match group contains:
  - `match`: Array of credential matchers that ALL must succeed (AND logic within group)
  - `forwardAs`: Array of credential outputs to emit (optional; omit for pass-through mode)
- **Match Group Evaluation** (OR between groups, AND within groups):
  - Groups are evaluated sequentially
  - Within each group, ALL matchers must succeed (AND logic)
  - First group where ALL matchers succeed wins
  - If no group matches and no `type: none` is present, rule fails before backend invocation
- **Credential Matchers** accept these types: `basic`, `bearer`, `header`, `query`, `none`
  - `basic`: Matches Basic Authorization header; optionally constrain via `username` and/or `password` value patterns
  - `bearer`: Matches Bearer Authorization header; optionally constrain via `value` patterns
  - `header`: Matches specific header by `name` (required); optionally constrain via `value` patterns
  - `query`: Matches specific query parameter by `name` (required); optionally constrain via `value` patterns
  - `none`: Always matches; used to synthesize credentials when client provides none
- **Value Constraints** filter credentials by their values (optional for bearer, header, query, basic username/password):
  - Single string: `value: "literal-match"` — exact match
  - Regex pattern: `value: "/^admin-/"` — pattern delimited by forward slashes
  - Array: `value: ["literal", "/pattern-1/", "/pattern-2/"]` — matches any (OR logic)
  - If no value constraint provided, any credential of that type matches
  - Regex uses Go's RE2 engine; patterns compile at config load time
- **Template Context** for matched credentials (`.auth.input.*`):
  - `.auth.input.bearer.token` — bearer token string
  - `.auth.input.basic.user`, `.auth.input.basic.password` — basic auth components
  - `.auth.input.header` — map of lowercase header names to values (access via `{{ index .auth.input.header "x-name" }}`)
  - `.auth.input.query` — map of query parameter names to values
  - All matched credentials in the winning group are accessible regardless of which matcher specified them
- **Forward Outputs** (`forwardAs` array):
  - Omit `forwardAs` for pass-through mode (credentials forwarded unchanged after stripping)
  - Each forward output specifies `type` and type-specific fields:
    - `basic`: requires `user` and `password` templates
    - `bearer`: requires `token` template
    - `header`: requires `name` template and optional `value` (defaults to empty if not provided)
    - `query`: requires `name` template and optional `value`
    - `none`: emits nothing (consume credentials without forwarding)
  - Multiple outputs supported: emit same credential as bearer + header + query simultaneously
  - All outputs rendered using Go templates with Sprig helpers
  - Duplicate outputs (same header or query name) cause compile-time validation error
- **Security Model** (explicit credential stripping):
  - All credential sources mentioned in ANY match group across the entire auth block are stripped from forwarded request
  - Only the winning group's `forwardAs` outputs are then applied to the backend request
  - Fail-closed: missing credentials fail evaluation; no implicit forwarding
  - This ensures credentials don't leak through unintended paths

### Response Model & Variable Separation

PassCtrl enforces clear separation between rule chain logic and endpoint response formatting:

**Rule Outcomes are Trinary**:
- Rules produce exactly one outcome: `pass`, `fail`, or `error`
- No other data flows directly from rules to client responses
- Endpoints use these outcomes to select which `responsePolicy.pass/fail/error` block to execute

**Endpoints Own Response Format**:
- Status codes, headers, and body templates are defined entirely in endpoint `responsePolicy` blocks
- Rules cannot override or suggest status codes or body content
- Endpoints control the complete client-facing response structure

**Two Types of Variables**:

1. **Local Variables** (`variables`):
   - Purpose: Temporary calculations within a single rule
   - Scope: Available only within the defining rule (for conditions, backend templates, exported variables)
   - Cached: **No** — ephemeral, discarded after rule evaluation
   - Access: Rules can reference via `variables.<name>`, endpoints cannot access
   - Examples: intermediate calculations, cache keys, formatted strings

2. **Exported Variables** (`responses.pass/fail/error.variables`):
   - Purpose: Share data with subsequent rules AND endpoint response templates
   - Scope: Available to subsequent rules via `.rules.<rule>.variables.<name>` AND to endpoint templates via `.response.<name>`
   - Cached: **Yes** — stored with decision outcomes for cache replay
   - Access: Both rule chain and endpoint response rendering
   - Examples: user IDs, display names, email addresses, tier levels, session IDs
   - Extraction: Same hybrid CEL/Template evaluation as local variables (auto-detected by `{{` presence)

**Template Context in Endpoint Responses**:

Endpoint `responsePolicy.pass/fail/error` templates have access to:
- `.endpoint` — endpoint name
- `.correlationId` — request correlation ID
- `.auth.input.*` — matched credentials from admission
- `.backend.*` — backend response from decisive rule (status, headers, body if JSON-parsed)
- `.response.*` — exported variables from decisive rule via `responses.*.variables`
- Standard helpers: `lookup()`, Sprig functions, `env` (if allowed)

**Example**:

```yaml
rules:
  lookup-user:
    backendApi:
      url: "https://users.internal/api/lookup"
      method: GET
    variables:
      # Local temporaries (not cached, not exported)
      temp_user_id: backend.body.userId
      temp_tier: backend.body.tier
    responses:
      pass:
        # Exported to subsequent rules AND endpoint response templates
        variables:
          user_id: variables.temp_user_id
          tier: variables.temp_tier
          display_name: backend.body.displayName
          email: backend.body.email
          account_status: backend.body.status

endpoints:
  api-gateway:
    rules:
      - name: lookup-user
    responsePolicy:
      pass:
        status: 200
        headers:
          custom:
            X-User-ID: "{{ .response.display_name }}"
            X-Account-Status: "{{ .response.account_status }}"
        body: |
          {
            "user": "{{ .response.display_name }}",
            "email": "{{ .response.email }}",
            "status": "{{ .response.account_status }}"
          }
      fail:
        status: 403
        body: '{"error": "user lookup failed"}'
```

**Migration from Old Model**:

The old model allowed rules to set headers directly, mixing response formatting with authorization logic. The new model enforces complete separation:

Old model (REMOVED):
```yaml
rules:
  my-rule:
    responses:
      pass:
        headers:  # ❌ Rules can no longer set headers
          custom:
            X-User-ID: "{{ .backend.body.userId }}"
```

New model:
```yaml
rules:
  my-rule:
    responses:
      pass:
        variables:  # ✅ Export data for endpoint use
          user_id: backend.body.userId

endpoints:
  my-endpoint:
    responsePolicy:
      pass:
        headers:  # ✅ Endpoint constructs all headers
          custom:
            X-User-ID: "{{ .response.user_id }}"
```

**Key Changes**:
- **Rules**: Only export variables; no header/status/body configuration
- **Endpoints**: Own complete response format using exported variables
- **Benefit**: Clear separation of concerns (authorization logic vs response formatting)
- CEL conditions execute against the activation assembled in `internal/runtime/rule_execution_agent.go`.
  Expressions should reference the documented maps (`raw`, `admission`, `forward`,
  `backend`, `vars`, `now`) instead of the early `request.*` identifiers so
  configuration validation succeeds.
- Response directives start from the backend status code and header set by default; endpoint or rule `strip`/`custom` directives adjust the replayed values.
