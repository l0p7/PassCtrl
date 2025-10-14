# Intent

Rest ForwardAuth is being reimplemented to satisfy the same fundamental requirements with a model that is easier to explain, test, and operate. Rather than iterating on the existing engine, this effort treats the new runtime as a clean reimplementation that preserves the spirit of composable rule chains while making it easier to reason about what happens to an inbound request. The current engine spreads concerns across endpoint overrides, rule definitions, and implicit defaults. The successor aims to:

1. **Clarify responsibilities.** Endpoint configuration owns admission (authentication requirements, trusted proxy posture, and how raw request state is captured). Forwarding policy owns which parts of the request are exposed to rules and backends. Rules focus on policy decisions. Response policy owns what is sent back to callers.
2. **Streamline happy paths.** A minimally configured endpoint should make a single allow/deny decision with obvious defaults, and only opt in to advanced features when required.
3. **Expose intent explicitly.** Operators should be able to express the purpose of an endpoint (authentication, authorization, enrichment) without stitching together multiple low-level primitives.
4. **Preserve observability.** Every decision must be auditable. The v2 runtime should publish structured traces of rules evaluated, variables computed, and responses returned.
5. **Acknowledge breaking changes.** While the reimplementation will offer migration tooling, operators should expect behavioral differences from the current release and plan to validate each endpoint end-to-end.

These principles ground the remaining documents in this folder and guide the compromises we make while reshaping the configuration surface.
