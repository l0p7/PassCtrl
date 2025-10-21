# PassCtrl

PassCtrl captures the forward-looking redesign of the Rest ForwardAuth runtime. The reimplementation effort keeps the spirit of composable
rule chains while making endpoint behavior easier to explain, operate, and audit. Instead of iterating on the existing
configuration engine, the new runtime starts from a clean slate with opinionated building blocks that emphasize intent,
observability, and predictable request handling.

## Architectural Pillars
- **Admission & Raw State** – authenticate requests early, enforce trusted proxies, and persist immutable snapshots for auditing.
- **Forward Request Policy** – curate headers and query parameters that downstream rules and backends may see, with explicit
  allow/strip/custom directives.
- **Rule Chain** – evaluate ordered, named rules that can transform credentials, call backends (with pagination), set variables,
  and emit pass/fail/error results.
- **Response Policy** – render pass/fail/error responses using curated context and rule outputs while preserving default
  forward-auth semantics when overrides are omitted.
- **Result Caching** – memoize decisions for bounded durations without storing backend bodies, and never cache 5xx/error outcomes.

These cooperating agents are documented in detail inside `design/` and mirror the runtime structure described in
[`design/config-structure.md`](design/config-structure.md) and [`design/uml-diagrams.md`](design/uml-diagrams.md).

## Core Scenarios
The design targets three canonical flows that cover the majority of forward-auth use cases:
1. **Authentication Gateway** – credential validation with minimal backend interaction.
2. **Authorization With Backend Signal** – policy decisions driven by external services, credential forwarding, and cache hints.
3. **Health & Explain** – operational endpoints that surface configuration health and compiled rule graphs without executing rule chains.

Each flow is captured step-by-step in [`design/request-flows.md`](design/request-flows.md) and backed by activity diagrams in
[`design/uml-diagrams.md`](design/uml-diagrams.md).

## Configuration Model
Runtime configuration is expressed in YAML with consistent evaluation semantics:
- Conditional logic and data extraction use [Common Expression Language (CEL)](https://opensource.google/projects/cel) with
  programs compiled at configuration load, while Go `text/template` (with Sprig helpers) remains for rendering string bodies,
  headers, and query parameters. The runtime exposes helpers such as `lookup(map, key)` so rules can safely inspect optional
  headers, query parameters, or backend fields without triggering evaluation errors. The specialised evaluation order for each
  field is highlighted in the design docs.
- Endpoint objects declare authentication posture, forward request policy, rule ordering, response policy defaults, and caching hints.
- Rule objects own credential intake (`auth` directives), backend orchestration, conditional overrides, scoped variable exports,
  response shaping, and memoization.

The loader now materializes merged rule definitions during startup. When `server.rules` points at a file or folder the
resulting `Config` records every source in `RuleSources`, captures duplicate definitions in `SkippedDefinitions`, and hydrates
`Endpoints`/`Rules` for downstream agents. Endpoints that reference missing rules are also quarantined with a
`SkippedDefinitions` entry (`missing rule dependencies: <rule>`) so operators can correct the right document. Create the
referenced folder (or set `rulesFolder: ""` when relying solely on `rulesFile`) so hot reloads activate immediately.

See [`design/config-structure.md`](design/config-structure.md) for a full schema skeleton and inline guidance on default behavior.

## Technical Expectations
Implementation work should meet the non-functional guidance in [`design/technical-requirements.md`](design/technical-requirements.md):
- Target Go 1.25+, prefer the standard library, and keep module metadata minimal.
- Organize packages around the same building blocks described above to keep control flow obvious.
- Emit structured logs via `log/slog`, provide mode-aware verbosity, and deliver consistent observability signals across every
  decision point.
- Keep design documents authoritative—code changes that alter behavior must update the corresponding artifacts in `design/`.

## Development
- Run unit tests with `go test ./...` to validate runtime agents, cache integrations, and configuration loaders.
- Enforce static analysis and formatting with `golangci-lint run ./...`. The baseline `.golangci.yml` enables `errcheck`,
  `gofmt`, `goimports`, `govet`, `staticcheck`, `ineffassign`, `misspell`, `unparam`, and `prealloc` to cover error
  handling, style, and data-flow regressions before runtime testing. The CI workflow rebuilds `golangci-lint` with
  the Go 1.25 toolchain so local and automated runs share the same analyzers. When developing locally, install the Go 1.25
  toolchain (for example via `go toolchain install go1.25.0`) and rebuild `golangci-lint` with that toolchain (`go install
  github.com/golangci/golangci-lint/cmd/golangci-lint@latest`) so the binary can target the module's language version when
  running formatters and analyzers.
- Reuse the cached lint invocation to avoid permission issues in restricted environments:
  ```bash
  mkdir -p .gocache .gomodcache .golangci-lint
  GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache GOLANGCI_LINT_CACHE=$(pwd)/.golangci-lint golangci-lint run ./...
  ```
- Regenerate interface doubles with `mockery --config .mockery.yml` whenever new seams are introduced. Generated mocks emit to the package-specific `mocks/` folders (for example, `cmd/mocks/`, `internal/mocks/cache/`, `internal/mocks/runtime/`, `internal/mocks/server/`) and must be committed alongside the tests that consume them.

## Contributing
1. Review the design documents under `design/` before proposing changes; they establish terminology, control flow, and
   configuration semantics.
2. When implementing or adjusting behavior, update both code and design artifacts so intent, diagrams, and configuration examples
   stay aligned.
3. Surface new examples under `examples/` that demonstrate the documented flows (authentication, backend-driven authorization,
   caching) as functionality evolves.

Feedback on the design itself is welcome via issues or pull requests that reference the affected documents.

## Containerization
See [`docs/guides/deploy.md`](docs/guides/deploy.md) for building and running the container image, including support for `TZ`, `PUID`, and `PGID`.
