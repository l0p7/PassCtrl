# Testing Migration Plan

This plan tracks the remaining work to standardise PassCtrl's test suites on
`testify` (require/assert/mock/suite), mockery-generated doubles, `httpexpect`
for HTTP flows, and table-driven patterns. It complements the Beads issues
PassCtrl-5 through PassCtrl-13.

## Current Posture (Oct 2025)

- ✅ `cmd/integration_test.go` uses `httpexpect`, `testify/require`, and a
  mockery-generated `httpDoer`.
- ✅ `internal/runtime/resultcaching/agent_test.go` and
  `internal/runtime/runtime_additional_test.go` use mockery + testify.
- ✅ Remaining runtime, server, config, metrics, and template suites converted
  to table-driven `testify` tests with mockery doubles where interfaces exist.
- ✅ HTTP-focused scenarios rely on `httpexpect` for declarative assertions.
- ✅ Admission, forward request policy, and response policy agents now share
  table-driven testify suites that cover proxy metadata sanitisation and
  outcome mapping permutations.
- ✅ Config loader/CLI, templates, metrics, and CEL helper tests are table-driven
  to exercise configuration precedence, sandbox boundaries, and metrics
  instrumentation paths.
- ✅ Rule execution agent scenarios use mockery-generated HTTP clients to avoid
  external listeners while validating backend pagination, templating, and
  metadata forwarding.

### Coverage Snapshot

- `go test ./... -coverprofile=coverage.out` (Oct 2025) reports **71.7%** total
  statement coverage. Runtime subpackages sit between 67–93%; CLI package
  coverage remains low (15.9%) because it primarily exercises integration
  flows.

## Migration Phases

1. **Audit Remaining Tests (PassCtrl-9)**
   - Catalogue each `*_test.go` file, capturing current assertion style,
     reliance on stubs, and required mocks.
   - Output: markdown table appended here summarising findings and
     recommended actions.

2. **Runtime Packages (PassCtrl-10)**
   - Convert admission, forward policy, pipeline, cache, rulechain suites to
     table-driven tests with `require/assert`.
   - Promote frequently reused stubs to mockery targets (e.g., pipeline
     agents, metrics recorder).

3. **HTTP-Facing Tests (PassCtrl-11)**
   - Replace bespoke HTTP assertions with `httpexpect` in server/router tests.
   - Ensure scenarios are table-driven with descriptive case names.

4. **Broader Mockery Coverage (PassCtrl-12)**
   - Extend `.mockery.yml` with additional interfaces:
     `metrics.Recorder`, template sandbox interfaces, cache reloaders, etc.
   - Regenerate mocks and update tests to consume them.

5. **Docs & Lint Closure (PassCtrl-13)**
   - Finalise documentation in `AGENTS.md`, `DEPENDENCIES.md`,
     `design/technical-requirements.md`.
   - Confirm `golangci-lint` (with `testifylint`) passes after conversions.

## Package Checklist

| Package/File | Primary Concerns | Migration Notes |
| --- | --- | --- |
| `cmd/main_test.go` | ✅ Uses testify | - |
| `cmd/integration_test.go` | ✅ Uses httpexpect + testify | - |
| `internal/config/loader_test.go` | ✅ Testify | - |
| `internal/config/watch_test.go`, `rules_loader_test.go`, `types_test.go` | ✅ Testify | - |
| `internal/metrics/metrics_test.go` | ✅ Testify | - |
| `internal/server/router_test.go` | ✅ Testify | - |
| `internal/server/server_test.go` | ✅ Testify | - |
| `internal/templates/renderer_test.go`, `sandbox_test.go` | ✅ Testify | - |
| `internal/runtime/admission/agent_test.go` | ✅ Testify | - |
| `internal/runtime/forwardpolicy/agent_test.go` | ✅ Testify | - |
| `internal/runtime/pipeline/state_test.go` | ✅ Testify | - |
| `internal/runtime/rulechain/agent_test.go`, `rule_execution_agent_test.go` | ✅ Testify | - |
| `internal/runtime/rulechain/backend_test.go` | ✅ Testify | - |
| `internal/runtime/runtime_test.go`, `runtime_additional_test.go` | ✅ Testify | - |
| `internal/runtime/agents_test.go` | ✅ Testify + coverage | Migrated pagination/backend templating flows to `require`; added renderer body/file coverage. |
| `internal/runtime/cache/cache_test.go` | ✅ Testify | - |
| `internal/runtime/responsepolicy/agent_test.go` | ✅ Testify | - |
| `internal/runtime/resultcaching/agent_test.go` | ✅ Testify + mockery | - |
| `internal/templates/renderer_test.go`, `sandbox_test.go` | ✅ Testify | - |
| `internal/expr/env_test.go` | ✅ Testify | - |

## File Status (Updated Oct 2025)

| File | Status |
| --- | --- |
| cmd/integration_test.go | ✅ |
| cmd/main_test.go | ✅ |
| internal/config/loader_test.go | ✅ |
| internal/config/rules_loader_test.go | ✅ |
| internal/config/types_test.go | ✅ |
| internal/config/watch_test.go | ✅ |
| internal/expr/env_test.go | ✅ |
| internal/logging/logger_test.go | ✅ |
| internal/metrics/metrics_test.go | ✅ |
| internal/runtime/admission/agent_test.go | ✅ |
| internal/runtime/agents_test.go | ✅ |
| internal/runtime/cache/cache_test.go | ✅ |
| internal/runtime/forwardpolicy/agent_test.go | ✅ |
| internal/runtime/pipeline/state_test.go | ✅ |
| internal/runtime/responsepolicy/agent_test.go | ✅ |
| internal/runtime/resultcaching/agent_test.go | ✅ |
| internal/runtime/rule_execution_agent_test.go | ✅ |
| internal/runtime/rulechain/agent_test.go | ✅ |
| internal/runtime/rulechain/backend_test.go | ✅ |
| internal/runtime/runtime_additional_test.go | ✅ |
| internal/runtime/runtime_test.go | ✅ |
| internal/server/router_test.go | ✅ |
| internal/server/server_test.go | ✅ |
| internal/templates/renderer_test.go | ✅ |
| internal/templates/sandbox_test.go | ✅ |

## Mockery Candidate Interfaces

- `internal/runtime/pipeline.Pipeline` (or exported test seam) for server/router tests.
- `internal/runtime/metrics.Recorder` for metrics agent verification.
- `internal/runtime/rulechain` backend executor interfaces (once extracted).
- `internal/server` response writer helpers if deeper mocks are needed.

## Tooling Expectations

- Generate/update mocks with `mockery --config .mockery.yml`.
- Re-run `go test ./...` and `golangci-lint run ./...` (with `testifylint`)
  after each batch.
- Update this document after each phase with progress notes and links to the
  relevant Bead entries or PRs.

## Next Steps (PassCtrl-9 Kickoff)

- Populate the “Package Checklist” with audit details (owner, blockers).
- Identify additional interfaces needing mocks and append to `.mockery.yml`
  under a dedicated “Candidates” section.
- Prepare a prioritised conversion sequence (likely runtime → server →
  config/templates → metrics).
