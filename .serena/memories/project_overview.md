# PassCtrl Project Overview

## Purpose

PassCtrl is a forward-auth runtime implementing a composable rule chain architecture. It's a redesign of the Rest ForwardAuth runtime that processes authentication/authorization requests through eight specialized runtime agents. The system emphasizes observability, predictable request handling, and configuration-driven behavior.

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

PassCtrl models request processing as collaboration between eight specialized agents:

1. **Server Configuration & Lifecycle** - Bootstrap, config loading, hot-reload
2. **Admission & Raw State** - Request authentication, proxy validation
3. **Forward Request Policy** - Header/query parameter curation
4. **Rule Chain** - Ordered rule execution with short-circuit semantics
5. **Rule Execution** - Individual rule orchestration, template rendering, condition evaluation
6. **Backend Interaction** - HTTP execution to backend APIs with pagination support
7. **Response Policy** - HTTP response rendering
8. **Result Caching** - Decision memoization (never caches backend bodies or 5xx)

### Agent Separation (v2 Architecture)

The Backend Interaction Agent (Agent 6) was separated from the Rule Execution Agent to achieve:
- **Single Responsibility**: HTTP execution isolated from rule orchestration
- **Better Testability**: Backend agent fully mockable
- **Improved Observability**: Separate `agent: "backend_interaction"` log labels
- **Future Extensibility**: Circuit breakers, retries, streaming can be added without touching rule logic

See `design/backend-agent-separation.md` for detailed architecture and migration rationale.

## Key Features

- CEL-based conditional logic (`whenAll`, `failWhen`, `errorWhen`)
- Template-based response rendering with Sprig helpers
- Configuration hot-reload with automatic cache invalidation
- Template sandboxing with path traversal protection
- Backend pagination support (link-header per RFC 5988)
- Separate pass/fail TTL caching
- Prometheus metrics on `/metrics`
- Structured logging with correlation IDs
