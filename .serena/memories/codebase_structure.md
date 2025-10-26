# PassCtrl Codebase Structure

## Top-Level Directories

### `cmd/`
Main entrypoint, server bootstrap, dependency injection seams
- Entry point: `cmd/main.go`

### `internal/`
Core implementation (private packages)

#### Configuration & Infrastructure
- `internal/config/` - Configuration loading, validation, hot-reload via koanf
- `internal/logging/` - Structured logging setup (slog)
- `internal/metrics/` - Prometheus instrumentation
- `internal/templates/` - Template sandbox, Sprig integration, env variable guards
- `internal/expr/` - CEL program compilation and evaluation helpers

#### Runtime Agents
- `internal/runtime/admission/` - Request authentication, proxy validation
- `internal/runtime/forwardpolicy/` - Header/query parameter curation
- `internal/runtime/rulechain/` - Rule orchestration, short-circuit logic
- `internal/runtime/pipeline/` - Agent pipeline coordination
- `internal/runtime/responsepolicy/` - Response rendering
- `internal/runtime/resultcaching/` - Cache policy enforcement
- `internal/runtime/cache/` - Cache backend abstractions (memory, Valkey/Redis)
- `internal/runtime/rule_execution_agent.go` - Individual rule execution

#### HTTP Layer
- `internal/server/` - HTTP routing, health endpoints, request pipeline

#### Testing Support
- `internal/mocks/` - Generated testify mocks (via mockery)

### `design/`
Architecture documents, UML diagrams, request flows
- **Authoritative** design documentation
- `design/system-agents.md` - Agent contracts
- `design/config-structure.md` - Configuration schema
- `design/request-flows.md` - Request walkthroughs
- `design/uml-diagrams.md` - Architecture diagrams
- `design/decision-model.md` - Evaluation semantics

### `examples/`
Working configuration samples
- `examples/basic/` - Basic configuration example

### `docs/`
Deployment guides, operational documentation

### `docker/`
Docker-related files

### `rules/`
Runtime rules directory (watched for hot-reload)

### `.beads/`
Beads issue tracking database (git-tracked)

## Configuration Files

### Build & Development
- `go.mod` - Go module definition (requires Go 1.25+)
- `go.sum` - Dependency checksums
- `.golangci.yml` - Linter configuration
- `.mockery.yml` - Mock generation configuration
- `Dockerfile` - Docker build definition
- `Dockerfile.alpine` - Alpine-based Docker build

### Project Documentation
- `README.md` - Project overview and getting started
- `CLAUDE.md` - Claude Code development guidance (THIS FILE)
- `AGENTS.md` - Detailed agentic coding guidance
- `DEPENDENCIES.md` - Library selection principles and dependency rationale
- `LICENSE` - Project license

### Git
- `.gitignore` - Git ignore patterns
- `.github/` - GitHub workflows and configurations

## Key Architectural Flows

### Rule Evaluation Order
1. Evaluate rule authentication directives (ordered `auth` blocks)
2. Forward first matched credential via optional `forwardAs` transformation
3. Render backend request using templates
4. Invoke backend API
5. Evaluate CEL conditions (`whenAll`, `failWhen`, `errorWhen`)
6. Export variables to scoped context (`global`, `rule`, `local`)
7. Return outcome (`Pass`, `Fail`, `Error`)

First non-pass result short-circuits the chain.

### Configuration Sources
- `server.rules.rulesFile` - Static file loaded once at startup
- `server.rules.rulesFolder` - Directory watched for hot-reload (default: `./rules`)

Configuration precedence: `env > file > default`

## Agent Boundaries

Each runtime agent has a clear contract and should not leak responsibilities:

1. **Server Configuration & Lifecycle** - Bootstrap, config, hot-reload
2. **Admission & Raw State** - Auth, proxy validation
3. **Forward Request Policy** - Header/query curation
4. **Rule Chain** - Orchestration
5. **Rule Execution** - Individual rule processing
6. **Response Policy** - Response rendering
7. **Result Caching** - Decision memoization

See `design/system-agents.md` for detailed contracts.
