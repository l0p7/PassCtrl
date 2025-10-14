# Progress Summary

## Bootstrapping
- Created the initial Go 1.25 module and repository structure with packages for configuration, logging, runtime pipeline, and server lifecycle.
- Implemented the configuration loader using Koanf with env > file > default precedence and validation for listener settings and rules sources.
- Added a logging factory around `log/slog` to honor level/format preferences and correlation headers.
- Assembled the HTTP pipeline exposing `/auth`, `/health` (alias `/healthz`), and namespaced
  `/<endpoint>/auth`/`/<endpoint>/healthz` routes.
- Wired the lifecycle server to handle graceful shutdown and embedded it in a signal-aware CLI entrypoint.

## Runtime Agents
- Built out the full authentication pipeline covering server readiness, admission, forward policy curation, rule chaining/execution, response shaping, and result caching state.
- Hardened the admission path with trusted proxy enforcement, exhaustive `X-Forwarded-*` validation, and RFC7239 `Forwarded` header normalization so downstream agents receive canonical client metadata.
- Replaced the placeholder rule and rule-chain agents with declarative rule definitions, sequential execution, and history tracking to mirror the documented evaluation order.
- Wired the runtime pipeline to consume the sanitized endpoint and rule bundle from the configuration loader so operators can compose outcomes without code changes.
- Split the rule chain, response policy, and result caching agents into dedicated packages with shared helpers so the pipeline orchestrator only coordinates their contracts.
- Instrumented every runtime agent with structured logging, correlation IDs, and latency tracking while echoing the request identifier back in `/auth` responses.

## Testing
- Established baseline unit tests covering configuration defaults, loader precedence (files and environment), logging validation, and server lifecycle behavior.
- Added comprehensive runtime agent coverage validating pipeline state transitions and caching behavior.

## Configuration Updates
- Nested template environment controls inside the `server.templates` block, updated defaults, and ensured loader + tests respect the new hierarchy.
- Updated design documentation to mirror the new configuration structure, lifecycle responsibilities, and new runtime agent surfaces.
- Materialized `server.rules` definitions by loading `rulesFile`/`rulesFolder`, populating `RuleSources`/`SkippedDefinitions`, and wiring filesystem watchers so rule edits hydrate `cfg.Endpoints`/`cfg.Rules` without restarts.
- Enforced rule-chain dependencies by quarantining endpoints that reference missing rules and emitting descriptive `SkippedDefinitions` metadata for operator visibility.

## Templating
- Implemented a template sandbox with path jail enforcement and environment allowlist support; added targeted tests for traversal and symlink escapes.

## Refactoring
- Began decoupling the HTTP surface by introducing `server.NewPipelineHandler` so the lifecycle agent owns routing while the pipeline exposes `ServeAuth`/`ServeHealth`/`ServeExplain` entrypoints and helper hooks for endpoint hints and error rendering.
- Extracted shared pipeline state and the admission agent into dedicated packages, clarifying the runtime contracts for other agents and reducing the `runtime` package surface area.
- Continued the agent package split by moving the forward request policy into `internal/runtime/forwardpolicy` and backfilling unit tests for its curation rules.
- Introduced a `ReloadInvalidator` hook for decision caches so pipeline reloads purge stale entries across in-memory and Redis backends.

## Documentation & Examples
- Expanded MkDocs-driven references to cover trusted proxy policy expectations, forwarding metadata curation, and rule-chain execution so operators have accurate runtime guidance.
- Authored a refactoring roadmap capturing router decoupling priorities, deferred the go-chi adoption after evaluating current routing needs, and published new worked configuration examples for Basic auth, token introspection, and cached multi-endpoint flows.
- Added a new suite of configuration bundles (`examples/suites/*`) covering
  rules folders, Redis-backed caches, and environment-aware templates alongside
  doc updates explaining the CEL activation maps.

## Repository Hygiene
- Renamed the module to `github.com/l0p7/passctrl` and updated all internal imports.
- Relocated the entrypoint to `cmd/main.go`.
- Added MkDocs-oriented documentation (`docs/index.md`) describing setup, configuration, runtime usage, and the agent pipeline.
- Extended `AGENTS.md` to clarify expectations for intent-focused comments, testing, and keeping `/docs` + `/examples` current.
- Introduced a `.golangci.yml` ruleset and CI workflow enforcing linting alongside `go test` to keep quality gates automated.
