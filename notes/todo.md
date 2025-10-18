# Outstanding Work

## Runtime Agents
- [x] Backfill unit tests for the new `rulechain`, `responsepolicy`, and `resultcaching` packages to lock their contracts before further refactors. (See the expanded suites under `internal/runtime/*`.)
- [ ] Extract the rule execution helper into a dedicated package once backend orchestration stabilizes so cross-agent imports stay consistent. Evaluation recorded in `design/refactoring-roadmap.md` to revisit after the backend client refactor settles.

## Documentation & Examples
- Author deep-dive guides for rule-chain configuration and response policy tuning, including worked examples.
- Back new `/examples` configurations (including the suite bundles) with smoke
  tests or CI validation to ensure YAML remains loadable as the schema evolves.
- Maintain change logs or ADR-style notes as features land to keep future contributors oriented.
- Keep `AGENTS.md` and `DEPENDENCIES.md` aligned with implementation changes so the documented library guidance stays authoritative.
- Document the new cache reload invalidation hooks and package layout across design and MkDocs references when the broader caching docs are refreshed.
- Expand the CLI integration harness to assert `/health`/`/explain` flows, metrics availability, and hot-reload behavior once the opt-in gate (`PASSCTRL_INTEGRATION`) lands in CI.

## Governance & Dependency Strategy
- Re-evaluate adopting maintained libraries for routing (`github.com/go-chi/chi/v5`) and resilient HTTP (`github.com/hashicorp/go-retryablehttp`) after the routing facade stabilizes; defer `chi` until middleware or complex path handling is required. Metrics instrumentation now uses `github.com/prometheus/client_golang`.
