# Technical Requirements

This document captures the non-functional expectations for Rest ForwardAuth v2 so the design
artifacts stay aligned with the implementation. These requirements complement the intent,
decision model, and request flow documents by describing the underlying platform choices and
engineering conventions the codebase must follow.

## Runtime and Language

1. **Target Go 1.25.** The runtime must compile and run with Go 1.25 or newer. New language
   features introduced up to Go 1.25 (such as range-over-function and iterator helpers) are
   available to simplify code paths when they improve clarity without sacrificing portability.
2. **Prefer the standard library.** Implementation should favor maintained packages that ship
   with Go. Third-party dependencies are allowed only when the standard library lacks the
   required capability or when an existing dependency remains actively maintained and tested.
3. **Module hygiene.** `go.mod` and `go.sum` stay minimal and tidy. Tooling updates should happen
   alongside runtime upgrades so v2 remains compatible with supported Go toolchains.

## Architectural Structure

1. **Clear segmentation.** Source files organize around the same building blocks called out in the
   design docs: endpoint admission, forward request policy, rule evaluation, response policy,
   and caching. Each package or subdirectory owns a single responsibility so
   looking at the project layout reveals how the runtime processes requests.
2. **Understandable flow.** Endpoint and rule orchestration share a common structure. Reading the
   endpoint executor should make it obvious how control passes into individual rules, and each
   rule should expose the same stages described in the decision model (authentication, backend
   call, conditions, responses, variables, caching). Diagrams in `design/uml-diagrams.md` should
   mirror the code structure, and code changes that diverge must update the diagrams.
3. **Function reuse.** Shared behaviors—such as header sanitisation, proxy trust checks, template
   rendering, and cache key derivation—live in dedicated helpers that can be imported across
   packages. Avoid copy-paste logic inside different rule types.

## Logging and Observability

1. **Structured logging with slog.** The runtime uses Go's `log/slog` package to emit JSON logs in
   production and human-readable text logs in development. Each log entry carries consistent
   fields such as endpoint, rule name, request identifiers, and decision outcomes.
2. **Mode-aware verbosity.** Development mode increases verbosity, surfacing detailed context about
   rule evaluation, variable extraction, and caching events. Production mode defaults to info
   level with optional debug toggles for targeted troubleshooting. When the log level drops to
   `debug`, the pipeline emits inbound request snapshots and post-decision summaries (admission,
   cache, backend, response) so operators can correlate Traefik requests with runtime decisions
   without enabling trace-level logging elsewhere.
3. **Traceability.** Every decision point (admission, forward policy evaluation, rule outcome,
   response policy selection, caching decision) should log structured breadcrumbs so
   operators can reconstruct what happened during a request.

## Documentation and Comments

1. **Intent-focused comments.** Function-level comments explain *why* a function exists and what
   design choice it supports, not just restating implementation details. Inline comments clarify
   surprising control flow or invariants that stem from the v2 design.
2. **Aligned documentation.** When the implementation introduces new behavior or reorganizes
   responsibilities, the corresponding design docs (intent, decision model, config structure,
   request flows, UML diagrams, and this technical requirements file) must be updated in the same
   change to keep the written guidance authoritative.
3. **Example parity.** Configuration examples under `examples/` should reflect the latest schema and
   demonstrate how the documented behaviors (authentication reuse and caching
   semantics) work in practice.

These requirements keep the runtime maintainable, observable, and aligned with the design so teams
can reason about endpoint behavior without reverse-engineering the code.
