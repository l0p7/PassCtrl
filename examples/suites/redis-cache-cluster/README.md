# Redis Cache Cluster Bundle

This scenario configures PassCtrl to use the Redis/Valkey cache backend while
highlighting rule-level cache hints and endpoint result memoization. Use it to
exercise cache invalidation hooks or validate TLS/credential wiring for hosted
Redis services.

## Layout

```
server.yaml       # enables the Redis cache backend and points at rules.yaml
rules.yaml        # inline endpoints and rules that depend on cached decisions
templates/
  pass.json.tmpl  # pass response rendered when the rule chain succeeds
```

## Running the Example

Ensure a Redis instance is reachable from the runtime (for local testing `docker
run --rm -p 6379:6379 redis:7` works well), then execute:

```bash
go run ./cmd --config ./examples/suites/redis-cache-cluster/server.yaml
```

Adjust the `server.cache.redis` block to match your environment—set `tls.enabled`
for managed Redis offerings and populate `username`/`password` when ACLs are in
use.
