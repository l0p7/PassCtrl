# PassCtrl with Valkey Cache Example

This example demonstrates running PassCtrl with Valkey (Redis-compatible in-memory data store) as a distributed cache backend for authorization decisions.

## Overview

**Valkey** is a high-performance, open-source key-value store forked from Redis, fully compatible with the Redis protocol. Using Valkey as PassCtrl's cache backend provides:

- **Distributed caching** across multiple PassCtrl instances
- **Persistent caching** that survives PassCtrl restarts
- **High performance** with sub-millisecond latency
- **Memory management** with configurable eviction policies
- **Production-ready** clustering and replication support

## Stack Components

### Services

- **PassCtrl** (`passctrl:latest`) - Forward-auth server on port 8080
- **Valkey** (`valkey/valkey:7.2-alpine`) - Cache backend on port 6379

### Cache Configuration

The example configures Valkey with:
- **Persistence**: AOF (Append-Only File) with `everysec` fsync
- **Memory Limit**: 256MB with LRU eviction policy
- **Health Checks**: Automatic health monitoring
- **Volume**: Persistent storage for cache data

### Rule Examples

The configuration includes multiple rules demonstrating different caching strategies:

1. **`validate-token`** - Standard token validation with moderate caching (5 min pass, 1 min fail)
2. **`check-admin-token`** - Admin token with extended caching (15 min pass, 30 sec fail)
3. **`rate-limited-check`** - Short-lived cache for high-frequency checks (30 sec pass)
4. **`dynamic-cache-token`** - Respects backend `Cache-Control` headers

## Quick Start

### Prerequisites

- Docker and Docker Compose installed
- 512MB+ free RAM
- Ports 8080 (PassCtrl) and 6379 (Valkey) available

### Start the Stack

```bash
cd examples/valkey-cache
docker-compose up -d
```

### Verify Services

Check that both services are healthy:

```bash
# Check PassCtrl health
curl http://localhost:8080/health

# Check Valkey connectivity
docker-compose exec valkey valkey-cli ping
# Expected output: PONG
```

### Test Cached Authentication

```bash
# First request (cache miss - calls backend)
time curl -H "Authorization: Bearer test-token-123" \
  http://localhost:8080/auth?endpoint=api

# Second request (cache hit - served from Valkey)
time curl -H "Authorization: Bearer test-token-123" \
  http://localhost:8080/auth?endpoint=api
```

The second request should be significantly faster as it's served from cache.

## Cache Observability

### Monitor Cache Hits

Watch PassCtrl logs to see cache hit/miss events:

```bash
docker-compose logs -f passctrl | grep -i cache
```

Look for log fields:
- `"from_cache": true` - Decision served from cache
- `"cache_hit": true` - Cache entry found
- `"cache_stored": true` - Decision stored in cache

### Inspect Valkey Directly

Connect to Valkey CLI:

```bash
docker-compose exec valkey valkey-cli
```

Useful commands:
```redis
# View all cache keys
KEYS passctrl:*

# Get cache statistics
INFO stats

# Monitor cache operations in real-time
MONITOR

# Check memory usage
MEMORY STATS

# Inspect a specific cache entry
GET passctrl:1:<key-hash>

# Check cache entry TTL
TTL passctrl:1:<key-hash>

# Clear all cached decisions
FLUSHDB
```

### Cache Key Structure

PassCtrl cache keys follow the pattern:
```
<namespace>:<epoch>:<hash>
```

Example: `passctrl:1:xJ8kL9mN2pQ5rT7vW`

Where:
- **namespace**: `passctrl` (configurable via `PASSCTRL_SERVER__CACHE__NAMESPACE`)
- **epoch**: `1` (increment to invalidate all entries)
- **hash**: SHA-256 hash of (endpoint + credential + salt)

## Configuration Details

### Environment Variables

Key cache-related environment variables:

```yaml
# Cache backend selection
PASSCTRL_SERVER__CACHE__BACKEND=redis

# Valkey connection
PASSCTRL_SERVER__CACHE__REDIS__ADDRESS=valkey:6379
PASSCTRL_SERVER__CACHE__REDIS__DB=0

# Cache behavior
PASSCTRL_SERVER__CACHE__TTLSECONDS=300      # Default TTL (5 minutes)
PASSCTRL_SERVER__CACHE__KEYSALT=...         # Salt for cache key hashing
PASSCTRL_SERVER__CACHE__EPOCH=1             # Increment to bust all cache
```

### Cache TTL Strategy

Different rules use different TTL strategies:

| Rule Type | Pass TTL | Fail TTL | Rationale |
|-----------|----------|----------|-----------|
| Standard token | 5 min | 1 min | Balance freshness vs performance |
| Admin token | 15 min | 30 sec | Admin sessions are stable, failures transient |
| Rate-limited | 30 sec | 10 sec | High-frequency checks, rapid state changes |
| Dynamic | 10 min | 2 min | Backend controls actual TTL via Cache-Control |

### Cache Invalidation

**Global Invalidation** (all endpoints):
1. Increment `PASSCTRL_SERVER__CACHE__EPOCH` in docker-compose.yml
2. Restart PassCtrl: `docker-compose restart passctrl`

**Selective Invalidation** (specific keys):
```bash
docker-compose exec valkey valkey-cli DEL passctrl:1:<key-hash>
```

**Complete Flush** (development only):
```bash
docker-compose exec valkey valkey-cli FLUSHDB
```

## Production Considerations

### Security

**⚠️ Change these values in production:**

1. **Cache Key Salt**: Generate a secure random salt
   ```bash
   # Generate 32-byte random salt
   openssl rand -base64 32
   ```

2. **Valkey Password**: Enable authentication
   ```yaml
   # docker-compose.yml
   valkey:
     command: >
       valkey-server
       --requirepass YOUR_SECURE_PASSWORD

   # Environment
   PASSCTRL_SERVER__CACHE__REDIS__PASSWORD=YOUR_SECURE_PASSWORD
   ```

3. **TLS Encryption**: Enable TLS for Redis connections
   ```yaml
   PASSCTRL_SERVER__CACHE__REDIS__TLS__ENABLED=true
   PASSCTRL_SERVER__CACHE__REDIS__TLS__CAFILE=/path/to/ca.pem
   ```

### High Availability

For production, consider:

1. **Valkey Sentinel** for automatic failover
2. **Valkey Cluster** for horizontal scaling
3. **Multiple PassCtrl instances** behind a load balancer
4. **Persistent volumes** for cache data durability

Example Sentinel setup:
```yaml
services:
  valkey-primary:
    image: valkey/valkey:7.2-alpine
    # ... primary config

  valkey-replica:
    image: valkey/valkey:7.2-alpine
    command: valkey-server --replicaof valkey-primary 6379
    # ... replica config

  sentinel:
    image: valkey/valkey:7.2-alpine
    command: valkey-sentinel /etc/sentinel.conf
    # ... sentinel config
```

### Monitoring

Integrate with observability stack:

1. **Prometheus**: Valkey exporter for metrics
   ```yaml
   valkey-exporter:
     image: oliver006/redis_exporter
     environment:
       REDIS_ADDR: valkey:6379
   ```

2. **Grafana**: Import Valkey dashboard
3. **PassCtrl Metrics**: `/metrics` endpoint for cache hit rates

### Memory Management

Tune Valkey memory limits based on load:

```bash
# Calculate required memory
# Estimated cache entry size: ~1-2 KB
# Expected cache entries: N
# Required memory: N * 2KB * 1.2 (overhead)

# Example: 50,000 entries
# 50000 * 2KB * 1.2 = ~120MB + buffer = 256MB
```

Adjust `maxmemory` in docker-compose.yml accordingly.

## Troubleshooting

### Cache Not Working

1. **Check Valkey connectivity:**
   ```bash
   docker-compose exec passctrl wget -O- http://valkey:6379
   ```

2. **Verify cache backend config:**
   ```bash
   docker-compose exec passctrl env | grep CACHE
   ```

3. **Check PassCtrl logs:**
   ```bash
   docker-compose logs passctrl | grep -i "cache\|redis\|valkey"
   ```

### High Cache Miss Rate

Possible causes:
- Cache TTL too short
- High cardinality in cache keys (e.g., unique tokens per request)
- Frequent cache invalidations
- Memory eviction due to maxmemory limit

**Debug:**
```bash
# Check eviction stats
docker-compose exec valkey valkey-cli INFO stats | grep evicted
```

### Performance Issues

1. **Enable Valkey slow log:**
   ```redis
   CONFIG SET slowlog-log-slower-than 10000  # 10ms
   SLOWLOG GET 10
   ```

2. **Monitor PassCtrl latency:**
   ```bash
   docker-compose logs passctrl | jq '.latency_ms'
   ```

3. **Check network latency:**
   ```bash
   docker-compose exec passctrl ping -c 10 valkey
   ```

## Advanced Topics

### Multiple PassCtrl Instances

Scale PassCtrl horizontally with shared Valkey cache:

```yaml
services:
  passctrl-1:
    # ... passctrl config
    container_name: passctrl-1

  passctrl-2:
    # ... passctrl config
    container_name: passctrl-2

  valkey:
    # ... shared cache

  nginx:
    # Load balancer
```

All instances share the same cache, ensuring consistent decisions.

### Cache Warming

Pre-populate cache on startup:

```bash
# Script to warm common tokens
for token in token1 token2 token3; do
  curl -H "Authorization: Bearer $token" \
    http://localhost:8080/auth?endpoint=api
done
```

### Cache Migration

Migrate from memory to Valkey without downtime:

1. Deploy Valkey alongside existing PassCtrl (memory cache)
2. Configure new PassCtrl instances with Valkey
3. Route traffic through new instances
4. Decommission old instances

## References

- [Valkey Documentation](https://valkey.io/docs/)
- [PassCtrl Cache Configuration](/design/config-structure.md#cache-configuration)
- [Redis Protocol Compatibility](https://valkey.io/topics/protocol/)
- [Cache Decision Model](/design/decision-model.md#stage-5-result-caching)

## License

This example configuration is provided as-is under the same license as PassCtrl.
