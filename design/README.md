# Rest ForwardAuth v2 Design Notes

This folder captures the forward-looking design for a more approachable "v2" of Rest ForwardAuth. The goal is to reduce the
cognitive load of the current configuration-driven engine by clearly articulating intent, describing the critical request flows,
and documenting how decisions are derived from configuration and runtime data. Each document in this directory builds on the
existing `docs/` references while framing the changes necessary for a simplified successor. The emerging model centers on five
cooperating blocks per endpoint:

1. **Admission & Raw State** — define authentication posture, trusted proxies, and how the original request snapshot is captured.
2. **Forward Request Policy** — curate which headers and query parameters survive into rule evaluation and backend calls.
3. **Rule Chain** — evaluate ordered, named rules that read the curated request view, call backends with pagination support,
   and produce pass/fail/error outcomes.
4. **Response Policy** — render pass/fail/error responses using request context and rule outputs.
5. **Result Caching** — memoize rule decisions and endpoint outcomes for bounded durations without storing backend response
   bodies, honor separate pass/fail TTLs (with optional backend cache-header follow semantics), and skip caching when the
   decisive backend result was a 5xx.

## Contents
- [Intent](intent.md)
- [Request Flows](request-flows.md)
- [Decision Model](decision-model.md)
- [System Agents](system-agents.md)
- [Refactoring Roadmap](refactoring-roadmap.md)

Future additions (for example, migration plans or component-level specifications) should extend this directory while keeping the
high-level intent and flow summaries in sync.

Worked examples that mirror these documents now live under
`examples/suites/`. They cover directory-backed rule loading, Redis cache
configurations, and environment-aware templates to keep the design guidance
concrete.
