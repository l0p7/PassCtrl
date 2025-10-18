# PassCtrl Server Usage

This guide explains how to bootstrap, configure, and operate the PassCtrl forward-auth server. It is formatted for MkDocs so it can
serve as the landing page for a `docs/` site.

## Prerequisites
- Go 1.25 or newer
- Access to configuration files (YAML, TOML, HUML) or environment variables that follow the documented schema
- Optional: MkDocs for serving this documentation (`pip install mkdocs`)

## Installation
```bash
# clone the repository
git clone https://github.com/l0p7/passctrl.git
cd PassCtrl

# download dependencies
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache go mod download
```

To build the binary:
```bash
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache go build -o passctrl ./cmd
```

## Configuration Overview
PassCtrl loads configuration through the Server Configuration & Lifecycle agent. Inputs can come from:

1. **Defaults** – sensible baseline values applied when nothing else is provided.
2. **Config files** – supply one or more files via `--config` or by wiring the loader to a rules folder/file.
3. **Environment variables** – highest priority; use the `PASSCTRL_` prefix with double underscores to represent nesting (e.g.
   `PASSCTRL_SERVER__LISTEN__PORT=8081`).

The server configuration block mirrors the design schema:

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
  templates:
    templatesFolder: "./templates"
    templatesAllowEnv: false
    templatesAllowedEnv: []
```

For full schema details refer to `design/config-structure.md`.

## Running the Server
Use the entrypoint under `cmd/main.go`:
```bash
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache go run ./cmd --config config/server.yaml
```

Command-line flags:
- `--config` – path to the primary server configuration file.
- `--env-prefix` – environment variable prefix (default `PASSCTRL`).

The process listens on `listen.address:listen.port` once configuration loads successfully.

## Environment Variable Mapping
Environment keys follow the pattern:

- Double underscores (`__`) separate nested objects.
- Remaining underscores are dropped to keep camelCase keys readable.

Examples:
```bash
export PASSCTRL_SERVER__LISTEN__PORT=9090
export PASSCTRL_SERVER__TEMPLATES__TEMPLATESALLOWENV=true
export PASSCTRL_SERVER__TEMPLATES__TEMPLATESALLOWEDENV=CLIENT_ID
```

## Observability
PassCtrl uses `log/slog` for structured logging. Configure log level and format via the `server.logging` block. The correlation header
is attached automatically to every log once set in the configuration, and the `/auth` response mirrors the identifier in both the HTTP
headers and a minimal response body so downstream systems can trace decisions end-to-end. Enabling `server.logging.level: debug` also emits an
inbound request snapshot (method, path, proxy metadata) and a post-decision summary covering admission results, cache participation,
and backend usage. For detailed runtime metadata, prefer the `/explain` endpoint and logs rather than `/auth` responses, which purposely
avoid exposing internal state.

## Development Quality Gates
- Run unit tests with `go test ./...` to exercise runtime agents and configuration loaders.
- Enforce static analysis and formatting with `golangci-lint run ./...`. The repository ships a baseline `.golangci.yml` that
  enables `errcheck`, `gofmt`, `goimports`, `govet`, `staticcheck`, `ineffassign`, `misspell`, `unparam`, and
  `prealloc` to catch error-handling, formatting, and data-flow regressions early. The CI workflow rebuilds
  `golangci-lint` with the Go 1.25 toolchain so analyzer behavior stays consistent between local and hosted runs. Install the
  Go 1.25 toolchain locally (for example with `go toolchain install go1.25.0`) and rebuild `golangci-lint` with that toolchain
  (`go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`) before running the suite.

## Integration Testing
PassCtrl ships an opt-in CLI integration test under `cmd/integration_test.go`. The test boots the server via `go run` with a
temporary configuration, waits for `/auth` readiness, and performs a smoke request. To avoid long-running processes during regular
unit test runs, the suite is skipped by default. Enable it with:

```bash
PASSCTRL_INTEGRATION=1 go test ./cmd -run TestIntegrationServerStartup -count=1
```

The harness automatically allocates a free loopback port and captures server stdout/stderr when the test fails so you can inspect
startup issues without scrolling through standard test output.

## HTTP Surface
Initial routes exposed by the runtime:
- `/<endpoint>/auth` – forward-auth decision endpoint. Callers route requests to a configured endpoint
  by including its name in the path. When only a single endpoint is active, `/auth` continues to
  resolve to that default.
- `/<endpoint>/healthz` – readiness probe scoped to a specific endpoint (`/<endpoint>/health` is
  accepted as a compatibility alias). `/health` and `/healthz` remain available for aggregate status
  when endpoint scoping is unnecessary.
- `/<endpoint>/explain` – exposes compiled rule metadata, skipped definitions, and cache health for
  the requested endpoint. `/explain` returns the aggregate view when scoping is unnecessary.

These routes will evolve as agents (Admission, Forward Request Policy, Rule Chain, etc.) are implemented.

## Next Steps
- Define endpoints and rules under the `rules` directory to shape forward-auth logic.
- Review the [Configuration Deep Dive](configuration.md) for precedence rules and
  layout suggestions when structuring your deployment.
- Study the [Runtime Agents Overview](agents.md) to understand how each component
  participates in the decision pipeline.
- Consult the design documents in `design/` whenever behavior changes to keep
  documentation and runtime aligned.
- Explore the worked examples under `examples/configs/` and the new
  configuration suites in `examples/suites/` to see complete setups for rules
  folders, Redis-backed caches, and environment-aware templates.
