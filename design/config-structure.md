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
    templatesAllowEnv: false        # optional — gate environment variable access in templates
    templatesAllowedEnv:
      - "FORWARDAUTH_CLIENT_ID"    # optional — explicit allowlist of environment variables
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
- `templates.templatesAllowEnv` toggles whether templates may read environment variables. When enabled, the runtime must restrict access to
  the explicit allowlist declared in `templates.templatesAllowedEnv`; any variable not listed is denied. This guard prevents leaking
  sensitive process state while still enabling controlled parameterization.
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
        basic: false                   # optional — accept Authorization: Basic credentials
        bearer: true                   # optional — accept Authorization: Bearer credentials
        header: []                     # optional — header names inspected for tokens
        query: []                      # optional — query parameter names for tokens
        none: false                    # optional — allow anonymous requests to proceed
      challenge:                       # optional — WWW-Authenticate response for failures
        type: bearer                   # optional — basic|bearer (controls header syntax)
        realm: ""                      # optional — realm advertised to clients
        charset: "UTF-8"               # optional — only valid for basic challenges
    forwardProxyPolicy:                # optional — omit to trust only the direct client IP
      trustedProxyIPs: []              # optional — list of CIDRs that may set X-Forwarded-*
      developmentMode: false           # optional — strip instead of reject on untrusted peers
    forwardRequestPolicy:              # required — curates what rules/backends may see
      forwardProxyHeaders: false       # optional — expose sanitized X-Forwarded-* downstream
      headers:
        allow: []                      # optional — explicit allowlist from raw state
        strip: []                      # optional — headers removed after admission
        custom: {}                     # optional — synthesized headers exposed to rules (values rendered via templates)
      query:
        allow: []                      # optional — explicit allowlist of query parameters
        strip: []                      # optional — query parameters removed post-admission
        custom: {}                     # optional — synthesized query parameters (values rendered via templates)
    rules:                             # required — ordered evaluation list
      - name: rule-a                   # required per entry — references `rules.rule-a`
    responsePolicy:                    # optional — defaults to forward-auth statuses
      pass:                            # optional — executed when all rules pass
        status: 200                    # optional — override default HTTP 200
        body: ""                       # optional — templated body for `/auth`
        bodyFile: ""                   # optional — render body from template file
        headers:
          allow: []                    # optional — copy from curated request/rule variables and backend defaults
          strip: []                    # optional — suppress inherited or backend headers
          custom: {}                   # optional — synthetic headers layered on backend defaults (values rendered via templates)
      fail:                            # optional — when any rule returns `Fail`
        status: 401                    # optional — override default 401/403
        body: ""                       # optional — templated body for `/auth`
        bodyFile: ""                   # optional — render body from template file
        headers:
          allow: []                    # optional
          strip: []                    # optional — suppress inherited or backend headers
          custom: {}                   # optional — values rendered via templates
      error:                           # optional — configuration or rule `Error`
        status: 502                    # optional — override default 5xx
        body: ""                       # optional — templated body for `/auth`
        bodyFile: ""                   # optional — render body from template file
        headers:
          allow: []                    # optional
          strip: []                    # optional — suppress inherited or backend headers
          custom: {}                   # optional — values may be templates/JMESPath
    cache:                             # optional — endpoint-level memoization controls
      resultTTL: 0s                    # optional — alias for cacheResultDuration
```

## Rule Object

```yaml
rules:
  rule-a:
    description: ""                    # optional — human-readable summary
    auth:                              # optional — omit to inherit endpoint admission result
      - type: basic                    # optional entries — accept Basic credentials; forwarded unchanged when `forwardAs` is omitted
      - type: basic                    # transforms a captured Basic credential into a Bearer token
        forwardAs:
          type: bearer                 # emit as Authorization: Bearer ...
          token: "{{ .auth.input.password }}"  # Go template drawing from the matched credential
      - type: bearer                   # optional — accept bearer tokens; forwarded unchanged when `forwardAs` is omitted
      - type: bearer                   # prefix the inbound bearer token before forwarding
        forwardAs:
          type: bearer
          token: "ZZZ-{{ .auth.input.token }}"
      - type: header                   # optional — capture named header credentials
        name: PRIVATE-TOKEN            # required when type is header
        forwardAs:
          type: bearer                 # re-emit as a bearer token using the header value
          token: "{{ .auth.input.value }}"
      - type: query                    # optional — capture named query credentials
        name: private_token            # required when type is query
        forwardAs:
          type: header
          name: Authorization
          value: "Bearer {{ .auth.input.value }}"
      - type: none                     # optional — synthesize credentials when none were provided upstream
        forwardAs:
          type: basic
          user: service
          password: "{{ .variables.api_key }}"
    backendApi:                        # optional — omit when the rule is static
      url: "https://api.example"       # required when backendApi is present
      method: GET                      # optional — default GET
      forwardProxyHeaders: false       # optional — reuse sanitized proxy headers
      headers:
        allow: []                      # optional — headers from curated request (literal names, trimmed and lower-case)
        strip: []                      # optional — suppress specific headers
        custom: {}                     # optional — synthesized headers for backend (values rendered via templates)
      query:
        allow: []                      # optional — query parameters to forward (literal names, trimmed and lower-case)
        strip: []                      # optional — suppress specific parameters
        custom: {}                     # optional — synthesized query parameters (values rendered via templates)
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
    responses:                         # optional — defaults to endpoint headers
      pass:
        headers:
          allow: []                    # optional — start from backend/endpoint headers before stripping (literal names)
          strip: []                    # optional
          custom: {}                   # optional — values may be templates/JMESPath
      fail:
        headers:
          allow: []                    # optional — start from backend/endpoint headers before stripping (literal names)
          strip: []                    # optional
          custom: {}                   # optional — values rendered via templates
      error:
        headers:
          allow: []                    # optional — start from backend/endpoint headers before stripping (literal names)
          strip: []                    # optional
          custom: {}                   # optional — values rendered via templates
    variables:                         # optional — share data between rules
      global:                          # optional — `.variables.<name>` everywhere
        subjectID:
          from: ""                     # optional — CEL program projecting values into `variables.global`
      rule:                            # optional — `rules.<rule>.variables.<name>` externally
        enriched:
          from: ""                     # optional — CEL program projecting values into `variables.rule`
      local:                           # optional — visible only inside this rule
        debugInfo:
          from: ""                     # optional — CEL program projecting values into `variables.local`
    cache:                             # optional — decision memoization
      followCacheControl: false        # optional — honor backend cache headers
      passTTL: 0s                      # optional — cache duration for pass outcomes
      failTTL: 0s                      # optional — cache duration for fail outcomes
```

### Notes
- Rules referenced inside an endpoint’s `rules` list must have corresponding entries under `rules:`.
- Endpoint caches expire immediately when any contributing rule cache lapses; 5xx/error outcomes are never cached.
- The `auth` block is an ordered list. Each entry declares an accepted credential source (`type: basic|bearer|header|query|none`) and, when desired, a `forwardAs` mapping describing how that credential is emitted to the backend. Omitting `forwardAs` forwards the credential unchanged in its native form (e.g., Basic remains Basic). When `forwardAs` is present, fields such as `token`, `user`, `password`, `name`, and `value` may be populated via Go templates (with Sprig helpers) or JMESPath expressions.
- Within each `auth` directive, the matched credential is exposed as `.auth.input.*` (e.g., `.auth.input.user`, `.auth.input.password`, `.auth.input.token`, `.auth.input.value`) so transformations can reference the captured value.
- `forwardAs.type` accepts `basic`, `bearer`, `header`, `query`, or `none`. The type selects which additional fields matter:
  - `basic` expects `user` and `password` templates.
  - `bearer` expects a `token` template.
  - `header` expects a `name` and optional `value` template (defaulting to the matched credential for pass-through).
  - `query` expects a `name` and optional `value` template (also defaulting to the matched credential).
  - `none` emits no downstream credential, allowing rules to consume input without forwarding anything.
- Authentication directives are evaluated sequentially; the first directive that matches a captured credential wins. If nothing
  matches and no directive specifies `type: none`, the rule fails before invoking the backend. The credential projected to the
  backend (whether forwarded as-is or via `forwardAs`) is exposed to templates as `.auth.forward.*`.
- CEL conditions execute against the activation assembled in `internal/runtime/rule_execution_agent.go`.
  Expressions should reference the documented maps (`raw`, `admission`, `forward`,
  `backend`, `vars`, `now`) instead of the early `request.*` identifiers so
  configuration validation succeeds.
- Response directives start from the backend status code and header set by default; endpoint or rule `strip`/`custom` directives adjust the replayed values.
