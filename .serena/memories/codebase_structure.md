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
- `internal/runtime/rule_execution_agent.go` - Individual rule orchestration (delegates to backend agent)
- `internal/runtime/backend_interaction_agent.go` - HTTP execution to backend APIs with pagination

#### HTTP Layer
- `internal/server/` - HTTP routing, health endpoints, request pipeline

#### Testing Support
- `internal/mocks/` - Generated testify mocks (via mockery)

### `design/`
Architecture documents, UML diagrams, request flows
- **Authoritative** design documentation
- `design/system-agents.md` - Agent contracts (8 agents)
- `design/backend-agent-separation.md` - Backend agent architecture and rationale
- `design/config-structure.md` - Configuration schema
- `design/request-flows.md` - Request walkthroughs
- `design/uml-diagrams.md` - Architecture diagrams
- `design/decision-model.md` - Evaluation semantics
- `design/per-rule-caching-v2.md` - Per-rule caching architecture

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
3. Render backend request using templates (Rule Execution Agent)
4. Delegate HTTP execution to Backend Interaction Agent
5. Evaluate CEL conditions (`whenAll`, `failWhen`, `errorWhen`)
6. Export variables to scoped context (`global`, `rule`, `local`)
7. Return outcome (`Pass`, `Fail`, `Error`)

First non-pass result short-circuits the chain.

### Configuration Sources
- `server.rules.rulesFile` - Static file loaded once at startup
- `server.rules.rulesFolder` - Directory watched for hot-reload (default: `./rules`)

Configuration precedence: `env > file > default`

## Agent Boundaries (8 Total)

Each runtime agent has a clear contract and should not leak responsibilities:

1. **Server Configuration & Lifecycle** - Bootstrap, config, hot-reload
2. **Admission & Raw State** - Auth, proxy validation
3. **Forward Request Policy** - Header/query curation
4. **Rule Chain** - Orchestration
5. **Rule Execution** - Individual rule orchestration, template rendering, condition evaluation, caching
6. **Backend Interaction** (NEW) - HTTP execution, pagination, response parsing (no policy decisions)
7. **Response Policy** - Response rendering
8. **Result Caching** - Decision memoization

See `design/system-agents.md` for detailed contracts.

## Backend Agent Separation

The Backend Interaction Agent was separated from Rule Execution Agent (PassCtrl-37) to:
- Achieve single responsibility per agent
- Improve testability (backend agent fully mockable)
- Enable future features (circuit breakers, retries) without touching rule logic
- Provide better observability (separate log labels)

**Key files**:
- `internal/runtime/backend_interaction_agent.go` - HTTP execution implementation
- `internal/runtime/backend_interaction_agent_test.go` - Comprehensive tests
- `design/backend-agent-separation.md` - Architecture documentation

**No configuration changes required** - separation is purely internal.
