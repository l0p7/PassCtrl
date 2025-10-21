---
title: Development Workflow
description: Contributor checklist for building, testing, and linting the PassCtrl runtime.
permalink: /development/contributor/
---

# Development Workflow

These notes target contributors extending the PassCtrl runtime. Operators can focus on the staged guides, while engineers refer here for local tooling expectations and quality gates.

## Local Toolchain

- Go 1.25 or newer.
- [`golangci-lint`](https://github.com/golangci/golangci-lint) built with the Go 1.25 toolchain.
- [`mockery`](https://github.com/vektra/mockery) v3 for generating `testify/mock` doubles.

Install dependencies:

```bash
go toolchain install go1.25.0
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
go install github.com/vektra/mockery/v3@latest
```

## Quality Gates

- Run `go test ./...` before pushing changes.
- Run `golangci-lint run ./...` using repository-local caches:

```bash
mkdir -p .gocache .gomodcache .golangci-lint
GOCACHE=$(pwd)/.gocache \
  GOMODCACHE=$(pwd)/.gomodcache \
  GOLANGCI_LINT_CACHE=$(pwd)/.golangci-lint \
  golangci-lint run ./...
```

- Regenerate mocks with `mockery --config .mockery.yml` when interfaces change. Generated files remain under the corresponding `mocks/` packages (for example, `internal/mocks/runtime/`).
- Use table-driven tests with `testify/require` and `testify/assert`. HTTP flows should continue to leverage `httpexpect`.

## Integration Test Harness

The CLI integration smoke test boots the runtime via `go run` and exercises readiness:

```bash
PASSCTRL_INTEGRATION=1 go test ./cmd -run TestIntegrationServerStartup -count=1
```

The harness allocates an ephemeral port, streams server logs on failure, and skips by default unless `PASSCTRL_INTEGRATION` is set.

## Documentation Source of Truth

- Update design artifacts under `design/` whenever behavior shifts.
- Keep `/docs` focused on operator-facing workflows; contributor notes live in this appendix so user documentation stays concise.
