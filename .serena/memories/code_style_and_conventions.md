# PassCtrl Code Style and Conventions

## Go Code Style

### Naming Conventions
- Follow standard Go naming conventions
- Exported names: PascalCase
- Unexported names: camelCase
- Acronyms: HTTP, CEL, URL (all caps when at start, otherwise Http, Cel, Url in middle)

### Formatting
Code must pass all formatters configured in `.golangci.yml`:
- `gofmt` - Standard Go formatting
- `gofumpt` - Stricter formatting
- `gci` - Import grouping
- `goimports` - Import management

### Linting
All PRs must pass `golangci-lint` checks with these enabled linters:
- `errcheck` - Unchecked errors
- `govet` - Suspicious constructs
- `ineffassign` - Ineffectual assignments
- `staticcheck` - Static analysis
- `misspell` - Spelling errors
- `unparam` - Unused function parameters
- `prealloc` - Slice preallocation
- `gosec` - Security issues
- `noctx` - HTTP requests without context
- `bodyclose` - Unclosed HTTP bodies
- `errorlint` - Error wrapping
- `errchkjson` - JSON error handling
- `nolintlint` - Nolint directive validation
- `testifylint` - Testify usage patterns

## Error Handling

### Error Wrapping
Always wrap errors with `%w` for unwrapping:
```go
if err != nil {
    return fmt.Errorf("failed to load config: %w", err)
}
```

### Structured Logging
Emit structured logs via `slog` with correlation IDs:
```go
logger.Info("request processed",
    "component", "rule-chain",
    "outcome", "pass",
    "correlation_id", correlationID)
```

### Configuration Errors
- Invalid rule configs: Disable the rule, log warning, continue running
- Invalid server config: Terminate process with non-zero exit

## Testing Conventions

### Assertion Libraries
- Use `testify/require` for fatal setup checks
- Use `testify/assert` for non-fatal validations

### Mocking
- Generate mocks via `mockery --config .mockery.yml`
- Outputs to package-specific `mocks/` directories

### HTTP Tests
- Drive HTTP tests through `httpexpect/v2` for declarative assertions

### Test Structure
- Table-driven tests with descriptive `name` fields
- Always run `go test ./...` before finalizing changes

## Agent Separation

### Package Boundaries
Preserve boundaries between runtime agents. Each agent has a clear contract documented in `design/system-agents.md`. Avoid leaking responsibilities across packages.

### Package Organization
```
cmd/                    # Main entrypoint, bootstrap, DI seams
internal/
  config/               # Configuration loading, validation
  logging/              # Structured logging setup
  metrics/              # Prometheus instrumentation
  templates/            # Template sandbox, Sprig integration
  expr/                 # CEL program compilation
  runtime/
    admission/          # Request authentication
    forwardpolicy/      # Header/query curation
    rulechain/          # Rule orchestration
    pipeline/           # Agent pipeline coordination
    responsepolicy/     # Response rendering
    resultcaching/      # Cache policy enforcement
    cache/              # Cache backend abstractions
  server/               # HTTP routing, health endpoints
  mocks/                # Generated testify mocks
```

## Context Handling
- Context deadlines honored in all outbound calls (HTTP, cache, filesystem)
- Never ignore context cancellation

## Observability Standards

Every agent emits structured logs with consistent fields:
- `component` - Package/agent name
- `agent` - Specific agent identifier
- `outcome` - Result of operation
- `status` - HTTP status or decision status
- `latency_ms` - Operation duration
- `correlation_id` - Request correlation ID from configured header
