---
title: Advanced Configuration Examples
description: Complete configurations for complex scenarios including multi-environment setups, secrets management, and caching strategies.
stage: Stage 5 - Advanced Examples
permalink: /configuration/advanced/
---

# Advanced Configuration Examples

This guide provides complete, working configurations for complex real-world scenarios. Each example is self-contained and ready to deploy.

## Table of Contents

- [Multi-Environment Deployment](#multi-environment-deployment)
- [Docker Secrets Integration](#docker-secrets-integration)
- [Multi-Credential Composition](#multi-credential-composition)
- [Backend Token Introspection](#backend-token-introspection)
- [Webhook Signature Verification](#webhook-signature-verification)
- [Rate Limiting with Redis](#rate-limiting-with-redis)
- [Conditional Routing](#conditional-routing)
- [Variable Propagation Patterns](#variable-propagation-patterns)

---

## Multi-Environment Deployment

Deploy the same configuration across development, staging, and production using environment variables.

### Configuration

**File**: `config/multi-env.yaml`

```yaml
server:
  listen:
    address: "0.0.0.0"
    port: 8080

  logging:
    level: "${LOG_LEVEL}"  # Override per environment
    format: json
    correlationHeader: "X-Request-ID"

  rules:
    rulesFolder: "/app/rules"

  variables:
    environment:
      # Service discovery (environment-specific URLs)
      AUTH_API_URL: "AUTH_SERVICE_URL"
      USER_API_URL: "USER_SERVICE_URL"
      BILLING_API_URL: "BILLING_SERVICE_URL"

      # Feature flags
      ENABLE_WEBHOOKS: null
      ENABLE_RATE_LIMITING: null

      # Business logic
      REQUIRED_TIER: "APP_TIER"

    secrets:
      # Secrets from Docker/Kubernetes
      api_key: null
      jwt_secret: null

  backend:
    timeout: "15s"

  cache:
    backend: "${CACHE_BACKEND}"  # memory (dev) or redis (prod)
    ttlSeconds: 60
    namespace: "passctrl:${ENVIRONMENT}:v1"
    keySalt: "${CACHE_SALT}"
    epoch: 1
    redis:
      address: "${REDIS_ADDRESS}"
      password: "${REDIS_PASSWORD}"
      db: 0

endpoints:
  api:
    authentication:
      required: true
      allow:
        authorization: ["bearer"]
    rules:
      - name: validate-tier
      - name: call-user-api
    responsePolicy:
      pass:
        status: 200
      fail:
        status: 403

rules:
  validate-tier:
    backendApi:
      url: "{{ .variables.environment.AUTH_API_URL }}/validate"
      method: POST
      headers:
        templates:
          X-Api-Key: "{{ .variables.secrets.api_key }}"
      acceptedStatuses: [200]
    conditions:
      pass:
        - 'backend.body.tier == variables.environment.REQUIRED_TIER'
      fail:
        - 'backend.body.tier != variables.environment.REQUIRED_TIER'

  call-user-api:
    backendApi:
      url: "{{ .variables.environment.USER_API_URL }}/profile"
      method: GET
      acceptedStatuses: [200]
    conditions:
      pass:
        - 'backend.status == 200'
```

### Environment Files

**Development** (`.env.dev`):
```bash
LOG_LEVEL=debug
ENVIRONMENT=dev
CACHE_BACKEND=memory
CACHE_SALT=dev-salt

AUTH_SERVICE_URL=http://auth.dev.local
USER_SERVICE_URL=http://user.dev.local
BILLING_SERVICE_URL=http://billing.dev.local

ENABLE_WEBHOOKS=false
ENABLE_RATE_LIMITING=false
APP_TIER=basic
```

**Staging** (`.env.staging`):
```bash
LOG_LEVEL=info
ENVIRONMENT=staging
CACHE_BACKEND=redis
CACHE_SALT=staging-salt
REDIS_ADDRESS=redis.staging:6379
REDIS_PASSWORD=

AUTH_SERVICE_URL=https://auth.staging.example.com
USER_SERVICE_URL=https://user.staging.example.com
BILLING_SERVICE_URL=https://billing.staging.example.com

ENABLE_WEBHOOKS=true
ENABLE_RATE_LIMITING=true
APP_TIER=premium
```

**Production** (`.env.prod`):
```bash
LOG_LEVEL=warn
ENVIRONMENT=prod
CACHE_BACKEND=redis
CACHE_SALT=prod-xyz-secret
REDIS_ADDRESS=redis-cluster.prod:6380
REDIS_PASSWORD=secret-redis-password

AUTH_SERVICE_URL=https://auth.example.com
USER_SERVICE_URL=https://user.example.com
BILLING_SERVICE_URL=https://billing.example.com

ENABLE_WEBHOOKS=true
ENABLE_RATE_LIMITING=true
APP_TIER=enterprise
```

### Deployment

```bash
# Development
docker run --env-file .env.dev -v ./secrets:/run/secrets passctrl:latest

# Staging
docker run --env-file .env.staging -v ./secrets:/run/secrets passctrl:latest

# Production
docker run --env-file .env.prod -v ./secrets:/run/secrets passctrl:latest
```

---

## Docker Secrets Integration

Complete Docker Compose setup with secrets for backend authentication.

### Docker Compose

**File**: `docker-compose.yml`

```yaml
services:
  passctrl:
    image: passctrl:latest
    ports:
      - "8080:8080"
    environment:
      - PASSCTRL_SERVER__LOGGING__LEVEL=info
      - APP_TIER=premium
    secrets:
      - db_password
      - api_key
      - jwt_secret
    volumes:
      - ./config.yaml:/config/config.yaml:ro
    command: ["-config", "/config/config.yaml"]

secrets:
  db_password:
    file: ./secrets/db_password.txt
  api_key:
    file: ./secrets/api_key.txt
  jwt_secret:
    file: ./secrets/jwt_secret.txt
```

### Configuration

**File**: `config.yaml`

```yaml
server:
  variables:
    environment:
      TIER: "APP_TIER"
    secrets:
      db_password: null
      api_key: null
      jwt_secret: null

endpoints:
  secure-api:
    authentication:
      required: true
      allow:
        authorization: ["bearer"]
    rules:
      - name: verify-with-secret
    responsePolicy:
      pass:
        status: 200

rules:
  verify-with-secret:
    backendApi:
      url: "https://api.example.com/verify"
      method: POST
      headers:
        templates:
          X-Api-Key: "{{ .variables.secrets.api_key }}"
          Authorization: "Bearer {{ .variables.secrets.jwt_secret }}"
      body: |
        {
          "client_token": "{{ index .auth.input.bearer 0 }}",
          "tier": "{{ .variables.environment.TIER }}"
        }
      acceptedStatuses: [200]
    conditions:
      pass:
        - 'backend.status == 200'
```

**See**: `examples/docker-secrets/` for complete working example.

---

## Multi-Credential Composition

Require multiple credentials simultaneously and compose them for backend calls.

```yaml
endpoints:
  secure-gateway:
    authentication:
      required: true
      allow:
        authorization: ["bearer"]
        header: ["X-Api-Key"]
        query: ["session_id"]

rules:
  validate-all-credentials:
    # Match group requires ALL credentials present
    auth:
      - matchers:
          - type: bearer
          - type: header
            name: "X-Api-Key"
          - type: query
            name: "session_id"
        forwardAs:
          - type: bearer
            value: "{{ index .auth.input.bearer 0 }}"
          - type: header
            name: "X-Session-Token"
            value: "{{ .auth.input.header.x-api-key }}"

    backendApi:
      url: "https://api.example.com/validate"
      method: POST
      headers:
        templates:
          Content-Type: "application/json"
      body: |
        {
          "bearer_token": "{{ index .auth.input.bearer 0 }}",
          "api_key": "{{ .auth.input.header.x-api-key }}",
          "session_id": "{{ .auth.input.query.session_id }}"
        }
      acceptedStatuses: [200]

    conditions:
      pass:
        - 'backend.body.all_valid == true'
      fail:
        - 'backend.body.all_valid == false'
```

**Test**:
```bash
curl -H "Authorization: Bearer token123" \
     -H "X-Api-Key: key456" \
     "http://localhost:8080/secure-gateway/auth?session_id=session789"
```

**See**: `examples/configs/multi-credential-composition.yaml`

---

## Backend Token Introspection

Validate tokens with an OAuth2 introspection endpoint.

```yaml
endpoints:
  oauth-gateway:
    authentication:
      required: true
      allow:
        authorization: ["bearer"]
    rules:
      - name: introspect-token
      - name: check-scopes
    responsePolicy:
      pass:
        status: 200
        headers:
          custom:
            X-User-ID: "{{ .response.user_id }}"
            X-Scopes: "{{ .response.scopes }}"
      fail:
        status: 403

rules:
  introspect-token:
    backendApi:
      url: "https://oauth.example.com/introspect"
      method: POST
      headers:
        templates:
          Content-Type: "application/x-www-form-urlencoded"
      body: "token={{ index .auth.input.bearer 0 }}&token_type_hint=access_token"
      acceptedStatuses: [200]

    conditions:
      pass:
        - 'backend.body.active == true'
      fail:
        - 'backend.body.active == false'

    responses:
      pass:
        variables:
          # Export for downstream rules
          user_id: 'backend.body.sub'
          scopes: 'backend.body.scope'
          expires_at: 'backend.body.exp'

  check-scopes:
    conditions:
      pass:
        # Require 'api:read' scope
        - 'variables.global.scopes.contains("api:read")'
      fail:
        - '!variables.global.scopes.contains("api:read")'

    responses:
      pass:
        variables:
          # Re-export for response template
          user_id: 'variables.global.user_id'
          scopes: 'variables.global.scopes'
```

**See**: `examples/configs/backend-token-introspection.yaml`

---

## Webhook Signature Verification

Verify HMAC signatures on incoming webhooks.

```yaml
endpoints:
  github-webhook:
    authentication:
      required: false
      allow:
        none: true
    rules:
      - name: verify-github-signature
      - name: process-webhook
    responsePolicy:
      pass:
        status: 200
        body: '{"status":"accepted"}'
      fail:
        status: 401
        body: '{"error":"invalid signature"}'

rules:
  verify-github-signature:
    variables:
      # Extract signature from header
      provided_signature: 'lookup(forward.headers, "x-hub-signature-256")'
      # Get raw body (for signature computation)
      payload_body: 'lookup(forward.headers, "x-original-body")'

    conditions:
      pass:
        # In production, compute HMAC-SHA256 and compare
        # For now, just check signature exists
        - 'variables.local.provided_signature != null'
        - 'variables.local.provided_signature.startsWith("sha256=")'
      fail:
        - 'variables.local.provided_signature == null'

    responses:
      pass:
        variables:
          webhook_verified: '"true"'

  process-webhook:
    backendApi:
      url: "https://internal.example.com/webhooks/github"
      method: POST
      headers:
        templates:
          Content-Type: "application/json"
          X-Webhook-Verified: "{{ .variables.global.webhook_verified }}"
      # Forward original body
      bodyFile: "webhooks/forward.json"
      acceptedStatuses: [200, 202]

    conditions:
      pass:
        - 'backend.status in [200, 202]'
```

---

## Rate Limiting with Redis

Implement per-client rate limiting using Redis counters.

```yaml
server:
  cache:
    backend: redis
    redis:
      address: "redis:6379"

endpoints:
  rate-limited-api:
    authentication:
      required: true
      allow:
        authorization: ["bearer"]
    rules:
      - name: check-rate-limit
      - name: call-backend
    responsePolicy:
      pass:
        status: 200
      fail:
        status: 429
        headers:
          custom:
            Retry-After: "60"
        body: '{"error":"rate limit exceeded"}'

rules:
  check-rate-limit:
    variables:
      # Create rate limit key from client IP
      rate_key: 'admission.clientIp + ":rate_limit"'

    # Use backend to increment Redis counter
    backendApi:
      url: "https://rate-limiter.internal/increment"
      method: POST
      headers:
        templates:
          Content-Type: "application/json"
      body: |
        {
          "key": "{{ .variables.local.rate_key }}",
          "limit": 100,
          "window": 60
        }
      acceptedStatuses: [200]

    conditions:
      pass:
        - 'backend.body.allowed == true'
      fail:
        - 'backend.body.allowed == false'

    responses:
      pass:
        variables:
          remaining: 'backend.body.remaining'
          reset_at: 'backend.body.reset_at'

  call-backend:
    backendApi:
      url: "https://api.example.com/resource"
      method: GET
      acceptedStatuses: [200]

    responses:
      pass:
        headers:
          custom:
            X-RateLimit-Remaining: "{{ .variables.global.remaining }}"
            X-RateLimit-Reset: "{{ .variables.global.reset_at }}"
```

---

## Conditional Routing

Route requests to different backends based on conditions.

```yaml
endpoints:
  smart-router:
    authentication:
      required: true
      allow:
        authorization: ["bearer"]
    rules:
      - name: get-user-tier
      - name: route-to-backend
    responsePolicy:
      pass:
        status: 200

rules:
  get-user-tier:
    backendApi:
      url: "https://auth.example.com/user/tier"
      method: GET
      acceptedStatuses: [200]

    conditions:
      pass:
        - 'backend.status == 200'

    responses:
      pass:
        variables:
          tier: 'backend.body.tier'

  route-to-backend:
    variables:
      # Select backend URL based on tier
      backend_url: |
        variables.global.tier == "enterprise" ?
          "https://premium-api.example.com" :
          "https://standard-api.example.com"

    backendApi:
      url: "{{ .variables.local.backend_url }}/resource"
      method: GET
      acceptedStatuses: [200]

    conditions:
      pass:
        - 'backend.status == 200'
```

---

## Variable Propagation Patterns

Demonstrate variable scoping and propagation through rule chains.

```yaml
endpoints:
  variable-demo:
    # Endpoint-level variables (global scope)
    variables:
      endpoint_id: '"demo-endpoint"'

    rules:
      - name: rule-1-extract
      - name: rule-2-transform
      - name: rule-3-use

rules:
  rule-1-extract:
    # Extract data from backend, export globally
    backendApi:
      url: "https://api.example.com/user"
      method: GET
      acceptedStatuses: [200]

    variables:
      # Rule-scoped (available to this rule only, cleared after)
      local_temp: 'backend.body.temp_data'

    conditions:
      pass:
        - 'backend.status == 200'

    responses:
      pass:
        variables:
          # Export to global scope (available to all subsequent rules)
          user_id: 'backend.body.id'
          user_email: 'backend.body.email'

  rule-2-transform:
    # Transform global variables
    variables:
      # Rule-scoped variable using globals
      email_domain: 'variables.global.user_email.split("@")[1]'

    conditions:
      pass:
        - 'variables.local.email_domain == "example.com"'

    responses:
      pass:
        variables:
          # Export transformed data
          is_internal: '"true"'

  rule-3-use:
    # Use all accumulated variables
    backendApi:
      url: "https://api.example.com/authorize"
      method: POST
      body: |
        {
          "endpoint": "{{ .variables.global.endpoint_id }}",
          "user_id": "{{ .variables.global.user_id }}",
          "email": "{{ .variables.global.user_email }}",
          "internal": {{ .variables.global.is_internal }}
        }
      acceptedStatuses: [200]

    conditions:
      pass:
        - 'backend.status == 200'
```

### Variable Scopes

| Scope | Lifetime | Access | Use Case |
|-------|----------|--------|----------|
| `variables.environment.*` | Server lifetime | All rules, all requests | Configuration, feature flags |
| `variables.secrets.*` | Server lifetime | All rules, all requests | API keys, passwords |
| `variables.global.*` | Request lifetime | All rules in chain | Sharing data between rules |
| `variables.local.*` | Rule execution | Current rule only | Temporary computations |

---

## Next Steps

- **[Server Configuration](../server/)** - Configure server-wide settings
- **[Endpoint Configuration](../endpoints/)** - Define endpoint behavior
- **[Rule Configuration](../rules/)** - Author rule logic
- **[CEL Expressions Guide](../../guides/cel-expressions/)** - Learn CEL syntax

**Example Directories:**
- `examples/configs/` - Working configuration files
- `examples/docker-secrets/` - Docker/Kubernetes secrets integration
- `examples/valkey-cache/` - Redis/Valkey caching setup
