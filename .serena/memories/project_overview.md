# PassCtrl Project Overview

## Purpose

PassCtrl is a forward-auth runtime implementing a composable rule chain architecture. It's a redesign of the Rest ForwardAuth runtime that processes authentication/authorization requests through nine specialized runtime agents. The system emphasizes observability, predictable request handling, and configuration-driven behavior.

## Tech Stack

### Language
- **Go 1.25+** - Primary implementation language

### Key Dependencies

#### Configuration & Templating
- `koanf v2` - Configuration loading with env > file > default precedence
- Go `text/template` + `Masterminds/sprig/v3` - Template rendering
- `fsnotify` - Configuration hot-reload

#### Expression Language
- `google/cel-go` - Common Expression Language for conditional logic

#### HTTP & Testing
- `gavv/httpexpect/v2` - Declarative HTTP testing
- `stretchr/testify` - Test assertions and mocking

#### Observability
- `prometheus/client_golang` - Metrics instrumentation
- Built-in `slog` - Structured logging

#### Caching
- `valkey-io/valkey-go` - Valkey/Redis client for distributed caching
- `alicebob/miniredis/v2` - In-memory Redis for testing

### Configuration Formats
Supports YAML, TOML, and JSON via koanf parsers.

## Core Architecture

PassCtrl models request processing as collaboration between nine specialized agents:

1. **Server Configuration & Lifecycle** - Bootstrap, config loading, hot-reload
2. **Admission & Raw State** - Request authentication, proxy validation
3. **Forward Request Policy** - Header/query parameter curation
4. **Endpoint Variables** - Evaluates endpoint-level variables once per request using CEL or Go templates
5. **Rule Chain** - Ordered rule execution with short-circuit semantics
6. **Rule Execution** - Individual rule orchestration, template rendering, condition evaluation (delegates backend calls)
7. **Backend Interaction** - HTTP execution to backend APIs with pagination support
8. **Response Policy** - HTTP response rendering
9. **Result Caching** - Decision memoization (never caches backend bodies or 5xx)

### Agent Separation (v2 Architecture)

The Backend Interaction Agent (Agent 7) was separated from the Rule Execution Agent to achieve:
- **Single Responsibility**: HTTP execution isolated from rule orchestration
- **Better Testability**: Backend agent fully mockable
- **Improved Observability**: Separate `agent: "backend_interaction"` log labels
- **Future Extensibility**: Circuit breakers, retries, streaming can be added without touching rule logic

See `design/backend-agent-separation.md` for detailed architecture and migration rationale.

### Endpoint Variables Agent

The Endpoint Variables Agent (Agent 4) evaluates endpoint-level variables once per request before the rule chain executes:
- Supports both CEL expressions and Go templates (auto-detected by presence of `{{`)
- Stores results in `state.Variables.Global` for access by all rules
- Continues evaluation on individual variable errors (defaults failed variables to empty string)
- Location: `internal/runtime/endpointvars/`

**Note**: This agent exists in code but is not yet formally documented in `design/system-agents.md`.

## Key Features

- CEL-based conditional logic (`whenAll`, `failWhen`, `errorWhen`)
- Template-based response rendering with Sprig helpers
- Configuration hot-reload with automatic cache invalidation
- Template sandboxing with path traversal protection
- Backend pagination support (link-header per RFC 5988)
- Separate pass/fail TTL caching
- Prometheus metrics on `/metrics`
- Structured logging with correlation IDs
- Endpoint-level variable evaluation with hybrid CEL/template support
