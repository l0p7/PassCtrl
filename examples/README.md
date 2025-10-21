# Example Configurations

The examples mirror the staged documentation so you can jump from a concept to a runnable configuration. Copy the files into your deployment bundle or mount them directly inside the PassCtrl container while experimenting locally.

## Quick Map to the Docs

| Documentation Stage | Example | Highlights |
| --- | --- | --- |
| Stage 1 – Deploy the Runtime | [`docker-compose.yml`](./docker-compose.yml) | Container wiring for `TZ`, `PUID/PGID`, and mounted config directories. |
| Stage 2 – Configure Endpoints | [`configs/cached-multi-endpoint.yaml`](./configs/cached-multi-endpoint.yaml) | Multiple endpoints, trusted proxy policy, curated headers/query parameters, and endpoint-level caching. |
| Stage 3 – Author Rule Chains | [`configs/backend-token-introspection.yaml`](./configs/backend-token-introspection.yaml) | Rule-level caching, CEL conditions, variable exports, and response overrides tied to upstream payloads. |
| Stage 4 – Follow the Flow | [`suites/rules-folder-bundle`](./suites/rules-folder-bundle/) | Hot-reloaded rules folder with variables feeding downstream decisions, ideal for walking through `/explain` output. |

## Single-File Configurations (`configs/`)

- [`server.yaml`](./server.yaml) – Minimal server bootstrap shared by all scenarios.
- [`configs/basic-auth-gateway.yaml`](./configs/basic-auth-gateway.yaml) – Single endpoint protecting `/auth` with HTTP Basic.
- [`configs/backend-token-introspection.yaml`](./configs/backend-token-introspection.yaml) – Bearer token introspection with rule-level caching and conditional logic.
- [`configs/cached-multi-endpoint.yaml`](./configs/cached-multi-endpoint.yaml) – Mixed cached/uncached endpoints showing forward request policy and response defaults.

## Configuration Suites (`suites/`)

- [`suites/rules-folder-bundle`](./suites/rules-folder-bundle/) – Uses `server.rules.rulesFolder` for hot reload and demonstrates variable exports feeding subsequent rules.
- [`suites/redis-cache-cluster`](./suites/redis-cache-cluster/) – Enables the Redis cache backend and shows rule-level TTLs plus response templating.
- [`suites/template-env-bundle`](./suites/template-env-bundle/) – Opts into the template environment allowlist to render deny responses with environment-provided data.
- [`suites/backend-body-templates`](./suites/backend-body-templates/) – Demonstrates inline and file-backed backend request bodies rendered inside the template sandbox.

Every configuration includes inline comments that call out which runtime agent consumes each setting. Update rule names and template paths to match your deployment, then point the CLI at the relevant `server.yaml` with `--config`.
