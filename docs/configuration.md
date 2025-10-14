# Configuration Deep Dive

PassCtrl derives its behavior from a single `server` configuration block layered from
defaults, configuration files, and environment overrides. This page explains how the
pieces fit together and references the design contracts that govern validation rules.

## Loading Strategy

The Server Configuration & Lifecycle agent hydrates configuration with
[`koanf`](https://github.com/knadh/koanf), merging inputs in the following order:

1. Built-in defaults that keep the server usable out of the box.
2. Files supplied with `--config`, a `rulesFile`, or a `rulesFolder` tree.
3. Environment variables using the prefix supplied to the CLI (default `PASSCTRL`).

Later sources override earlier ones, yielding the precedence `env > file > default`.
This allows operators to version configuration files while still overriding single
fields via environment variables in containerised deployments.

Environment keys use double underscores (`__`) to express nesting. Underscores inside
a field name are dropped to keep camelCase properties readable. For example:

```bash
export PASSCTRL_SERVER__LISTEN__PORT=9090
export PASSCTRL_SERVER__LOGGING__LEVEL=debug
export PASSCTRL_SERVER__TEMPLATES__TEMPLATESALLOWEDENV=CLIENT_ID,CLIENT_SECRET
```

## Server Block Reference

The top-level schema mirrors the contract captured in `design/config-structure.md`:

```yaml
server:
  listen:
    address: "0.0.0.0"
    port: 8080
  logging:
    level: info
    format: json
    correlationHeader: "X-Request-ID"
  rules:
    rulesFolder: "./rules"
    rulesFile: ""
  templates:
    templatesFolder: "./templates"
    templatesAllowEnv: false
    templatesAllowedEnv: []
```

### Listener & Logging

- **listen** – Controls the HTTP bind address and port. Bind to `127.0.0.1` when the
  server sits behind a reverse proxy or swap to a Unix socket once support lands.
- **logging** – Select `json` or `text` output, enforce log level, and propagate the
  configured `correlationHeader` into request logs and `/auth` responses for traceability.【F:design/config-structure.md†L15-L45】

### Rules Sources

The runtime accepts structured configuration from a single file (`rulesFile`) or a
folder tree (`rulesFolder`). Only one source should be active at a time; setting both
is treated as invalid configuration and surfaces a startup error. When a folder is
provided the loader recursively watches for changes and reloads definitions so that
operators can publish updates without restarting the server.【F:design/config-structure.md†L32-L70】 Each successful load
captures the contributing filenames in `cfg.RuleSources`, lists duplicate definitions in `cfg.SkippedDefinitions`, and refreshes
`cfg.Endpoints`/`cfg.Rules` so downstream agents consume the latest snapshot. Create the configured folder (or set
`rulesFolder: ""` when relying solely on `rulesFile`) to avoid start-up failures.

### Template Sandboxing

Template rendering resolves paths relative to `templates.templatesFolder`. Attempts to
escape this directory are rejected. Environment access is gated by two fields:

- `templatesAllowEnv` – opt-in flag to expose environment variables.
- `templatesAllowedEnv` – explicit allowlist of environment keys rendered into templates.

The guard rails are mandatory for production deployments so templates cannot leak
arbitrary process state.【F:design/config-structure.md†L71-L93】

### CEL Activation Reference

Rule conditions and variable extracts run inside a curated CEL environment. The
runtime exposes the following top-level objects when evaluating expressions:

- `raw` – immutable request snapshot (`method`, `path`, `host`, `headers`,
  `query`).
- `admission` – admission decision (`authenticated`, `reason`, `clientIp`,
  `trustedProxy`, `proxyStripped`, `forwardedFor`, `forwarded`, `decision`).
- `forward` – curated headers and query parameters the forward policy allows.
- `backend` – last backend response observed by the rule execution agent
  (`requested`, `status`, `headers`, `body`, `bodyText`, `error`, `accepted`,
  `pages`).
- `vars` – global/rule/local variables exported by earlier rules.
- `now` – current UTC timestamp for TTL-style comparisons.

These keys mirror the activation assembled in
`internal/runtime/rule_execution_agent.go` and replace the older `request.*`
identifiers that appeared in early design drafts.

## Endpoint & Rule Documents

Endpoint and rule definitions live alongside the server configuration. Each endpoint
names a set of rules, response policies, and cache directives, while rules describe
credential intake, backend orchestration, and conditional outcomes.
Duplicate names are rejected, missing dependencies render the owning endpoint
unhealthy, and any edit that touches an endpoint or rule invalidates cached decisions
for that endpoint. The loader logs configuration mistakes (invalid templates,
unknown keys) and disables the offending rule without crashing the server.【F:design/config-structure.md†L41-L70】 Use the
`SkippedDefinitions` metadata to surface duplicates, missing rule dependencies
(`missing rule dependencies: <rule>`), or parse failures via the health and explain endpoints.

## Recommended Project Layout

```
config/
  server.yaml          # top-level server defaults
rules/
  endpoints.yaml       # endpoint catalog
  rules/               # optional nested rule files
    login.yaml
    api.yaml
templates/
  responses/           # body/fragment templates
  headers/
```

Mount the `config/`, `rules/`, and `templates/` directories into the container or
bind them into your deployment environment, then point the CLI at the server config:

```bash
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache \
  go run ./cmd --config ./config/server.yaml
```

When using a rules folder, make edits atomically (e.g., write to a temp file and
move it into place) to minimize inconsistent snapshots during reloads.

## Worked Examples

Sample configurations covering common topologies live under
[`examples/`](../examples/README.md):

- [`configs/basic-auth-gateway.yaml`](../examples/configs/basic-auth-gateway.yaml)
  demonstrates a standalone Basic authentication gateway with inline rules and
  templated deny responses.
- [`configs/backend-token-introspection.yaml`](../examples/configs/backend-token-introspection.yaml)
  shows how to call an upstream identity API, forward curated headers, and layer
  multiple rules.
- [`configs/cached-multi-endpoint.yaml`](../examples/configs/cached-multi-endpoint.yaml)
  illustrates mixing cached and uncached endpoints while surfacing rule-chain
  metadata via `/explain`.
- [`suites/rules-folder-bundle`](../examples/suites/rules-folder-bundle/README.md)
  loads endpoints and rules from a watched folder so edits hot-reload without
  restarting the process.
- [`suites/redis-cache-cluster`](../examples/suites/redis-cache-cluster/README.md)
  enables the Redis cache backend, mixes rule-level cache hints, and renders
  pass responses from templates.
- [`suites/template-env-bundle`](../examples/suites/template-env-bundle/README.md)
  opts into the template environment allowlist and demonstrates environment
  driven deny messaging.

Mount any of these files with `--config` to explore how the runtime surfaces
health, explain, and caching state across different rule chains.
