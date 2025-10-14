# Refactoring Roadmap and Router Evaluation

This note captures the current refactoring opportunities across the PassCtrl runtime and records the outcome of the go-chi router evaluation. It aligns the next structural changes with the agent responsibilities defined in `design/system-agents.md` and the dependency policy in `DEPENDENCIES.md`.

## Current Observations

- The HTTP listener delegates all request handling to `runtime.Pipeline.Handler`, which performs manual path parsing inside `routeRequest` to dispatch `/auth`, `/healthz`, and `/explain` routes (see `internal/runtime/runtime.go`). The flow is clear but concentrates routing, endpoint selection, and response shaping in a single type, making future expansion (e.g., middleware, metrics) harder to stage incrementally.
- Configuration reloads already happen through `Pipeline.Reload` and the rules watcher hook in `cmd/main.go`. Refactors must preserve the hot-reload behavior and the guarantees that cached entries are invalidated when rule definitions change.
- Logging, cache wiring, and template sandbox construction occur in `cmd/main.go`, leaving the pipeline free of bootstrap concerns. Refactors should keep this separation so runtime agents stay focused on request handling.

## Refactoring Priorities

1. **Routing surface decoupling.** Extract a small router facade from `Pipeline` that translates HTTP paths into endpoint requests. Keeping the facade in the `internal/server` package would let the lifecycle agent own HTTP-specific concerns (timeouts, middleware hooks) while the pipeline exposes agent-oriented handlers.
2. **Agent-specific packages.** Several responsibilities (admission, forward request policy, rule execution) still live inside `runtime` helpers. Following `design/system-agents.md`, migrate each agent into its own package with explicit contracts (`Name()`, `Execute`). This will make the pipeline orchestration thinner and improve testability. *(Completed for rule chain, response policy, and result caching agents; rule execution remains inline until backend orchestration is stabilized.)*
3. **Cache invalidation hooks.** Formalize cache invalidation signals when `Pipeline.Reload` swaps endpoint/rule bundles. Exposing a small interface for invalidation will simplify future cache backends without duplicating logic. *(Completed via `cache.ReloadInvalidator`, implemented by the memory and Redis caches.)*
4. **Documentation alignment.** Each refactor should update MkDocs and design artifacts in lock-step, using ADR-style notes to justify structural moves so the expectations in `AGENTS.md` remain authoritative.

## go-chi Evaluation

The router currently relies on `net/http` with a focused dispatch function. Adopting [`github.com/go-chi/chi/v5`](https://github.com/go-chi/chi) would introduce a well-supported router but comes with trade-offs:

- **Current scope sufficiency.** The existing routes are few and static. `chi` would not noticeably reduce code today because `routeRequest` already handles the required patterns without middleware stacks or nested routing trees.
- **Dependency policy alignment.** `DEPENDENCIES.md` directs us to stay on the standard library until capabilities are lacking. The manual router has no functional gaps right now, so adding `chi` would add build and maintenance surface without immediate benefit.
- **Future readiness.** If upcoming work introduces per-endpoint middleware, richer path hierarchies, or instrumentation needs, `chi` becomes attractive thanks to its context propagation and middleware ecosystem. For the moment we can postpone adoption and revisit once those features are scheduled.

**Decision:** defer `go-chi` integration. Continue with the standard library router while progressing the refactors above. Re-evaluate when we add programmable middleware or when routing logic grows beyond static dispatch.

## Progress

- The lifecycle server now owns the routing facade via `internal/server.NewPipelineHandler`, which delegates to the pipeline's exported `ServeAuth`, `ServeHealth`, and `ServeExplain` entrypoints while keeping endpoint validation logic inside the runtime.
- `Pipeline` exposes HTTP-agnostic helpers (`RequestWithEndpointHint`, `EndpointExists`, and `WriteError`) so future routing changes avoid re-implementing pipeline semantics.
- Pipeline state, agent contracts, and the admission agent now live in dedicated packages (`internal/runtime/pipeline` and `internal/runtime/admission`), giving the pipeline a slimmer orchestration role and clarifying reuse across packages.
- The forward request policy agent has moved to `internal/runtime/forwardpolicy`, curating headers/query logic behind an exported `Agent` with package-local tests to protect the behavior.
- Rule chain, response policy, and result caching agents now live in dedicated `internal/runtime/rulechain`, `internal/runtime/responsepolicy`, and `internal/runtime/resultcaching` packages with shared helpers so the pipeline only coordinates their contracts.
- Pipeline reloads invoke `cache.ReloadInvalidator` so both in-memory and Redis backends can purge stale decisions when configuration snapshots change.

### Rule execution helper extraction evaluation (April 2025)

- The helper in `internal/runtime/rule_execution_agent.go` still orchestrates backend pagination, CEL activation, and template rendering. It depends on `pipeline.State`, `rulechain.Definition`, and the backend pagination helpers in a tight loop, so moving it today would force new cross-package layers or circular imports.
- Backend orchestration is still under active refactor: the HTTP invocation path wires retry and pagination handling directly in the helper. Extracting the agent now would either duplicate the backend client or introduce an interim facade likely to churn.
- Decision: keep the helper inline until the backend client is stabilized (pagination abstractions and retry policy finalized). Once the backend module settles, extract `ruleexecution.Agent` mirroring the rule chain agent shape so downstream packages import a consistent surface.

## Next Steps

- Draft ADRs for the remaining agent package split before coding to confirm ownership boundaries with stakeholders.
- Backfill unit tests around the new agent packages to prevent regressions while rule execution is still being extracted. *(Addressed by expanding the rule chain, response policy, and result caching test suites.)*
- Track the rule execution helper split so backend orchestration remains reusable once that agent moves into its own package, using the evaluation above to revisit once the backend client solidifies.
- Keep monitoring `chi` releases and record any blocking issues (e.g., security fixes) that would influence future adoption timelines.
