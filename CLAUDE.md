# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

PassCtrl is a forward-auth runtime implementing a composable rule chain architecture. The system processes authentication/authorization requests through nine specialized runtime agents that evaluate rules, call backends, cache decisions, and render responses. The design emphasizes observability, predictable request handling, and configuration-driven behavior.

## Common Commands

### Testing & Quality
```bash
# Run all unit tests
go test ./...

# Run linter with repository-local caches (avoids permission issues)
mkdir -p .gocache .gomodcache .golangci-lint
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache GOLANGCI_LINT_CACHE=$(pwd)/.golangci-lint golangci-lint run ./...

# Generate mocks (run after modifying interfaces)
mockery --config .mockery.yml
```

### Development
```bash
# Build the binary
go build -o passctrl ./cmd

# Run the server (requires configuration file)
./passctrl -config ./examples/basic/config.yaml

# View available flags
./passctrl -h
```

### Dependency Management
```bash
# Update dependencies
go mod tidy

# Verify module integrity
go mod verify
```

### Issue Tracking (Beads)

This project uses Beads (`bd`) for issue tracking instead of Markdown files. Beads provides a lightweight, git-based issue tracking system with dependency management and AI-supervised workflows.

```bash
# View quickstart guide
See resource: beads://quickstart

# List all issues
/beads:list

# List issues by status
/beads:list --status=open
/beads:list --status=in_progress

# Find ready-to-work tasks (no blockers)
/beads:ready

# Show issue details
/beads:show <issue-id>

# Create a new issue
/beads:create "Issue title" task 2

# Update issue status (claim work)
/beads:update <issue-id> --status=in_progress

# Close completed issue
/beads:close <issue-id> "Completed: description"

# Add dependencies between issues
/beads:dep <issue-id> <depends-on-id>

# View project statistics
/beads:stats

# Show blocked issues
/beads:blocked
```

**Important Notes:**
- Beads stores issues in `.beads/` directory (git-tracked)
- Always call `set_context` with workspace root before write operations
- Issues have types: `bug`, `feature`, `task`, `epic`, `chore`
- Dependencies create hard blockers that affect `ready` status
- See `/beads:workflow` for AI-supervised issue workflow guide

## High-Level Architecture

### Nine Runtime Agents

The system models request processing as collaboration between specialized agents (see `design/system-agents.md`):

1. **Server Configuration & Lifecycle** (`cmd/main.go`, `internal/config/`) - Bootstraps HTTP server, loads/watches configuration via `koanf`, enforces template sandboxing, and manages hot-reloads
2. **Admission & Raw State** (`internal/runtime/admission/`) - Authenticates requests, validates trusted proxies, captures immutable request snapshots
3. **Forward Request Policy** (`internal/runtime/forwardpolicy/`) - Sanitizes proxy metadata headers when configured; backend headers/query use null-copy semantics
4. **Endpoint Variables** (`internal/runtime/endpointvars/`) - Evaluates endpoint-level variables once per request using CEL or Go templates, storing results in global scope for all rules to access
5. **Rule Chain** (`internal/runtime/rulechain/`) - Orchestrates ordered rule execution with short-circuit semantics and scoped variable management
6. **Rule Execution** (`internal/runtime/rule_execution_agent.go`) - Executes individual rules including credential intake, condition evaluation via CEL, and response assembly; delegates backend HTTP calls to Backend Interaction Agent
7. **Backend Interaction** (`internal/runtime/backend_interaction_agent.go`) - Executes HTTP requests to backend APIs with pagination support (link-header per RFC 5988), capturing responses and errors without evaluating policy logic
8. **Response Policy** (`internal/runtime/responsepolicy/`) - Renders final HTTP responses using endpoint policy and rule outputs
9. **Result Caching** (`internal/runtime/cache/`, `internal/runtime/resultcaching/`) - Memoizes decisions (never backend bodies) with separate pass/fail TTLs; never caches 5xx/error outcomes

### Configuration Model

Configuration uses YAML/TOML/JSON loaded via `koanf` with precedence: `env > file > default`. Two key sources:
- `server.rules.rulesFile` - Static file loaded once at startup
- `server.rules.rulesFolder` - Directory watched for hot-reload (default: `./rules`)

Configuration spans three layers:
- **Server** - Listen address/port, logging (json/text), rules sources, template sandbox controls (`templatesFolder`, `templatesAllowEnv`)
- **Endpoints** - Authentication posture, trusted proxy IPs, forward request/response policies, rule chain ordering, cache hints
- **Rules** - Credential intake via **match groups** (compound admission with AND/OR logic, value-based matching with regex, multi-format credential emission), backend orchestration with CEL-based conditions (`whenAll`/`failWhen`/`errorWhen`), scoped variable exports, response shaping

See `design/config-structure.md` for complete schema and `examples/` for working configurations.

### Expression & Templating

- **CEL (Common Expression Language)** - Used for conditional logic (`whenAll`, `failWhen`, `errorWhen`) and variable extraction; programs compile at config load
- **Go `text/template` + Sprig** - Used for rendering headers, query params, and response bodies
- **Custom helpers** - `lookup(map, key)` safely probes optional headers/params/variables without evaluation errors

### Package Organization

```
cmd/                    # Main entrypoint, server bootstrap, dependency injection seams
internal/
  config/               # Configuration loading, validation, hot-reload via koanf
  logging/              # Structured logging setup (slog)
  metrics/              # Prometheus instrumentation
  templates/            # Template sandbox, Sprig integration, env variable guards
  expr/                 # CEL program compilation and evaluation helpers
  runtime/
    admission/          # Request authentication, proxy validation
    forwardpolicy/      # Header/query parameter curation
    endpointvars/       # Endpoint-level variable evaluation (CEL/templates)
    rulechain/          # Rule orchestration, short-circuit logic
    pipeline/           # Agent pipeline coordination
    responsepolicy/     # Response rendering
    resultcaching/      # Cache policy enforcement
    cache/              # Cache backend abstractions (memory, Valkey/Redis)
  server/               # HTTP routing, health endpoints, request pipeline
  mocks/                # Generated testify mocks (via mockery)
design/                 # Architecture documents, UML diagrams, request flows
examples/               # Working configuration samples
docs/                   # Deployment guides, operational documentation
```

## Critical Development Patterns

### Agent Separation
Preserve boundaries between runtime agents. Each agent has a clear contract (inputs/outputs) documented in `design/system-agents.md`. Avoid leaking responsibilities across packages.

### Configuration Hot-Reload
Any config change affecting an endpoint or rule must:
1. Invalidate cached decisions for that endpoint
2. Update `RuleSources` and `SkippedDefinitions` in the loader
3. Mark endpoints with missing rule dependencies as unhealthy (5xx)

Endpoints referencing missing rules get quarantined with `SkippedDefinitions` entry: `missing rule dependencies: <rule>`.

### Rule Evaluation Order
Rules in an endpoint's chain execute sequentially:
1. **Evaluate rule authentication match groups** (ordered `auth` blocks):
   - Extract credentials from admission state (bearer, basic, headers, query)
   - Evaluate match groups sequentially (OR between groups, AND within groups)
   - For each group, check if ALL matchers succeed (including value constraints via regex/literals)
   - Build template context from all matched credentials in winning group
   - First complete match wins
2. **Render and apply credential forwards**:
   - When `forwardAs` is present: render each output using templates
   - When `forwardAs` is omitted: pass-through mode (forward matched credentials unchanged)
   - Strip all credential sources mentioned in auth block before applying forwards
3. Render backend request using templates (URL, headers, query, body)
4. Invoke backend API (with pagination if configured)
5. Evaluate CEL conditions (`whenAll`, `failWhen`, `errorWhen`)
6. Export variables to scoped context (`global`, `rule`, `local`)
7. Return outcome (`Pass`, `Fail`, `Error`)

First non-pass result short-circuits the chain.

**Auth Model Key Features**:
- **Match Groups**: Compound admission (require multiple credentials simultaneously)
- **Value Matching**: Filter credentials by literal strings or regex patterns (`/pattern/`)
- **Multi-Format Emission**: Emit same credential as bearer + header + query
- **Template Context**: `.auth.input.bearer.token`, `.auth.input.basic.user/.password`, `.auth.input.header['x-name']`, `.auth.input.query['param']`
- **Explicit Stripping**: All credential sources stripped before applying forwards (fail-closed security)

### Caching Invariants
- Store only decision metadata (outcome, variables, response descriptors)
- Never persist backend response bodies beyond active request
- Drop entries on 5xx or error outcomes
- Honor separate `passTTL`/`failTTL` from rule config
- Respect `followCacheControl` when backends send cache headers

### Testing Conventions
- Use `testify/require` for fatal setup checks, `testify/assert` for non-fatal validations
- Generate mocks via `mockery --config .mockery.yml` (output to `*/mocks/` directories)
- Drive HTTP tests through `httpexpect/v2` for declarative request/response assertions
- Table-driven tests with descriptive `name` fields
- Always run `go test ./...` and linter before finalizing changes

### Error Handling
- Wrap errors with `%w` for unwrapping
- Emit structured logs via `slog` with correlation IDs
- Invalid rule configs disable the rule and log warnings; server continues running
- Invalid server config terminates process with non-zero exit
- Context deadlines honored in all outbound calls (HTTP, cache, filesystem)

## Key Configuration Files

- `.golangci.yml` - Linter config (errcheck, govet, staticcheck, gosec, testifylint, gofmt/gofumpt/gci/goimports formatters)
- `.mockery.yml` - Mock generation config (outputs to package-specific `mocks/` directories)
- `go.mod` - Requires Go 1.25+, pins koanf v2, CEL, Sprig, Prometheus client, Valkey client
- `design/config-structure.md` - Authoritative YAML schema reference
- `AGENTS.md` - Detailed agentic coding guidance and consistency conventions
- `DEPENDENCIES.md` - Library selection principles and dependency rationale

## Important Notes

### Design Documentation is Authoritative
Code changes that alter behavior MUST update corresponding `design/` artifacts:
- `design/system-agents.md` - Agent contracts
- `design/config-structure.md` - Configuration schema
- `design/request-flows.md` - Request walkthroughs
- `design/uml-diagrams.md` - Architecture diagrams
- `design/decision-model.md` - Evaluation semantics

Keep code and design synchronized in the same changeset.

### Lint Requirements
All PRs must pass `golangci-lint` checks. The configuration enables:
- Linters: errcheck, govet, ineffassign, staticcheck, misspell, unparam, prealloc, gosec, noctx, bodyclose, errorlint, errchkjson, nolintlint, testifylint
- Formatters: gofmt, gofumpt, gci, goimports

Run locally with cached directories to avoid permission issues (see commands above).

### Template Sandbox
Templates must:
- Resolve paths relative to `server.templates.templatesFolder`
- Reject path traversal attempts outside the folder
- Only access environment variables when `templatesAllowEnv: true` and variable is in `templatesAllowedEnv` allowlist

### Observability
Every agent emits structured logs with consistent fields:
- `component` - Package/agent name
- `agent` - Specific agent identifier
- `outcome` - Result of operation
- `status` - HTTP status or decision status
- `latency_ms` - Operation duration
- `correlation_id` - Request correlation ID from configured header

Metrics published to `/metrics` using per-process Prometheus registry.

## Reference Flows

See `design/request-flows.md` for step-by-step walkthroughs of:
1. **Authentication Gateway** - Credential validation with minimal backend interaction
2. **Authorization With Backend Signal** - Policy decisions driven by external services
3. **Health & Explain** - Operational endpoints surfacing config health and rule graphs

Each flow demonstrates agent collaboration, caching participation, and observability touchpoints.
