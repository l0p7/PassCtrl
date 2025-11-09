# CEL Expression Guide for PassCtrl

This guide provides comprehensive documentation for writing CEL (Common Expression Language) expressions in PassCtrl rule configurations.

## Table of Contents

1. [Expression Context](#expression-context)
2. [Backend Response Parsing](#backend-response-parsing)
3. [Variable Access Patterns](#variable-access-patterns)
4. [Cross-Context Validation](#cross-context-validation)
5. [String Operations](#string-operations)
6. [Numeric and Time Operations](#numeric-and-time-operations)
7. [Array and List Operations](#array-and-list-operations)
8. [Boolean Logic](#boolean-logic)
9. [Error Handling](#error-handling)
10. [Best Practices](#best-practices)
11. [Anti-Patterns to Avoid](#anti-patterns-to-avoid)
12. [Complete Examples](#complete-examples)

---

## Expression Context

CEL expressions in PassCtrl have access to a structured context with the following top-level fields:

```yaml
backend:
  status: int                    # HTTP status code (e.g., 200, 404, 500)
  body: map<string, dynamic>     # Parsed JSON response body
  headers: map<string, string>   # Response headers (lowercase keys)

variables:
  endpoint: map<string, dynamic>   # Endpoint-level variables (from endpoint-level variables)
  rule: map<string, map<string, dynamic>>   # Variables from completed rules (keyed by rule name)
  local: map<string, dynamic>    # Rule-local variables (from current rule's variables block)

request:
  method: string                 # HTTP method (GET, POST, etc.)
  path: string                   # Request path
  query: map<string, string>     # Query parameters
  headers: map<string, string>   # Request headers (lowercase keys)

admission:
  authenticated: bool            # Whether request passed authentication
  decision: string               # Admission decision ("pass", "fail")
  clientIp: string              # Client IP address
  trustedProxy: bool            # Whether request came from trusted proxy

endpoint: string                 # Endpoint name (e.g., "admin-api")

auth:
  input: map                     # Credentials from request (bearer, basic, header, query)
  forward: map                   # Credentials after transformation (for backend forwarding)
```

---

## Backend Response Parsing

### Basic Field Access

```yaml
# Simple field access
- backend.body.user_id == "12345"

# Status code checks
- backend.status == 200
- backend.status >= 200 && backend.status < 300
```

### Nested JSON Paths

```yaml
# Navigate nested structures
- backend.body.data.user.roles[0] == "admin"
- backend.body.metadata.timestamps.created > 1609459200

# Access nested optional fields safely
- has(backend.body.data) && backend.body.data.user.email.endsWith("@example.com")
```

### Checking Field Existence

**PREFERRED**: Use CEL's `has()` macro for checking field existence:

```yaml
# Check if field exists before accessing
- has(backend.body.user_id) && backend.body.user_id != ""

# Check nested field existence
- has(backend.body.data) && has(backend.body.data.user)

# Complex existence checks
- has(backend.body.permissions) && "read" in backend.body.permissions
```

**ALTERNATIVE**: Use the `lookup()` helper for optional field access:

```yaml
# lookup(map, key) returns empty value if key doesn't exist
- lookup(backend.body, "user_id") != ""

# Nested lookups (verbose, avoid if possible)
- lookup(lookup(backend.body, "data"), "user") != null
```

### Working with Backend Headers

```yaml
# Access response headers (all lowercase)
- backend.headers["cache-control"].contains("max-age")
- has(backend.headers["x-rate-limit-remaining"]) &&
  int(backend.headers["x-rate-limit-remaining"]) > 100

# Check for presence of headers
- has(backend.headers["x-api-version"]) &&
  backend.headers["x-api-version"] == "v2"
```

---

## Variable Access Patterns

### Rule Variables - Using `in` Operator and `lookup()` (PREFERRED)

Access variables from other rules using the `in` operator for existence checks and `lookup()` for safe field access:

```yaml
# Check if rule has exported variables (use 'in' operator)
- '"validate-token" in variables.rule'

# Access rule variables safely with lookup()
- 'lookup(variables.rule["validate-token"], "user_id") == "12345"'
- 'lookup(variables.rule["validate-token"], "tier") == "premium"'

# Combined existence check and field access
- '"validate-token" in variables.rule'
- 'lookup(variables.rule["validate-token"], "tier") in ["plus", "enterprise"]'

# Combine multiple rule variables
- 'lookup(variables.rule["check-role"], "role") == "admin"'
- 'lookup(variables.rule["check-quota"], "remaining") > 0'
```

### Rule Variables - lookup() Helper (AVOID if possible)

The `lookup()` helper provides safe access but is verbose:

```yaml
# Nested lookup pattern (VERBOSE - original pattern)
- lookup(lookup(variables.rule, "validate-token"), "user_id") != ""

# Better alternative using 'in' operator and single lookup()
- '"validate-token" in variables.rule'
- 'lookup(variables.rule["validate-token"], "user_id") != ""'
```

### Endpoint-Level Variables

```yaml
# Access endpoint-level variables set at endpoint level
- variables.endpoint.api_version == "v2"
- variables.endpoint.rate_limit_enabled == true
```

### Rule-Local Variables

```yaml
# Access variables defined in current rule's variables block
- variables.local.computed_score > 0.8
- variables.local.risk_level == "low"
```

---

## Cross-Context Validation

Cross-context validation allows you to compare values from different parts of the request pipeline. This is essential for authorization checks, multi-stage validation, and ensuring consistency across rule executions.

### Comparing Backend Response to Authentication

Verify that backend responses match the authenticated user's identity:

```yaml
# Ensure backend returned data for the authenticated user
- backend.body.user_id == auth.input.bearer.claims.sub
- backend.body.email == auth.input.bearer.claims.email

# Validate user identity from header-based auth
- backend.body.account_id == auth.input.header["x-account-id"]

# Compare backend response to basic auth username
- backend.body.username == auth.input.basic.username

# Cross-validate multiple auth sources
- backend.body.client_id == auth.input.query["client_id"]
- backend.body.api_key.startsWith(auth.input.header["x-api-key"])
```

**Use Case: Authorization**
```yaml
# Rule that ensures users can only access their own data
validate-user-access:
  auth:
    - match:
        - type: bearer
  backendApi:
    url: "https://api.example.com/user/profile"
    headers:
      authorization: "Bearer {{ .auth.input.bearer.token }}"
  conditions:
    pass:
      # Backend must return data for the authenticated user
      - backend.status == 200
      - has(backend.body.user_id)
      - backend.body.user_id == auth.input.bearer.claims.sub
    fail:
      # Explicit denial when user_id mismatch (potential unauthorized access)
      - backend.body.user_id != auth.input.bearer.claims.sub
```

### Comparing Backend Response to Variables

Validate that backend responses match expected values from previous rules or endpoint variables:

```yaml
# Verify backend confirms previously validated tier
- backend.body.tier == variables.endpoint.expected_tier
- backend.body.subscription_level == variables.endpoint.plan

# Check backend response against rule-local computation
- backend.body.quota_remaining >= variables.local.minimum_quota
- backend.body.account_status == variables.local.expected_status

# Correlate backend response with data from earlier rule
- '"fetch-profile" in variables.rule'
- backend.body.organization_id == lookup(variables.rule["fetch-profile"], "org_id")

# Multi-rule validation chain
- '"validate-token" in variables.rule'
- backend.body.permissions.exists(p,
    p == lookup(variables.rule["validate-token"], "required_permission"))
```

**Use Case: Multi-Stage Validation**
```yaml
# First rule validates token and stores expected values
introspect-token:
  backendApi:
    url: "https://auth.example.com/introspect"
  conditions:
    pass:
      - backend.status == 200
      - backend.body.active == true
  variables:
    endpoint:
      validated_user_id: backend.body.sub
      expected_tier: backend.body.tier

# Second rule fetches profile and confirms consistency
fetch-profile:
  backendApi:
    url: "https://api.example.com/profile"
  conditions:
    pass:
      # Backend response must match token introspection results
      - backend.status == 200
      - backend.body.user_id == variables.endpoint.validated_user_id
      - backend.body.subscription_tier == variables.endpoint.expected_tier
    fail:
      # Data inconsistency between auth server and profile service
      - backend.body.user_id != variables.endpoint.validated_user_id
      - backend.body.subscription_tier != variables.endpoint.expected_tier
    error:
      # Profile service unavailable
      - backend.status >= 500
```

### Comparing Authentication to Variables

Ensure authentication credentials match expected patterns stored in variables:

```yaml
# Verify authenticated user matches expected role
- variables.endpoint.required_role == "admin"
- auth.input.bearer.claims.role == variables.endpoint.required_role

# Check if user is in allowlist
- auth.input.basic.username in variables.endpoint.allowed_users

# Validate client credentials against stored configuration
- variables.local.valid_client_ids.exists(id, id == auth.input.query["client_id"])
```

### Complex Cross-Context Patterns

Combine multiple contexts for sophisticated validation:

```yaml
pass:
  # All three contexts must align
  - backend.status == 200
  - backend.body.user_id == auth.input.bearer.claims.sub
  - backend.body.tier == variables.endpoint.minimum_tier

  # Backend confirms auth claim
  - has(backend.body.roles)
  - backend.body.roles.exists(r, r == auth.input.bearer.claims.role)

  # Computed variable validates against backend
  - variables.local.computed_quota <= backend.body.quota_limit

fail:
  # Any mismatch is a hard failure
  - backend.body.user_id != auth.input.bearer.claims.sub
  - backend.body.organization != variables.endpoint.expected_org
```

**Use Case: Security Correlation**
```yaml
# Ensure request, auth, and backend all agree on tenant context
validate-tenant-isolation:
  backendApi:
    url: "https://api.example.com/tenant/{{ .auth.input.header.x-tenant-id }}/data"
  conditions:
    pass:
      # Triple verification: request header, auth claim, backend response
      - backend.status == 200
      - has(backend.body.tenant_id)
      - backend.body.tenant_id == auth.input.header["x-tenant-id"]
      - backend.body.tenant_id == auth.input.bearer.claims.tenant_id
      - backend.body.active == true
    fail:
      # Tenant mismatch is a security violation
      - backend.body.tenant_id != auth.input.header["x-tenant-id"]
      - backend.body.tenant_id != auth.input.bearer.claims.tenant_id
```

### Best Practices for Cross-Context Validation

1. **Always use `has()` before accessing optional fields**:
   ```yaml
   # GOOD: Safe access
   - has(backend.body.user_id) && backend.body.user_id == auth.input.bearer.claims.sub

   # BAD: Crashes if backend.body.user_id is missing
   - backend.body.user_id == auth.input.bearer.claims.sub
   ```

2. **Check variable existence before comparing**:
   ```yaml
   # GOOD: Verify rule exported variables first
   - '"fetch-profile" in variables.rule'
   - backend.body.org_id == lookup(variables.rule["fetch-profile"], "org_id")

   # BAD: Assumes fetch-profile ran and exported org_id
   - backend.body.org_id == lookup(variables.rule["fetch-profile"], "org_id")
   ```

3. **Use explicit failure conditions for security-critical mismatches**:
   ```yaml
   pass:
     - backend.body.user_id == auth.input.bearer.claims.sub
   fail:
     # Don't rely on implicit fail - make security violations explicit
     - backend.body.user_id != auth.input.bearer.claims.sub
   ```

4. **Normalize values before comparison**:
   ```yaml
   # GOOD: Case-insensitive email comparison
   - backend.body.email.lowerAscii() == auth.input.bearer.claims.email.lowerAscii()

   # BAD: Case-sensitive comparison might miss matches
   - backend.body.email == auth.input.bearer.claims.email
   ```

---

## String Operations

### Pattern Matching

```yaml
# matches() for regex patterns
- backend.body.email.matches("^[a-z0-9._%+-]+@[a-z0-9.-]+\\.[a-z]{2,}$")
- backend.body.username.matches("^admin-[0-9]+$")

# Case-insensitive matching
- backend.body.role.matches("(?i)^(admin|superuser)$")
```

### String Checks

```yaml
# contains()
- backend.body.message.contains("success")
- backend.headers["content-type"].contains("application/json")

# startsWith() / endsWith()
- backend.body.user_id.startsWith("usr_")
- backend.body.email.endsWith("@internal.com")

# String length
- size(backend.body.token) >= 32
```

### String Manipulation

```yaml
# Lowercase/uppercase (CEL standard functions)
- backend.body.status.lowerAscii() == "active"
- backend.body.role.upperAscii() in ["ADMIN", "OWNER"]

# String concatenation in conditions
- backend.body.prefix + backend.body.suffix == "expected-value"
```

---

## Numeric and Time Operations

### Numeric Comparisons

```yaml
# Basic comparisons
- backend.body.age >= 18
- backend.body.score > 0.75
- backend.body.retry_count < 3

# Range checks
- backend.body.temperature >= 20.0 && backend.body.temperature <= 25.0

# Calculations
- backend.body.total_amount - backend.body.discount > 100.0
- backend.body.usage_percent < 80.0
```

### Timestamp Handling

```yaml
# Unix timestamp comparisons (backend.body.expires_at is an int)
- backend.body.expires_at > 1735689600  # After 2025-01-01

# Check if not expired (timestamp in future)
- backend.body.expiry > timestamp("2025-06-01T00:00:00Z").getSeconds()

# Duration checks (backend returns duration in seconds)
- backend.body.session_duration < 3600  # Less than 1 hour
- backend.body.cache_ttl >= 300  # At least 5 minutes
```

### Type Conversions

```yaml
# Convert string to int
- int(backend.headers["x-rate-limit-remaining"]) > 100

# Convert string to double
- double(backend.body.score_string) >= 0.8

# Check type before conversion
- has(backend.body.count) && type(backend.body.count) == int
```

---

## Array and List Operations

### Membership Checks

```yaml
# Check if value is in array
- "admin" in backend.body.roles
- backend.body.subscription_tier in ["premium", "enterprise"]

# Check if array contains specific element
- "read" in backend.body.permissions && "write" in backend.body.permissions
```

### Array Operations

```yaml
# Check array size
- size(backend.body.roles) > 0
- size(backend.body.permissions) >= 2

# Access array elements
- backend.body.roles[0] == "admin"
- backend.body.tags[size(backend.body.tags) - 1] == "verified"

# Check if array is empty
- size(backend.body.errors) == 0
```

### Array Filtering (using macros)

```yaml
# exists() macro - check if any element matches
- backend.body.roles.exists(r, r == "admin")
- backend.body.permissions.exists(p, p.startsWith("write:"))

# all() macro - check if all elements match
- backend.body.tags.all(t, t.startsWith("valid_"))
```

---

## Boolean Logic

### Simple Conditions

```yaml
# Single condition
- backend.body.active == true

# Negation
- !backend.body.locked
- backend.body.suspended != true
```

### Complex AND/OR Logic

```yaml
# Multiple conditions with AND
- backend.status == 200 &&
  backend.body.valid == true &&
  backend.body.tier in ["premium", "enterprise"]

# Multiple conditions with OR
- backend.body.role == "admin" ||
  backend.body.role == "superuser" ||
  backend.body.override == true

# Mixed AND/OR with parentheses
- (backend.body.role == "admin" || backend.body.owner == true) &&
  backend.body.active == true
```

### Ternary-Like Expressions

CEL doesn't have ternary operators, but you can achieve similar results:

```yaml
# Using boolean short-circuit
- backend.body.tier == "enterprise" && backend.body.limit > 10000 ||
  backend.body.tier == "premium" && backend.body.limit > 1000 ||
  backend.body.limit > 100

# Better: separate conditions for clarity
pass:
  - backend.body.tier == "enterprise" && backend.body.limit > 10000
  - backend.body.tier == "premium" && backend.body.limit > 1000
  - backend.body.tier == "free" && backend.body.limit > 100
```

---

## Error Handling

### Status Code-Based Error Classification

```yaml
# Distinguish client errors (4xx) from server errors (5xx)
pass:
  - backend.status >= 200 && backend.status < 300

fail:
  - backend.status >= 400 && backend.status < 500

error:
  - backend.status >= 500
```

### Backend Error Responses

```yaml
# Check for error fields in response body
error:
  - has(backend.body.error) && backend.body.error != ""
  - has(backend.body.error_code) && backend.body.error_code in ["TIMEOUT", "UNAVAILABLE"]

# Distinguish error types
fail:
  - backend.status == 400 && backend.body.error_code == "INVALID_REQUEST"

error:
  - backend.status == 503 && backend.body.error_code == "MAINTENANCE"
```

### Timeout and Network Error Handling

Note: PassCtrl will set `backend.status = 0` for network failures (connection refused, timeout, etc.)

```yaml
error:
  - backend.status == 0  # Network failure or timeout
  - backend.status >= 500  # Server error
  - backend.status == 504  # Gateway timeout
```

---

## Best Practices

### 1. Use `has()` for Optional Fields

**GOOD**:
```yaml
- has(backend.body.user) && backend.body.user.verified == true
```

**BAD**:
```yaml
- backend.body.user.verified == true  # Fails if user field doesn't exist
```

### 2. Use `in` Operator and `lookup()` for Rule Variables

**GOOD**:
```yaml
- '"validate-token" in variables.rule'
- 'lookup(variables.rule["validate-token"], "tier") == "premium"'
```

**BAD** (nested lookups):
```yaml
- lookup(lookup(variables.rule, "validate-token"), "tier") == "premium"
```

### 3. Avoid Redundant Conditions

**GOOD**:
```yaml
pass:
  - backend.body.active == true
# Implicit: anything not matching pass goes to fail
```

**BAD**:
```yaml
pass:
  - backend.body.active == true
fail:
  - backend.body.active != true  # Redundant
```

### 4. Use Short-Circuit Evaluation

**GOOD**:
```yaml
- has(backend.body.data) && backend.body.data.count > 0
```

This safely checks existence before accessing. If `backend.body.data` doesn't exist, the right side is never evaluated.

### 5. Order Conditions by Specificity

```yaml
# Most specific first
pass:
  - backend.status == 200 && backend.body.tier == "enterprise"
  - backend.status == 200 && backend.body.tier == "premium"
  - backend.status == 200 && backend.body.tier == "free"

fail:
  - backend.status == 403
  - backend.status >= 400 && backend.status < 500

error:
  - backend.status >= 500
  - backend.status == 0
```

### 6. Keep Expressions Readable

**GOOD**:
```yaml
pass:
  - backend.status == 200
  - has(backend.body.user)
  - backend.body.user.verified == true
  - backend.body.user.tier in ["premium", "enterprise"]
```

**BAD**:
```yaml
pass:
  - backend.status == 200 && has(backend.body.user) && backend.body.user.verified == true && backend.body.user.tier in ["premium", "enterprise"]
```

Multiple conditions are easier to read and debug when separated.

---

## Anti-Patterns to Avoid

### 1. Triple-Nested lookup()

**AVOID**:
```yaml
- lookup(lookup(lookup(variables.rule, "outer"), "inner"), "field") != ""
```

**USE**:
```yaml
- '"outer" in variables.rule'
- 'lookup(lookup(variables.rule["outer"], "inner"), "field") != ""'
```

### 2. Checking Status in Every Condition

**AVOID**:
```yaml
pass:
  - backend.status == 200 && backend.body.active == true
fail:
  - backend.status == 200 && backend.body.active == false
error:
  - backend.status >= 500
```

**USE**:
```yaml
pass:
  - backend.body.active == true
fail:
  - backend.body.active == false
error:
  - backend.status >= 500
```

The status check is implicit - if the backend returns 200, evaluate the body conditions.

### 3. Inverting Pass Conditions for Fail

**AVOID**:
```yaml
pass:
  - backend.body.valid == true
fail:
  - backend.body.valid != true  # Redundant
```

**USE**:
```yaml
pass:
  - backend.body.valid == true
# Let fail be implicit for non-pass cases
```

### 4. Not Checking Field Existence

**AVOID**:
```yaml
- backend.body.optional_field == "value"  # Crashes if field missing
```

**USE**:
```yaml
- has(backend.body.optional_field) && backend.body.optional_field == "value"
```

---

## Complete Examples

### Example 1: Token Validation with User Metadata

```yaml
rules:
  validate-bearer:
    auth:
      - match:
          - type: bearer
        forwardAs:
          - type: bearer
            token: "{{ .auth.input.bearer.token }}"

    backendApi:
      url: "https://auth.api/validate"
      method: POST
      acceptedStatuses: [200]

    conditions:
      pass:
        # Check token is active and user has required tier
        - has(backend.body.active) && backend.body.active == true
        - has(backend.body.user) && has(backend.body.user.tier)
        - backend.body.user.tier in ["premium", "enterprise"]

        # Check token hasn't expired
        - has(backend.body.expires_at) && backend.body.expires_at > 1735689600

      fail:
        # Explicit failure conditions
        - backend.body.active == false
        - backend.body.user.tier == "expired"

      error:
        # Network or server errors
        - backend.status >= 500
        - backend.status == 0

    variables:
      user_id: backend.body.user.id
      tier: backend.body.user.tier
      permissions: backend.body.user.permissions

    responses:
      pass:
        variables:
          user_id: variables.user_id
          tier: variables.tier
          permissions: variables.permissions
```

### Example 2: Multi-Rule Variable Composition

```yaml
rules:
  check-role:
    backendApi:
      url: "https://roles.api/check"
      method: GET
      acceptedStatuses: [200]

    conditions:
      pass:
        - backend.body.role in ["admin", "editor", "viewer"]

    variables:
      role: backend.body.role
      role_level: backend.body.level

    responses:
      pass:
        variables:
          role: variables.role
          role_level: variables.role_level

  check-quota:
    conditions:
      pass:
        # Access previous rule's variables
        - '"check-role" in variables.rule'
        - 'lookup(variables.rule["check-role"], "role") in ["admin", "editor"]'
        - 'lookup(variables.rule["check-role"], "role_level") >= 5'

        # Combine with current rule logic
        - backend.body.quota_remaining > 100
        - backend.body.quota_used < backend.body.quota_limit * 0.8

      fail:
        - 'lookup(variables.rule["check-role"], "role") == "viewer"'
        - backend.body.quota_remaining == 0
```

### Example 3: Complex Backend Response Parsing

```yaml
rules:
  parse-api-response:
    backendApi:
      url: "https://api.example.com/data"
      method: GET
      acceptedStatuses: [200, 206]

    conditions:
      pass:
        # Parse nested JSON structure
        - has(backend.body.data) && size(backend.body.data.items) > 0

        # Check array elements
        - backend.body.data.items.exists(item,
            item.status == "active" && item.verified == true)

        # Verify metadata
        - has(backend.body.metadata) &&
          has(backend.body.metadata.pagination)
        - backend.body.metadata.pagination.total > 0

        # Check response headers
        - has(backend.headers["x-api-version"]) &&
          backend.headers["x-api-version"] == "v2"

        # Timestamp validation
        - has(backend.body.metadata.timestamp) &&
          backend.body.metadata.timestamp > 1700000000

      fail:
        # No active items found
        - size(backend.body.data.items) == 0
        - backend.body.data.items.all(item, item.status != "active")

      error:
        # API errors
        - has(backend.body.error) &&
          backend.body.error.code in ["RATE_LIMITED", "MAINTENANCE"]
        - backend.status >= 500
```

### Example 4: String Pattern Matching and Validation

```yaml
rules:
  validate-input:
    conditions:
      pass:
        # Email validation
        - has(backend.body.email) &&
          backend.body.email.matches("^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}$")

        # Username pattern (alphanumeric, 3-20 chars)
        - has(backend.body.username) &&
          backend.body.username.matches("^[a-zA-Z0-9_]{3,20}$")

        # Token format validation
        - has(backend.body.access_token) &&
          backend.body.access_token.startsWith("tk_") &&
          size(backend.body.access_token) == 35

        # Domain allowlist
        - backend.body.email.endsWith("@example.com") ||
          backend.body.email.endsWith("@partner.com")

      fail:
        # Invalid formats
        - !backend.body.email.contains("@")
        - size(backend.body.username) < 3
```

---

## Additional Resources

- [CEL Specification](https://github.com/google/cel-spec)
- [CEL Standard Definitions](https://github.com/google/cel-spec/blob/master/doc/langdef.md)
- [PassCtrl Configuration Reference](../configuration/)
- [PassCtrl Examples](../../examples/configs/)

---

*This guide is part of the PassCtrl documentation. For questions or contributions, see the [contributor guide](../development/contributor.md).*
