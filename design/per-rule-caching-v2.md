# Per-Rule Caching Architecture V2

## Overview

PassCtrl implements **per-rule caching** (Tier 1 of the two-tier caching architecture) where each rule with a `backendApi` can be cached independently with configurable TTLs based on outcome (pass/fail/error).

**Note**: This document describes **Tier 1: Per-Rule Caching**. PassCtrl also implements **Tier 2: Endpoint-Level Caching** which caches entire rule chain outcomes. See `system-agents.md` Section 9 for the complete two-tier caching architecture and rationale.

## Cache Key Structure

### Components

```
credential | endpoint | path | rule-name | backend-hash | upstream-vars-hash
```

1. **credential**: Extracted credential (secure, from cache key security fix)
   - `auth:Bearer_token123`
   - `header:x-api-key:abc123`
   - `query:token:xyz789`
   - `ip:192.168.1.100:12345` (fallback)

2. **endpoint**: Endpoint name
3. **path**: Request path
4. **rule-name**: Name of the rule being cached
5. **backend-hash**: `sha256(canonicalized-backend-request)`
6. **upstream-vars-hash**: `sha256(all-previous-exported-variables)` ← Optional (see strict mode)

### Backend Request Canonicalization

```go
type BackendDescriptor struct {
    Method  string            `json:"method"`  // GET, POST, etc.
    URL     string            `json:"url"`     // After template rendering
    Headers map[string]string `json:"headers"` // Custom headers, sorted keys
    Body    string            `json:"body"`    // After template rendering
}

func (d BackendDescriptor) Hash() string {
    keys := make([]string, 0, len(d.Headers))
    for k := range d.Headers {
        keys = append(keys, k)
    }
    sort.Strings(keys)

    canonical := d.Method + "|" + d.URL + "|"
    for _, k := range keys {
        canonical += k + ":" + d.Headers[k] + "|"
    }
    canonical += d.Body

    hash := sha256.Sum256([]byte(canonical))
    return hex.EncodeToString(hash[:])
}
```

**What's included in backend hash:**
- HTTP method
- Fully rendered URL (with template substitutions)
- Custom headers (after template rendering, sorted)
- Request body (after template rendering)

**NOT included:**
- Allow headers (already in credential)
- Timestamps
- Request-specific metadata not in the backend request

### Upstream Variables Hash (Strict Mode)

```go
func hashUpstreamVariables(upstreamVars map[string]map[string]any) string {
    // Sort rule names for determinism
    ruleNames := make([]string, 0, len(upstreamVars))
    for ruleName := range upstreamVars {
        ruleNames = append(ruleNames, ruleName)
    }
    sort.Strings(ruleNames)

    canonical := ""
    for _, ruleName := range ruleNames {
        vars := upstreamVars[ruleName]

        // Sort variable names for determinism
        varNames := make([]string, 0, len(vars))
        for varName := range vars {
            varNames = append(varNames, varName)
        }
        sort.Strings(varNames)

        // Build canonical string: ruleName.varName=value|
        for _, varName := range varNames {
            canonical += ruleName + "." + varName + "=" + fmt.Sprint(vars[varName]) + "|"
        }
    }

    hash := sha256.Sum256([]byte(canonical))
    return hex.EncodeToString(hash[:])
}
```

## Configuration Schema

```yaml
rules:
  rule-name:
    backendApi:
      # ... backend configuration ...
    cache:
      followCacheControl: true  # Parse backend Cache-Control header (optional)
      ttl:
        pass: "5m"   # Cache successful outcomes for 5 minutes
        fail: "30s"  # Cache failed outcomes for 30 seconds
        error: "0s"  # Never cache errors (hardcoded, can't override)
      strict: true   # Include upstream variables in cache key (default)
```

### Cache TTL Configuration

**Structure:**
```yaml
cache:
  ttl:
    pass: "5m"   # Duration when outcome == "pass"
    fail: "30s"  # Duration when outcome == "fail"
    error: "0s"  # Duration when outcome == "error" (always 0, hardcoded)
```

**TTL Precedence (highest to lowest):**
1. **Error outcomes** → Always `0s` (never cached, hardcoded)
2. **Backend Cache-Control** (if `followCacheControl: true`)
3. **Manual TTL** (`cache.ttl.pass/fail`)
4. **Default** → `0s` (no caching)

### Cache-Control Header Support

When `followCacheControl: true`, PassCtrl parses the backend's `Cache-Control` header:

**Supported directives:**
- `max-age=<seconds>` → Use this TTL
- `s-maxage=<seconds>` → Prefer over max-age (we're a shared cache)
- `no-cache` → Don't cache (TTL = 0)
- `no-store` → Don't cache (TTL = 0)
- `private` → Don't cache (TTL = 0)

**Examples:**

```yaml
# Backend returns: Cache-Control: max-age=300
cache:
  followCacheControl: true
  ttl:
    pass: "10m"  # Ignored - backend controls with 300s
    fail: "30s"  # Used (no backend on fail)
```

```yaml
# Backend returns: Cache-Control: no-cache
cache:
  followCacheControl: true
  ttl:
    pass: "5m"  # Ignored - backend says don't cache
```

```yaml
# Backend returns: Cache-Control: max-age=600, s-maxage=1200
cache:
  followCacheControl: true
  ttl:
    pass: "5m"  # Ignored - uses s-maxage=1200 (shared cache preference)
```

```yaml
# Ignore backend, use manual TTL
cache:
  followCacheControl: false
  ttl:
    pass: "5m"  # Always used
    fail: "30s"
```

### Strict Mode (Cache Invalidation Strategy)

**Purpose:** Control whether upstream variable changes invalidate downstream caches.

**Options:**
- `strict: true` (default) - Include all upstream exported variables in cache key
- `strict: false` - Only include backend request hash

#### Strict Mode (Default - Safe)

```yaml
rules:
  - name: lookup-user
    responses:
      pass:
        variables:
          user_id: "123"
          tier: "premium"
          metadata: "..."

  - name: fetch-permissions
    backendApi:
      url: "/users/{{ .rules.lookup-user.variables.user_id }}/permissions"
    cache:
      ttl:
        pass: "10m"
      strict: true  # DEFAULT
```

**Cache key includes:**
- Backend: `GET /users/123/permissions`
- Upstream vars: `lookup-user.tier=premium|lookup-user.user_id=123|lookup-user.metadata=...|`

**Behavior:**
- If `tier` changes from "premium" to "free" → upstream hash changes → **CACHE MISS**
- If `metadata` changes → upstream hash changes → **CACHE MISS**
- Guarantees consistency: cached data never stale relative to upstream state

**Use when:**
- You're not sure which upstream variables affect the backend
- Safety is more important than cache hit rate
- Backend might behave differently based on variables not in the request

#### Loose Mode (Opt-in - Performance)

```yaml
rules:
  - name: lookup-user
    responses:
      pass:
        variables:
          user_id: "123"
          tier: "premium"

  - name: fetch-profile
    backendApi:
      url: "/users/{{ .rules.lookup-user.variables.user_id }}/profile"
    cache:
      ttl:
        pass: "10m"
      strict: false  # OPT-IN
```

**Cache key includes:**
- Backend: `GET /users/123/profile`
- Upstream vars: *(not included)*

**Behavior:**
- If `tier` changes → backend URL unchanged → **CACHE HIT**
- Better cache hit rate when unused variables change
- ⚠️ Risk: If backend behavior depends on variables not in request, might use stale data

**Use when:**
- You KNOW the backend output only depends on what's in the URL/headers/body
- All dependencies are captured in the backend request
- Cache hit rate is critical

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

**What's cached:**
- Outcome (pass/fail, never error)
- Reason string
- Exported variables from winning outcome
- Custom response headers
- Timestamps

**What's NOT cached:**
- Local variables (ephemeral)
- Backend response body (only decision artifacts)
- Error outcomes (always 0s TTL)

## Execution Flow

```go
func executeRule(rule RuleConfig, upstreamExports map[string]map[string]any) RuleCacheEntry {
    // 1. Build template context with upstream variables
    ctx := TemplateContext{
        Vars:  endpointVars,
        Rules: upstreamExports,  // From previous rules
    }

    // 2. Render backend request (uses upstream vars)
    backendDesc := BackendDescriptor{
        Method:  rule.BackendAPI.Method,
        URL:     renderTemplate(rule.BackendAPI.URL, ctx),
        Headers: renderHeaders(rule.BackendAPI.Headers.Custom, ctx),
        Body:    renderTemplate(rule.BackendAPI.Body, ctx),
    }

    // 3. Build cache key
    backendHash := backendDesc.Hash()
    upstreamHash := ""
    if rule.Cache.IsStrict() {
        upstreamHash = hashUpstreamVariables(upstreamExports)
    }

    cacheKey := buildCacheKey(
        credential,
        endpoint,
        path,
        rule.Name,
        backendHash,
        upstreamHash,
    )

    // 4. Check cache
    if entry, ok := cache.Lookup(cacheKey); ok {
        // Cache HIT
        return entry
    }

    // 5. Execute backend
    response := callBackend(backendDesc)

    // 6. Evaluate local variables
    localVars := evaluateLocalVariables(rule.Variables, response)

    // 7. Evaluate conditions
    outcome := evaluateConditions(rule.Conditions, localVars, response)

    // 8. Render exported variables from winning outcome
    var exportedVars map[string]any
    switch outcome {
    case "pass":
        exportedVars = renderVariables(rule.Responses.Pass.Variables, localVars)
    case "fail":
        exportedVars = renderVariables(rule.Responses.Fail.Variables, localVars)
    case "error":
        exportedVars = renderVariables(rule.Responses.Error.Variables, localVars)
    }

    // 9. Calculate TTL
    ttl := calculateCacheTTL(outcome, rule.Cache, response.Headers)

    // 10. Store in cache (if ttl > 0)
    if ttl > 0 {
        entry := RuleCacheEntry{
            Outcome:   outcome,
            Reason:    reason,
            Exported:  exportedVars,
            ExpiresAt: time.Now().Add(ttl),
        }
        cache.Store(cacheKey, entry)
    }

    return entry
}

func calculateCacheTTL(outcome string, config RuleCacheConfig, headers map[string]string) time.Duration {
    // Error outcomes never cached
    if outcome == "error" {
        return 0
    }

    // Check backend Cache-Control
    if config.FollowCacheControl {
        if cacheControl, ok := headers["cache-control"]; ok {
            directive := parseCacheControl(cacheControl)
            if ttl := directive.GetTTL(); ttl != nil {
                return *ttl
            }
        }
    }

    // Fall back to manual TTL
    return config.GetTTL(outcome)
}
```

## Key Behaviors

### 1. Upstream Refresh with Same Values (Strict Mode)

```yaml
rules:
  - name: lookup-user
    cache:
      ttl:
        pass: "5m"
      strict: true

  - name: fetch-permissions
    cache:
      ttl:
        pass: "10m"
      strict: true
```

**Timeline:**
- T=0s: Both execute, both cached
- T=6m: lookup-user expires → re-executes → returns **same values** → exports `{user_id: 123}`
- fetch-permissions cache key: upstream hash = `sha256("lookup-user.user_id=123")` ← **SAME HASH**
- Result: **CACHE HIT** ✅ (no unnecessary re-execution)

**Key insight:** Hashing variable VALUES (not timestamps) prevents false invalidations

### 2. Upstream Variable Change (Strict Mode)

**Timeline:**
- T=0s: lookup-user exports `{user_id: 123, tier: "premium"}`
- T=6m: lookup-user re-executes → exports `{user_id: 123, tier: "free"}`
- fetch-permissions cache key: upstream hash = `sha256("lookup-user.tier=free|lookup-user.user_id=123")` ← **DIFFERENT**
- Result: **CACHE MISS** → re-executes with new tier ✅

### 3. Backend-Only Dependencies (Loose Mode)

```yaml
rules:
  - name: lookup-session
    responses:
      pass:
        variables:
          user_id: "123"
          metadata: "..."  # Not used downstream

  - name: fetch-profile
    backendApi:
      url: "/users/{{ .rules.lookup-session.variables.user_id }}/profile"
    cache:
      strict: false  # Only user_id matters
```

**Timeline:**
- T=0s: lookup-session exports `{user_id: 123, metadata: "abc"}`
- T=6m: lookup-session re-executes → exports `{user_id: 123, metadata: "xyz"}` (metadata changed)
- fetch-profile cache key: backend hash = `sha256(GET /users/123/profile)` ← **SAME** (no upstream hash)
- Result: **CACHE HIT** ✅ (metadata change doesn't matter)

### 4. Error Outcomes Never Cached

```yaml
cache:
  ttl:
    pass: "5m"
    fail: "30s"
    error: "10m"  # Ignored - always 0
```

Regardless of configuration, error outcomes are never cached (hardcoded 0s TTL).

## Complete Example

```yaml
endpoints:
  api-gateway:
    variables:
      api_base: "https://api.internal"
      tenant: "{{ .request.headers.x-tenant-id }}"
    rules:
      - name: validate-token
      - name: fetch-profile
      - name: fetch-permissions
      - name: check-tier

rules:
  validate-token:
    backendApi:
      url: "{{ .vars.api_base }}/auth/validate"
      # Backend returns: Cache-Control: max-age=300
    cache:
      followCacheControl: true  # Use backend's 300s
      ttl:
        pass: "10m"  # Ignored
        fail: "30s"  # Used (no backend on fail)
      strict: true   # Include all upstream vars

  fetch-profile:
    backendApi:
      url: "{{ .vars.api_base }}/users/{{ .rules.validate-token.variables.user_id }}"
      # Backend returns: Cache-Control: max-age=600, s-maxage=1200
    variables:
      temp_tier: backend.body.tier
      temp_status: backend.body.status
    responses:
      pass:
        variables:
          tier: variables.temp_tier
          status: variables.temp_status
    cache:
      followCacheControl: true  # Uses s-maxage=1200
      ttl:
        pass: "10m"  # Ignored
        fail: "30s"
      strict: true

  fetch-permissions:
    backendApi:
      url: "{{ .vars.api_base }}/users/{{ .rules.validate-token.variables.user_id }}/permissions"
    responses:
      pass:
        variables:
          permissions: backend.body.permissions
    cache:
      followCacheControl: false  # Manual control
      ttl:
        pass: "5m"   # Always used
        fail: "0s"   # Don't cache failures
      strict: false  # Only backend request matters (optimization)

  check-tier:
    # No backend - pure logic
    conditions:
      pass:
        - 'rules["fetch-profile"].variables.tier == "premium"'
    # No cache config - not applicable (no backend)
```

## Migration Path

### From Endpoint-Level Caching

**Old (endpoint-level):**
```yaml
endpoints:
  api-gateway:
    cache:
      resultTTL: "120s"
    rules:
      - name: rule-a
      - name: rule-b
```

**New (per-rule):**
```yaml
rules:
  rule-a:
    cache:
      ttl:
        pass: "120s"
        fail: "30s"
      strict: true

  rule-b:
    cache:
      ttl:
        pass: "120s"
        fail: "30s"
      strict: true
```

### From Old Variable Schema

**Old:**
```yaml
rules:
  lookup-user:
    variables:
      global:
        user_id:
          from: backend.body.userId
    cache:
      passTTL: "5m"
      failTTL: "30s"
```

**New:**
```yaml
rules:
  lookup-user:
    variables:
      temp_id: backend.body.userId
    responses:
      pass:
        variables:
          user_id: variables.temp_id
    cache:
      ttl:
        pass: "5m"
        fail: "30s"
      strict: true
```

## Implementation Checklist

- [ ] Update config types (done)
- [ ] Implement backend descriptor hashing
- [ ] Implement upstream variables hashing
- [ ] Implement Cache-Control header parsing
- [ ] Implement TTL calculation with precedence
- [ ] Move cache operations into rule execution agent
- [ ] Remove endpoint-level cache lookup
- [ ] Remove result caching agent from pipeline
- [ ] Update all example configs
- [ ] Add comprehensive tests
- [ ] Update documentation
