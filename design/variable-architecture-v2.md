# Variable Architecture V2

## Overview

PassCtrl uses a three-tier variable system to manage data flow through the rule chain:
1. **Endpoint Variables** - Configuration-level values available to all rules
2. **Local Variables** - Rule-scoped temporaries for intermediate calculations
3. **Exported Variables** - Cross-rule data explicitly shared via response outcomes

## Hybrid Evaluation Model

Variables support both CEL and Go Templates, detected automatically:

- **CEL** (no `{{`): Type-preserving expressions for extraction and logic
  ```yaml
  user_id: backend.body.userId           # Returns number/string as-is
  is_active: backend.body.status == "active"  # Returns boolean
  ```

- **Template** (contains `{{`): String interpolation and transformation
  ```yaml
  cache_key: "user:{{ .backend.body.userId }}:{{ .variables.endpoint.tenant }}"
  email_lower: "{{ .backend.body.email | lower }}"
  ```

## 1. Endpoint Variables

**Purpose**: Configuration-level constants and request-derived values available to all rules.

**Configuration:**
```yaml
endpoints:
  api-gateway:
    variables:
      # Static configuration
      api_base: "https://api.internal"
      default_region: "us-east-1"

      # Environment variables
      tenant_id: "{{ env \"TENANT_ID\" }}"
      api_key: "{{ env \"BACKEND_API_KEY\" }}"

      # Request extraction (CEL)
      client_ip: request.remoteAddr
      request_method: request.method

      # Request extraction (Template)
      tenant_header: "{{ .request.headers.x-tenant-id }}"
      correlation_id: "{{ .request.headers.x-request-id | default (uuidv4) }}"
```

**CEL Context:**
- `request.remoteAddr`
- `request.method`
- `request.path`
- `request.headers["name"]`
- `request.query["param"]`

**Template Context:**
- `{{ .request.remoteAddr }}`
- `{{ .request.method }}`
- `{{ .request.headers.x-tenant-id }}`
- `{{ env "VAR" }}`
- Sprig functions: `{{ .request.path | base }}`

**Evaluation**: Once per request, before rule execution.

**Access**: `.variables.endpoint.<name>` in all rule contexts

## 2. Local Variables

**Purpose**: Rule-scoped temporary values for intermediate calculations. Not exported to other rules.

**Configuration:**
```yaml
rules:
  lookup-user:
    backendApi:
      url: "{{ .variables.endpoint.api_base }}/validate"
    variables:
      # CEL - type-preserving extraction
      raw_user_id: backend.body.userId
      raw_email: backend.body.email
      raw_tier: backend.body.subscription.tier
      is_active: backend.body.status == "active"
      role_count: size(backend.body.roles)

      # Template - string construction
      display_name: "{{ .backend.body.firstName }} {{ .backend.body.lastName }}"
      cache_key: "user:{{ .backend.body.userId }}:{{ .variables.endpoint.tenant_id }}"
      email_lower: "{{ .backend.body.email | lower | trim }}"
```

**CEL Context:**
- `backend.body.*`
- `backend.status`
- `backend.headers["name"]`
- `auth.input.*`
- `variables.endpoint.*` (endpoint variables)
- `request.*`

**Template Context:**
- `{{ .backend.body.firstName }}`
- `{{ .backend.status }}`
- `{{ .auth.input.token }}`
- `{{ .variables.endpoint.api_base }}`

**Evaluation**: After backend call, before conditions.

**Access**: `.variables.local.<name>` within the rule only.

**Lifecycle**: Ephemeral - not cached, not visible to other rules.

## 3. Exported Variables

**Purpose**: Explicitly share data from one rule to subsequent rules. Only variables from the winning outcome are exported.

**Configuration:**
```yaml
rules:
  lookup-user:
    variables:
      # Local variables for internal use
      temp_id: backend.body.userId
      temp_email: backend.body.email
    responses:
      pass:
        headers:
          custom:
            X-User-ID: "{{ .variables.local.temp_id }}"
        variables:
          # Export to other rules
          user_id: variables.local.temp_id
          email: "{{ .variables.local.temp_email | lower }}"
          tier: variables.local.temp_tier
      fail:
        variables:
          error_code: backend.body.errorCode
          error_message: "{{ .backend.body.message | default \"Auth failed\" }}"
```

**CEL Context:**
- `.variables.local.*` (local variables)
- `.backend.*`
- `.auth.*`
- `.variables.endpoint.*` (endpoint variables)

**Template Context:**
- Same as above with `{{ }}` syntax

**Evaluation**: After conditions determine outcome.

**Access**: `.variables.rule.<rule-name>.<name>` in subsequent rules.

**Lifecycle**: Cached with rule outcome, available to all later rules.

## Usage Examples

### Example 1: Multi-Step API Chain

```yaml
endpoints:
  api-gateway:
    variables:
      api_base: "https://api.internal"
      tenant: "{{ .request.headers.x-tenant-id }}"
    rules:
      - name: validate-token
      - name: fetch-user-profile
      - name: check-permissions

rules:
  validate-token:
    backendApi:
      url: "{{ .vars.api_base }}/auth/validate"
      headers:
        custom:
          authorization: "Bearer {{ .auth.input.token }}"
    variables:
      token_user_id: backend.body.userId
      token_scopes: backend.body.scopes
    conditions:
      pass:
        - variables.local.token_user_id != ""
    responses:
      pass:
        variables:
          user_id: variables.local.token_user_id
          scopes: variables.local.token_scopes
    cache:
      passTTL: "5m"
      failTTL: "30s"

  fetch-user-profile:
    backendApi:
      # Uses exported variable from validate-token
      url: "{{ .variables.endpoint.api_base }}/users/{{ .variables.rule.validate-token.user_id }}"
    variables:
      profile_tier: backend.body.subscription.tier
      profile_status: backend.body.status
    responses:
      pass:
        variables:
          tier: variables.local.profile_tier
          status: variables.local.profile_status
    cache:
      passTTL: "10m"

  check-permissions:
    conditions:
      pass:
        # Uses exported variables from both previous rules
        - '"admin" in variables.rule["validate-token"].scopes'
        - 'variables.rule["fetch-user-profile"].status == "active"'
        - 'variables.rule["fetch-user-profile"].tier in ["premium", "enterprise"]'
```

### Example 2: Conditional Exports

```yaml
rules:
  lookup-session:
    backendApi:
      url: "{{ .variables.endpoint.api_base }}/sessions/{{ .auth.input.token }}"
    variables:
      session_user: backend.body.userId
      session_status: backend.body.status
      session_expires: backend.body.expiresAt
    responses:
      pass:
        variables:
          user_id: variables.local.session_user
          expires_at: variables.local.session_expires
      fail:
        # Different variables exported on failure
        variables:
          failure_reason: "session_expired"
          expired_at: variables.local.session_expires
```

## Caching Integration

### Cache Key Construction

For rules with `backendApi`:

```
credential | endpoint | path | rule-name | backend-hash
```

Where `backend-hash = sha256(canonicalized-backend-request)`:

```go
type BackendDescriptor struct {
    Method  string            // GET, POST, etc.
    URL     string            // After template rendering with endpoint vars + exported vars
    Headers map[string]string // Custom headers after rendering
    Body    string            // After template rendering
}
```

**Example:**
```yaml
rules:
  fetch-permissions:
    backendApi:
      url: "{{ .variables.endpoint.api_base }}/users/{{ .variables.rule.lookup-user.user_id }}/perms"
      headers:
        custom:
          x-tenant: "{{ .variables.endpoint.tenant }}"
```

Cache key includes hash of:
- URL: `https://api.internal/users/123/perms` (rendered with user_id=123)
- Headers: `{x-tenant: "acme-corp"}`

If `user_id` changes to `456`, URL renders differently → different hash → cache miss ✅

### Cache Entry Structure

```go
type RuleCacheEntry struct {
    Outcome      string            `json:"outcome"`   // "pass" | "fail"
    Reason       string            `json:"reason"`
    Exported     map[string]any    `json:"exported"`  // From responses.<outcome>.variables
    Headers      map[string]string `json:"headers"`   // From responses.<outcome>.headers.custom
    StoredAt     time.Time         `json:"storedAt"`
    ExpiresAt    time.Time         `json:"expiresAt"`
}
```

**Local variables are NOT cached** - they're ephemeral temporaries.

### Cache Behavior

```yaml
rules:
  lookup-user:
    variables:
      temp_id: backend.body.userId  # Not cached
    responses:
      pass:
        variables:
          user_id: variables.local.temp_id  # This IS cached
    cache:
      passTTL: "5m"
      failTTL: "30s"
```

**On cache hit:**
1. Skip backend call
2. Restore outcome + exported variables
3. Make exported vars available as `.rules.lookup-user.variables.*`
4. Subsequent rules can use them

### TTL Selection

```yaml
cache:
  passTTL: "5m"   # When outcome == "pass"
  failTTL: "30s"  # When outcome == "fail"
```

- `passTTL: "0s"` → Don't cache pass outcomes
- `failTTL: "0s"` → Don't cache fail outcomes
- Error outcomes → Never cached regardless of config

## Migration from V1

### Old Schema (v1)
```yaml
rules:
  lookup-user:
    variables:
      global:
        user_id:
          from: backend.body.userId
        tier:
          from: backend.body.tier
```

### New Schema (v2)
```yaml
rules:
  lookup-user:
    variables:
      # Local (temporary)
      temp_user_id: backend.body.userId
      temp_tier: backend.body.tier
    responses:
      pass:
        variables:
          # Exported (shared)
          user_id: variables.local.temp_user_id
          tier: variables.local.temp_tier
```

### Key Differences

| Aspect | V1 | V2 |
|--------|----|----|
| Scopes | `global`, `rule`, `local` | `endpoint`, `local`, `exported` |
| Export | Implicit (all global vars) | Explicit (responses.*.variables) |
| Cache | All variables cached | Only exported vars cached |
| Access (endpoint) | Not supported | `.variables.endpoint.name` |
| Access (local) | `.variables.local.name` | `.variables.local.name` |
| Access (exported) | `.vars.global.name` | `.variables.rule.<rule>.name` |
| Syntax | CEL only with `from:` | Hybrid CEL/Template |

## Implementation Notes

### Evaluation Order

1. **Request Start**: Evaluate endpoint variables
2. **Rule Execution**:
   a. Render backend request (uses endpoint vars + previous exports)
   b. Check cache (keyed by rendered backend)
   c. Execute backend (if cache miss)
   d. Evaluate local variables
   e. Evaluate conditions
   f. Render exported variables from winning outcome
   g. Cache entry with exported vars

### Variable Resolution

```go
func evaluateVariable(expr string, ctx any) (any, error) {
    if strings.Contains(expr, "{{") {
        // Template: returns string
        return templates.Render(expr, ctx)
    }
    // CEL: preserves type
    return cel.Eval(expr, ctx)
}
```

### Template Context Structure

```go
type RuleContext struct {
    // Endpoint variables (evaluated at request start)
    Vars map[string]any

    // Request metadata
    Request struct {
        RemoteAddr string
        Method     string
        Path       string
        Headers    map[string]string
        Query      map[string]string
    }

    // Authentication
    Auth struct {
        Input  map[string]any
        Output map[string]any
    }

    // Current rule
    Variables map[string]any  // Local variables
    Backend struct {
        Status  int
        Body    map[string]any
        Headers map[string]string
    }

    // Previous rules' exports
    Rules map[string]struct {
        Variables map[string]any
    }
}
```

## Benefits

1. **Clear Separation**: Endpoint config, local temps, and exports are distinct
2. **Explicit Exports**: Only export what's needed, reducing cache size
3. **Automatic Dependencies**: Cache keys include rendered backend → automatic invalidation
4. **Type Preservation**: CEL keeps numbers/bools, templates for strings
5. **Single Outcome**: Only winning outcome's variables are exported/cached
6. **Smaller Cache**: Don't persist intermediate calculations
