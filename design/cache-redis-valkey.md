# Redis/Valkey Cache Interface Design

## Overview
PassCtrl currently keeps decision metadata in an in-memory map with TTL controls. This document outlines how to extend that cache so production deployments can offload storage to Redis or Valkey while preserving the existing agent contracts.

## Goals
- Provide a pluggable cache interface that supports the current in-memory implementation and a Redis/Valkey backend without changing agent call sites.
- Ensure cached entries remain metadata-only (status, headers, expiry) and avoid persisting request bodies or credentials.
- Allow operators to invalidate cache entries across the cluster when rules change.
- Maintain observability parity (structured logs, metrics, health) regardless of backend choice.

## Non-Goals
- Implement cache population strategies beyond the existing result-caching agent behavior.
- Add binary protocol proxies or Redis clustering management. Deployments are expected to operate Redis/Valkey separately.
- Provide a generic key/value abstraction outside of decision caching.

## Architecture
### Interface
Introduce a `DecisionCache` interface in `internal/runtime/cache` with methods:

```go
Lookup(ctx context.Context, key string) (Entry, bool, error)
Store(ctx context.Context, key string, entry Entry) error
DeletePrefix(ctx context.Context, prefix string) error // used for epoch-style invalidation
Size(ctx context.Context) (int64, error)               // optional, best-effort
Close(ctx context.Context) error
```

- `Entry` mirrors the current cached payload (status, message, headers, expiry timestamp).
- `DeletePrefix` supports coarse invalidation when configuration epochs change; implementations may no-op if unsupported.
- `Size` returns approximate entry counts (Redis uses `DBSIZE` or scans, memory backend returns `len(map)`).
- Backends that require additional coordination on reload can optionally implement `ReloadInvalidator`, which receives the cache namespace, epoch, and prefix so clustered stores can clear state beyond simple prefix deletion.

### Implementations
1. **Memory cache** (`internal/runtime/cache/memory.go`)
   - Wrap the existing map+mutex+TTL logic behind the interface.
   - Preserve current behavior for unit tests and local development.
   - Add optional metrics (hit/miss counters, eviction counts) exposed via the shared instrumentation surface once available.

2. **Redis/Valkey cache** (`internal/runtime/cache/redis.go`)
   - Use `github.com/valkey-io/valkey-go` for connectivity; the client speaks RESP3 and remains compatible with Redis deployments.
   - Store entries as JSON blobs (`Entry` marshaled via `encoding/json`) with `SET` + `PX` for TTL management.
   - Key format: `passctrl:decision:v1:<epoch>:<hash>`, where `<hash>` is `base64url(sha256(salt + rawKey))` to prevent leaking identifiers.
   - Support `DeletePrefix` by bumping the `epoch` portion via configuration rather than scanning keys.
   - Honor context deadlines/timeouts on each command. On connectivity failures, return a cache-miss style error so the agent can continue processing without caching.

### Configuration
Extend `server.cache` in the configuration schema:

```yaml
server:
  cache:
    backend: memory # or "redis"
    ttlSeconds: 30
    keySalt: ""    # optional secret used for key hashing
    epoch: 1        # bump to invalidate cluster-wide cache entries
    redis:
      address: localhost:6379
      username: ""
      password: ""
      db: 0
      tls:
        enabled: false
        caFile: ""
```

- Env overrides maintain precedence (`env > file > default`).
- When `backend` is `redis`, require `address` and log a warning if TLS is disabled in production environments.
- Expose `epoch` so the rules loader can increment it on successful reload and flush stale entries.

### Pipeline Integration
- Update the runtime pipeline constructor to accept a `DecisionCache`.
- During startup, instantiate the backend indicated by configuration; fallback to memory with a warning if Redis initialization fails.
- Propagate the cache instance to the result-caching agent and any health endpoints that report cache status.
- Ensure graceful shutdown calls `Close` with context deadlines.

## Observability
- Add slog fields `cache_backend`, `cache_hit`, `cache_key`, and `latency_ms` around lookup/store operations.
- Emit Prometheus metrics (`passctrl_cache_operations_total` and `passctrl_cache_operation_duration_seconds`) capturing lookup/store hits, misses, and errors with latency histograms.
- Surface cache health in `/healthz` (alias `/health`), including backend type and last error timestamp.

## Testing Strategy
- Reuse existing unit tests against the memory backend to guarantee backward compatibility.
- Add table-driven tests that exercise both memory and Redis implementations using `github.com/alicebob/miniredis/v2` as an in-memory server.
- Include integration tests for epoch invalidation: set epoch=1, store entry, bump epoch to 2, ensure subsequent lookups miss.
- Run race detector (`go test -race ./...`) to cover concurrent access patterns.

## Operational Considerations
- Recommend setting `keySalt` from a secret so cache keys cannot be correlated to specific tokens or endpoints if the store leaks.
- Encourage Redis deployment with persistence disabled (cache-only) and eviction policy `allkeys-lru` or similar.
- Document backup expectations: cache is authoritative for performance only; data loss should not block authentication flows.

## Open Questions
- Should cache statistics be exposed via an admin endpoint or limited to metrics/logging?
- Do we need fine-grained invalidation (e.g., per-endpoint keys) beyond epoch bumps?
- How should we handle multi-region deployments where cache latency may exceed TTL benefits?

## Next Steps
1. Validate additional cache backends against the `ReloadInvalidator` hook to ensure cluster-wide invalidation works consistently.
2. Extend the configuration loader and validation logic to support the new `server.cache` block.
3. Implement Redis backend and corresponding tests.
4. Update documentation (`docs/configuration.md`, `design/system-agents.md`) to reflect the new cache options.
5. Add operational guidance for running Redis/Valkey to the public docs once the backend is battle-tested.
