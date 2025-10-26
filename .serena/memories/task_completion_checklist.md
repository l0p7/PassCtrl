# Task Completion Checklist

When completing a development task in PassCtrl, ensure the following steps are performed:

## 1. Code Quality

### Run Tests
```bash
go test ./...
```
All tests must pass.

### Run Linter
```bash
mkdir -p .gocache .gomodcache .golangci-lint
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache GOLANGCI_LINT_CACHE=$(pwd)/.golangci-lint golangci-lint run ./...
```
All linter checks must pass.

### Regenerate Mocks (if interfaces changed)
```bash
mockery --config .mockery.yml
```

## 2. Design Documentation Synchronization

**CRITICAL**: Code changes that alter behavior MUST update corresponding `design/` artifacts in the same changeset:

- `design/system-agents.md` - If agent contracts changed
- `design/config-structure.md` - If configuration schema changed
- `design/request-flows.md` - If request handling changed
- `design/uml-diagrams.md` - If architecture changed
- `design/decision-model.md` - If evaluation semantics changed

**Design documentation is authoritative**. Code and design must remain synchronized.

## 3. Configuration Hot-Reload Compliance

If changes affect endpoint or rule configuration:

1. Ensure cached decisions are invalidated for affected endpoints
2. Update `RuleSources` and `SkippedDefinitions` in the loader
3. Mark endpoints with missing rule dependencies as unhealthy (5xx)

## 4. Template Sandbox Compliance

If changes involve templates:

- Templates must resolve paths relative to `server.templates.templatesFolder`
- Reject path traversal attempts outside the folder
- Only access environment variables when `templatesAllowEnv: true` and variable is in `templatesAllowedEnv` allowlist

## 5. Observability

Ensure all new code paths emit:

- Structured logs via `slog` with standard fields
- Prometheus metrics where appropriate
- Proper correlation ID propagation

## 6. Dependency Management

If dependencies were added or updated:
```bash
go mod tidy
go mod verify
```

## 7. Documentation

Update relevant documentation:
- README.md if user-facing behavior changed
- CLAUDE.md if development workflow changed
- Comments in code for complex logic

## 8. Git Commit

Follow standard Git practices:
- Descriptive commit messages
- Atomic commits
- Reference issue IDs if applicable (Beads issues)
