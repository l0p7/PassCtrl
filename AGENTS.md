# Agentic Coding Guidance

This project treats AI or scripted contributors as first-class teammates. Use this guide to ground your workflow, keep design
artifacts authoritative, and align code changes with the runtime agents defined in `design/system-agents.md`.

## Working Loop
- **Get oriented**: skim `README.md` and the relevant `design/` docs before implementing changes. Map the task to one or more of
  the runtime agents (Server Configuration, Admission, Forward Request Policy, Rule Chain, Rule Execution, Response Policy, Proxy
  Binding, Result Caching).
- **Capture intent**: restate the objective in your scratchpad or plan, including the affected agent(s) and any configuration or
  observability touchpoints.
- **Plan deliberately**: break work into verifiable steps. Prefer local reasoning and diff inspection over blind editing.
- **Validate and document**: run focused tests or linters when possible, summarize results, and update design docs when behavior
  shifts.
- **Run lint/format checks** when touching Go code. The repo relies on `golangci-lint` (see below) and will reject PRs with
  outstanding lint or formatting issues. Prefer to run it locally before finalizing a change set.

## Implementation Heuristics
- Preserve the separation of concerns outlined in `design/system-agents.md`; avoid leaking responsibilities across packages.
- Honor configuration precedence (`env > file > default`) and watch for invariants called out in `design/config-structure.md`.
- Keep `server.rules` sources consistent: create the configured folder (or set `rulesFolder: ""` when relying solely on `rulesFile`) so the loader can hydrate `RuleSources`/`SkippedDefinitions` and watch for edits.
- Expect endpoints referencing missing rules to be quarantined; inspect `SkippedDefinitions` (`missing rule dependencies: ...`) when diagnosing unhealthy configuration loads.
- Keep rule, response, and proxy decisions observable—emit structured logs via `log/slog` with correlation IDs when plumbed.
- Treat caching as metadata-only; never persist backend bodies beyond the active proxy request path.
- Comment intent, not mechanics—add succinct notes only when they clarify why code exists or what failure modes it guards
  against. Inline noise that merely restates the implementation should be avoided.
- Pair every behavioral change with tests or explain why coverage is deferred. Favor table-driven or agent-scoped tests that
  mirror the design contracts.

## Library Usage Policy
- Prefer the Go standard library when it already satisfies the runtime agent contract; introduce third-party dependencies only when they demonstrably simplify the implementation or close a capability gap (e.g., configuration layering, filesystem events).
- Reuse the packages already adopted in this repository before adding alternatives. If the existing library provides the required behavior, integrate with it instead of reimplementing helpers.
- When new capabilities are needed, select maintained and well-regarded libraries with clear upgrade paths (recent releases, active issue triage, permissive license). Pin explicit versions and document the rationale in `DEPENDENCIES.md`.
- These conventions are not a hard restriction on bringing additional libraries. They are a consistency guardrail—choose libraries that align with the established stack and keep similar concerns using a single dependency family when practical.

## Consistency Conventions
- Maintain uniform agent contracts (`Name()`, `Execute(ctx, request, state)`) and share typed constants for agent identifiers, outcomes, and cache keys across packages to prevent drift.
- Normalise request state the same way in every agent (lower-case map keys, never nil maps, UTC timestamps). If a new agent needs additional context, extend the shared state struct rather than bolting on ad hoc maps.
- Apply consistent logging structure—include `component`, `agent`, `outcome`, `status`, `latency_ms`, and `correlation_id` fields. Extend the pattern rather than inventing new key names.
- Honor context deadlines in all outbound calls (HTTP, cache, filesystem) and wrap errors with `%w` so callers can surface causes without string matching.
- Keep naming conventions steady: package names map to runtime agents, filenames use the agent or responsibility name, and tests mirror the file they exercise (`*_test.go`).

## Documentation Expectations
- When you introduce or alter behavior, update the corresponding `design/` document in the same change set.
- Cross-check diagrams (`design/uml-diagrams.md`) and flow write-ups (`design/request-flows.md`) for divergence.
- Surface new operational insights or caveats in `design/technical-requirements.md` if they influence deployment posture.
- Keep `/docs` and `/examples` aligned with the runtime. Any change that affects user workflows or agent flows should update the
  public-facing guides and runnable samples alongside design artifacts.
- Run `golangci-lint` with repository-local caches to avoid permission issues in restricted environments. Example:
  ```bash
  mkdir -p .gocache .gomodcache .golangci-lint
  GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache GOLANGCI_LINT_CACHE=$(pwd)/.golangci-lint golangci-lint run ./...
  ```
  This command executes the enabled formatters (`gofmt`, `gofumpt`, `goimports`, `gci`) and linters in one pass.

## Quick References
- Runtime agent contracts: `design/system-agents.md`
- Configuration schema: `design/config-structure.md`
- Decision model and evaluation order: `design/decision-model.md`
- Request walkthroughs: `design/request-flows.md`

Use this document as both a checklist and a reminder that code, tests, and documentation evolve together. Consistent updates keep
the agentic model trustworthy for future contributors.
