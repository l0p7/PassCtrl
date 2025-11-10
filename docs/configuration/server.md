---
title: Server Configuration Reference
description: Configure server-wide settings including listen address, logging, caching, environment variables, and secrets.
stage: Stage 2 - Server Configuration
permalink: /configuration/server/
---

# Server Configuration Reference

The `server` block establishes deployment-wide behavior that applies to all endpoints and rules. These settings control the HTTP server, logging, caching backends, template sandboxing, and variable loading.

## Configuration Order

Server configuration should be completed **before** defining endpoints and rules, as it provides the foundation for:
1. **Runtime environment** - Listen address, port, logging
2. **Shared resources** - Cache backends, template directories
3. **Global variables** - Environment variables and secrets
4. **Hot-reload behavior** - Rules folder watching

---

## Table of Contents

- [Listen Configuration](#listen-configuration)
- [Logging Configuration](#logging-configuration)
- [Rules Loading](#rules-loading)
- [Template Sandbox](#template-sandbox)
- [Environment Variables](#environment-variables)
- [Docker/Kubernetes Secrets](#dockerkubernetes-secrets)
- [Backend HTTP Client](#backend-http-client)
- [Cache Configuration](#cache-configuration)
- [Complete Example](#complete-example)

---

## Listen Configuration

Controls where the HTTP server binds and accepts connections.

```yaml
server:
  listen:
    address: "0.0.0.0"  # Bind address
    port: 8080           # TCP port
```

### Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `address` | string | `"0.0.0.0"` | Network interface to bind. Use `127.0.0.1` for localhost-only, `0.0.0.0` for all interfaces. |
| `port` | integer | `8080` | TCP port for the HTTP listener. |

### Examples

**Localhost only (development):**
```yaml
server:
  listen:
    address: "127.0.0.1"
    port: 8080
```

**Production (all interfaces):**
```yaml
server:
  listen:
    address: "0.0.0.0"
    port: 8080
```

---

## Logging Configuration

Structured logging with configurable format and levels.

```yaml
server:
  logging:
    level: info                          # debug|info|warn|error
    format: json                         # json|text
    correlationHeader: "X-Request-ID"   # Header for request correlation
```

### Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `level` | string | `"info"` | Log level: `debug`, `info`, `warn`, `error`. Debug level shows CEL evaluations, template rendering, and cache operations. |
| `format` | string | `"json"` | Output format: `json` for structured logs, `text` for human-readable. |
| `correlationHeader` | string | `"X-Request-ID"` | Header name used for request correlation IDs. PassCtrl reads this header from requests and echoes it in responses. |

### Examples

**Development (debug logs, text format):**
```yaml
server:
  logging:
    level: debug
    format: text
    correlationHeader: "X-Request-ID"
```

**Production (JSON logs for log aggregation):**
```yaml
server:
  logging:
    level: info
    format: json
    correlationHeader: "X-Correlation-ID"
```

### Log Fields

Every log entry includes:
- `component` - Package or agent name
- `agent` - Specific agent identifier
- `correlation_id` - Request correlation ID
- `endpoint` - Endpoint being processed
- `status` - HTTP status or decision status
- `latency_ms` - Operation duration
- `outcome` - Result of operation

---

## Rules Loading

Configure how PassCtrl loads endpoint and rule definitions.

```yaml
server:
  rules:
    rulesFolder: "./rules"  # Directory watching (hot-reload)
    rulesFile: ""           # Single file (no hot-reload)
```

### Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `rulesFolder` | string | `"./rules"` | Directory to watch for YAML/TOML/JSON files. Changes trigger hot-reload and cache invalidation. |
| `rulesFile` | string | `""` | Single configuration file loaded once at startup. No hot-reload. |

⚠️ **Do not set both `rulesFolder` and `rulesFile`** - this is invalid configuration.

### Hot-Reload Behavior

When using `rulesFolder`:
1. PassCtrl watches for file changes (add, modify, delete)
2. Configuration reloads trigger cache invalidation for affected endpoints
3. Invalid configurations quarantine affected endpoints (5xx responses)
4. Metrics track reload successes and failures

### Examples

**Development (hot-reload):**
```yaml
server:
  rules:
    rulesFolder: "./rules"
    rulesFile: ""
```

**Production (immutable config in container):**
```yaml
server:
  rules:
    rulesFolder: ""
    rulesFile: "/config/production.yaml"
```

**Docker volume mount:**
```yaml
server:
  rules:
    rulesFolder: "/app/rules"
```

---

## Template Sandbox

Configure the root directory for Go template file lookups.

```yaml
server:
  templates:
    templatesFolder: "./templates"
```

### Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `templatesFolder` | string | `"./templates"` | Root directory for template lookups. All template paths must resolve within this directory (sandboxed). |

### Security

- **Path Traversal Protection**: Template paths cannot escape `templatesFolder`
- **Disabled Functions**: `env`, `expandenv`, `readFile`, `readDir`, `glob` are blocked
- **Controlled Access**: Use `server.variables.environment` and `server.variables.secrets` instead

### Examples

**Development:**
```yaml
server:
  templates:
    templatesFolder: "./templates"
```

**Docker:**
```yaml
server:
  templates:
    templatesFolder: "/app/templates"
```

**Using templates in rules:**
```yaml
rules:
  custom-response:
    backendApi:
      bodyFile: "request-templates/profile.json"  # Resolves to ./templates/request-templates/profile.json
```

---

## Environment Variables

Load environment variables at startup with fail-fast validation.

```yaml
server:
  variables:
    environment:
      TIER: null              # null-copy: read env var "TIER"
      API_BASE: "BASE_URL"    # read env var "BASE_URL", expose as "API_BASE"
      SUPPORT: null           # null-copy: read env var "SUPPORT"
```

### Null-Copy Semantics

- `key: null` → Read environment variable with exact name as `key`
- `key: "ENV_VAR"` → Read `ENV_VAR`, expose as `key`

### Fail-Fast Validation

All referenced environment variables **must exist** at startup or the server will refuse to start.

```bash
# This will fail startup if TIER is not set
export TIER=premium
./passctrl -config server.yaml
```

### Access Patterns

**In CEL expressions (no `{{ }}`):**
```yaml
conditions:
  pass:
    - 'backend.body.tier == variables.environment.TIER'
```

**In Go templates (`{{ }}`):**
```yaml
headers:
  templates:
    X-Api-Base: "{{ .variables.environment.API_BASE }}"
```

### Examples

**Multi-environment deployment:**
```yaml
server:
  variables:
    environment:
      # Tier-based access control
      REQUIRED_TIER: "APP_TIER"          # e.g., "premium", "enterprise"

      # API endpoints
      AUTH_API_URL: "AUTH_SERVICE_URL"   # e.g., "https://auth.prod.example.com"
      USER_API_URL: "USER_SERVICE_URL"

      # Feature flags
      ENABLE_WEBHOOKS: null               # "true" or "false"

      # Contact info
      SUPPORT_EMAIL: null                 # e.g., "support@example.com"
```

**Environment variable override via Docker:**
```bash
docker run -e APP_TIER=premium \
           -e AUTH_SERVICE_URL=https://auth.example.com \
           -e SUPPORT_EMAIL=support@example.com \
           passctrl:latest
```

**See also:** `examples/env-vars-cel-and-templates.yaml` for complete working example.

---

## Docker/Kubernetes Secrets

Load file-based secrets from `/run/secrets/` at startup.

```yaml
server:
  variables:
    secrets:
      db_password: null        # null-copy: read /run/secrets/db_password
      api_key: "api_token"     # read /run/secrets/api_token, expose as "api_key"
      webhook_secret: null
```

### Null-Copy Semantics

- `key: null` → Read `/run/secrets/key`
- `key: "filename"` → Read `/run/secrets/filename`, expose as `key`

### Automatic Newline Trimming

Docker automatically adds trailing newlines to secret files. PassCtrl trims `\n`, `\r`, and `\r\n` automatically.

### Fail-Fast Validation

All referenced secret files **must exist and be readable** at startup or the server will refuse to start.

### Access Patterns

**In CEL expressions:**
```yaml
variables:
  expected_key: 'variables.secrets.api_key'
```

**In Go templates (backend requests only!):**
```yaml
backendApi:
  headers:
    templates:
      X-Api-Key: "{{ .variables.secrets.api_key }}"
```

⚠️ **Security Warning**: Never expose secrets in responses to clients!

### Examples

**Docker Compose:**
```yaml
# docker-compose.yml
services:
  passctrl:
    image: passctrl:latest
    secrets:
      - db_password
      - api_key

secrets:
  db_password:
    file: ./secrets/db_password.txt
  api_key:
    file: ./secrets/api_key.txt
```

```yaml
# PassCtrl config
server:
  variables:
    secrets:
      db_password: null
      api_key: null
```

**Kubernetes:**
```yaml
# Secret object
apiVersion: v1
kind: Secret
metadata:
  name: passctrl-secrets
type: Opaque
stringData:
  db_password: "supersecret123"
  api_key: "sk-1234567890"

---
# Deployment
apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
      - name: passctrl
        volumeMounts:
        - name: secrets
          mountPath: /run/secrets
          readOnly: true
      volumes:
      - name: secrets
        secret:
          secretName: passctrl-secrets
```

**Using secrets securely:**
```yaml
rules:
  backend-auth:
    backendApi:
      url: "https://api.example.com/validate"
      headers:
        templates:
          # ✅ GOOD: Use secret in backend request
          X-Api-Key: "{{ .variables.secrets.api_key }}"
    responsePolicy:
      pass:
        headers:
          custom:
            # ❌ BAD: Never expose secret to client!
            # X-Secret: "{{ .variables.secrets.api_key }}"

            # ✅ GOOD: Safe to expose non-secret data
            X-Auth-Status: "authenticated"
```

**See also:** `examples/docker-secrets/` for complete working example with Docker Compose and Kubernetes manifests.

---

## Backend HTTP Client

Configure the HTTP client used for all backend API calls.

```yaml
server:
  backend:
    timeout: "10s"  # Maximum duration for backend HTTP requests
```

### Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `timeout` | duration | `"10s"` | Maximum duration for backend HTTP requests. Format: Go duration string (e.g., `10s`, `30s`, `1m`). |

### Examples

**Fast internal services:**
```yaml
server:
  backend:
    timeout: "5s"
```

**Slow external APIs:**
```yaml
server:
  backend:
    timeout: "30s"
```

**Very slow batch operations:**
```yaml
server:
  backend:
    timeout: "60s"
```

---

## Cache Configuration

Configure caching backend and behavior.

```yaml
server:
  cache:
    backend: memory              # memory|redis
    ttlSeconds: 30               # Default TTL for cached decisions
    namespace: "passctrl:v1"     # Cache key prefix
    keySalt: "random-salt-123"   # Optional salt for key hashing
    epoch: 0                     # Increment to invalidate all caches

    # Redis-specific (when backend: redis)
    redis:
      address: "localhost:6379"
      password: ""
      db: 0
      tls:
        enabled: false
        insecure: false
        caFile: ""
```

### Cache Backend Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `backend` | string | `"memory"` | Cache backend: `memory` (in-process) or `redis` (shared). |
| `ttlSeconds` | integer | `30` | Default TTL for cached endpoint decisions (seconds). Can be overridden by endpoint and rule settings. |
| `namespace` | string | `"passctrl:decision:v1"` | Cache key prefix for namespacing. |
| `keySalt` | string | `""` | Optional salt appended to cache keys. Prevents collisions between environments sharing a cache backend. |
| `epoch` | integer | `0` | Epoch number appended to cache keys. Increment to invalidate all caches globally. |

### Redis Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `redis.address` | string | `"localhost:6379"` | Redis server address. |
| `redis.password` | string | `""` | Redis authentication password. |
| `redis.db` | integer | `0` | Redis database number (0-15). |
| `redis.tls.enabled` | boolean | `false` | Enable TLS for Redis connection. |
| `redis.tls.insecure` | boolean | `false` | Skip TLS certificate verification (insecure). |
| `redis.tls.caFile` | string | `""` | Path to CA certificate file for TLS verification. |

### Examples

**In-memory cache (development):**
```yaml
server:
  cache:
    backend: memory
    ttlSeconds: 30
```

**Redis cache (production, single instance):**
```yaml
server:
  cache:
    backend: redis
    ttlSeconds: 60
    namespace: "passctrl:prod:v1"
    keySalt: "production-salt-xyz"
    redis:
      address: "redis.internal:6379"
      password: ""
      db: 0
```

**Redis with TLS (production cluster):**
```yaml
server:
  cache:
    backend: redis
    ttlSeconds: 60
    namespace: "passctrl:prod:v1"
    redis:
      address: "redis-cluster.internal:6380"
      password: "${REDIS_PASSWORD}"  # From environment
      db: 0
      tls:
        enabled: true
        insecure: false
        caFile: "/etc/ssl/certs/redis-ca.crt"
```

**Multi-environment isolation:**
```yaml
# Staging environment
server:
  cache:
    backend: redis
    namespace: "passctrl:staging:v1"
    keySalt: "staging-salt"
    redis:
      address: "redis.staging:6379"
      db: 1

# Production environment
server:
  cache:
    backend: redis
    namespace: "passctrl:prod:v1"
    keySalt: "production-salt"
    redis:
      address: "redis.prod:6379"
      db: 0
```

**Global cache invalidation:**
```yaml
# Increment epoch to invalidate all cached decisions
server:
  cache:
    backend: redis
    epoch: 2  # Was 1, now 2 - all old cache entries ignored
```

### Caching Behavior

PassCtrl implements **two-tier caching**:

1. **Per-rule caching** (Tier 1):
   - Caches individual rule decisions
   - Cache key: `baseKey|ruleName|backendHash|upstreamVarsHash`
   - Skips redundant backend calls within a chain

2. **Endpoint-level caching** (Tier 2):
   - Caches entire chain outcomes
   - Cache key: `baseKey`
   - Skips entire rule chain on cache hit

**What is cached:**
- Decision metadata (pass/fail outcome, reason, exported variables)
- Response descriptors (status, headers, body template references)

**What is NOT cached:**
- Backend response bodies (never persisted)
- 5xx responses or error outcomes
- Requests with `none: true` authentication

**TTL hierarchy** (highest precedence first):
1. Error outcomes → Always 0 (never cached)
2. Backend `Cache-Control` header (if `followCacheControl: true`)
3. Rule manual TTL (`cache.pass.ttl`, `cache.fail.ttl`)
4. Endpoint TTL ceiling (`cache.resultTTL`)
5. Server max TTL ceiling (`server.cache.ttlSeconds`)

---

## Complete Example

Comprehensive server configuration for production deployment:

```yaml
server:
  # Network binding
  listen:
    address: "0.0.0.0"
    port: 8080

  # Structured logging
  logging:
    level: info
    format: json
    correlationHeader: "X-Request-ID"

  # Rules hot-reload
  rules:
    rulesFolder: "/app/rules"
    rulesFile: ""

  # Template sandbox
  templates:
    templatesFolder: "/app/templates"

  # Environment variables
  variables:
    environment:
      # Service discovery
      AUTH_API_URL: "AUTH_SERVICE_URL"
      USER_API_URL: "USER_SERVICE_URL"

      # Feature flags
      ENABLE_RATE_LIMITING: null
      ENABLE_AUDIT_LOGS: null

      # Business logic
      REQUIRED_TIER: "APP_TIER"
      SUPPORT_EMAIL: null

    # Docker/Kubernetes secrets
    secrets:
      db_password: null
      api_key: null
      jwt_secret: null
      webhook_secret: null

  # Backend HTTP client
  backend:
    timeout: "15s"

  # Redis cache cluster
  cache:
    backend: redis
    ttlSeconds: 60
    namespace: "passctrl:prod:v1"
    keySalt: "production-xyz"
    epoch: 1
    redis:
      address: "redis-cluster.internal:6380"
      password: ""  # Loaded from REDIS_PASSWORD env var via koanf
      db: 0
      tls:
        enabled: true
        insecure: false
        caFile: "/etc/ssl/certs/redis-ca.crt"
```

---

## Environment Variable Override

PassCtrl supports environment variable overrides using the `PASSCTRL_` prefix:

```bash
# Override listen port
export PASSCTRL_SERVER__LISTEN__PORT=9090

# Override log level
export PASSCTRL_SERVER__LOGGING__LEVEL=debug

# Override Redis address
export PASSCTRL_SERVER__CACHE__REDIS__ADDRESS=redis.prod:6379

# Run server
./passctrl -config server.yaml
```

**Precedence**: `environment variables > config file > defaults`

---

## Next Steps

After configuring server-wide settings:

1. **[Endpoint Configuration](../endpoints/)** - Define endpoints with authentication, forward policies, and rule chains
2. **[Rule Configuration](../rules/)** - Author rules that call backends and evaluate conditions
3. **[Advanced Examples](../advanced/)** - See complete configurations for complex scenarios

**Example Configurations:**
- `examples/server.yaml` - Basic server configuration
- `examples/env-vars-cel-and-templates.yaml` - Environment variables
- `examples/docker-secrets/` - Docker/Kubernetes secrets
- `examples/valkey-cache/` - Redis/Valkey caching
