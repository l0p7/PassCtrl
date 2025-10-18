# Dependency Guide

This document captures the libraries that underpin PassCtrl, the rationale for choosing them, and the guardrails for adding new dependencies. It complements the agent-focused expectations in `AGENTS.md`.

## Selection Principles
- **Consistency first.** Favor the packages already present in this repository when similar functionality is required. If the capability exists in the current stack, reuse it rather than introducing a parallel helper or forking a bespoke utility.
- **Standard library baseline.** Reach for the Go standard library before considering third-party modules. Bring in an external dependency only when it demonstrably reduces complexity or unlocks behavior the standard library cannot reasonably provide.
- **Well-maintained and reputable.** New libraries should show recent releases, active maintenance, and a license compatible with PassCtrl. Prefer dependencies with clear documentation and adoption in the Go community.
- **Documented rationale.** Every addition should note why it was selected and how it will be used. Update this file when a dependency is introduced, upgraded, or removed.
- **Not a hard ban.** These principles guide consistency; they are not strict prohibitions. When another library is a better fit, explain the reasoning and ensure it integrates cleanly with the existing stack.

## Core Runtime Dependencies
| Concern | Library | Purpose | Notes |
| --- | --- | --- | --- |
| Configuration layering | `github.com/knadh/koanf/v2` | Loads defaults, file sources, and environment variables with precedence handling. | Upstream v2 provides typed helpers and continued maintenance; supports file watches for individual configs—pair with fsnotify for directory watching. |
| Filesystem notifications | `github.com/fsnotify/fsnotify` | Emits change events for rule folders and configuration files. | Use to trigger rule reloads and cache invalidation when koanf file watches are insufficient. |
| Koanf providers | `github.com/knadh/koanf/providers/confmap`, `github.com/knadh/koanf/providers/env`, `github.com/knadh/koanf/providers/file` | Hydrate configuration from in-memory defaults, environment variables, and files. | Provider modules remain available individually—add additional koanf providers (e.g., consul, s3) as needed using the same import pattern. |
| Koanf parsers (YAML/TOML/JSON) | `github.com/knadh/koanf/parsers/yaml`, `github.com/knadh/koanf/parsers/toml`, `github.com/knadh/koanf/parsers/json` | Bridge koanf to the concrete decoding libraries used by the loader and rules catalog. | Other koanf parsers (HCL, HJSON, etc.) can be introduced when requirements expand; our schema currently standardizes on YAML/TOML/JSON. |
| YAML/TOML decoding backends | `go.yaml.in/yaml/v3`, `github.com/pelletier/go-toml` (transitive) | Underlying structured configuration decoders consumed via koanf parser wrappers. | Keep parsing surface minimal; sanitize inputs before use. |
| Structured logging | Go standard library (`log/slog`) | Emits correlation-aware telemetry for every runtime agent via the shared instrumentation layer. | Prefer the standard library logger; only evaluate third-party logging stacks if future requirements exceed `slog`'s handlers. |
| Metrics instrumentation | `github.com/prometheus/client_golang` | Publishes Prometheus counters and histograms for pipeline outcomes and cache activity. | Served from the dedicated `/metrics` endpoint using a per-process registry to avoid collisions with global collectors. |
| Deep copy helpers | `github.com/mitchellh/copystructure`, `github.com/mitchellh/mapstructure`, `github.com/mitchellh/reflectwalk` (transitive) | Enable safe duplication and mapping of configuration structs. | Inherited via koanf; rely on upstream updates for bug fixes. |
| Expression evaluation | `github.com/google/cel-go` | Compiles and executes CEL programs for rule predicates and variable extraction. | Programs compile at configuration load; keep the function set constrained to deterministic helpers. |
| Decision cache client | `github.com/valkey-io/valkey-go` | Provides Redis/Valkey connectivity for the distributed decision cache backend. | Valkey-first driver with RESP3 support; TLS enabled via optional CA bundle and identical fallback semantics to the memory backend. |
| Cache testing server | `github.com/alicebob/miniredis/v2` | Lightweight in-memory Redis implementation for exercising cache integrations in tests. | Used only in unit tests; mirrors Redis protocol without external services. |

## Testing Tooling
| Concern | Library | Purpose | Notes |
| --- | --- | --- | --- |
| HTTP integration flows | `github.com/gavv/httpexpect/v2` | Drives end-to-end assertions against the runtime HTTP surface with expressive request builders and response checks. | Adopted for the CLI integration harness; continue using it anywhere HTTP behavior is observed so request/response invariants stay explicit. |
| Test scaffolding | `github.com/stretchr/testify` | Provides `require`, `assert`, `mock`, `suite` helpers for table-driven tests. | Restrict imports to test files; mix `require`/`assert` as the scenario demands, keep `httpexpect` for HTTP paths, and generate doubles with the matching `testify/mock` APIs. |
| Mock generation | `github.com/vektra/mockery/v3` (CLI) | Code-generates `testify/mock` implementations from interfaces. | Invoke `mockery --config .mockery.yml`; mocks emit to package-specific `mocks/` folders (e.g., `internal/mocks/`, `cmd/mocks/`) with expecter support and must be checked in. |

## Libraries Under Evaluation
- **HTTP routing and middleware:** `github.com/go-chi/chi/v5` — composable router if the standard library mux no longer satisfies observability or middleware needs. Current assessment (see [`design/refactoring-roadmap.md`](design/refactoring-roadmap.md)) defers adoption until routing expands beyond the static `/auth`/`/healthz`/`/explain` surface.
- **Resilient outbound HTTP:** `github.com/hashicorp/go-retryablehttp` — retry/backoff semantics for backend orchestration without maintaining custom retry loops.
- **Metrics instrumentation:** Adopted via `github.com/prometheus/client_golang`; future evaluations should focus on surfacing additional metrics dimensions rather than selecting alternative libraries.

Each candidate should be assessed for maintenance signals, API ergonomics, and consistency with existing conventions before adoption. Document decisions (adopt, reject, defer) in this file and cross-link to the corresponding design or ADR where the evaluation is captured.

## Operational Expectations
- Pin dependency versions in `go.mod` to ensure reproducible builds. Upgrade deliberately and run the full test + lint suite when bumping versions.
- Remove unused transitive dependencies during tidy runs (`go mod tidy`) to keep the dependency graph lean.
- Audit licenses when introducing new libraries and ensure compliance with project governance.
- Capture follow-up tasks in `notes/todo.md` whenever additional work (evaluations, migrations) is required after selecting a dependency.
