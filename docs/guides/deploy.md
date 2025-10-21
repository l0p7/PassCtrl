---
title: Deploy the PassCtrl Runtime
description: Install PassCtrl from source, run the packaged binary, or operate the Docker image with environment overrides.
stage: Stage 1 - Deploy the Runtime
permalink: /guides/deploy/
---

# Deploy the PassCtrl Runtime

PassCtrl runs as a single Go binary with optional container packaging. This guide walks through local builds, production-ready binaries, and the first-run checklist for the Docker image.

## What You’ll Learn

- Baseline prerequisites and environment variables that shape configuration precedence.
- How to build and run the binary directly.
- How to run the maintained Docker image with bind-mounted configuration.
- Tips for supervising the process and capturing structured logs.

## Prerequisites

| Requirement | Purpose | Upstream / Response Impact |
| --- | --- | --- |
| Go 1.25+ | Builds the CLI and integration tests. | Ensures generated binaries embed the latest rule and response handling code paths. |
| Access to configuration files (`.yaml`, `.toml`, `.hujson`) | Provides defaults for endpoints, rules, and responses. | Files seed the canonical configuration snapshot that downstream agents read; changes trigger cache invalidation. |
| Environment overrides (`PASSCTRL_…`) | Override configuration in containers or managed hosts. | Highest precedence; immediately shapes what headers and responses the runtime emits. |
| Optional: Docker / Compose | Runs PassCtrl as an OCI container. | Container entrypoint exposes the same configuration behavior while isolating filesystem mounts for rules/templates. |

## Build and Run from Source

Clone the repository and download module dependencies:

```bash
git clone https://github.com/l0p7/PassCtrl.git
cd PassCtrl
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache go mod download
```

Build a local binary:

```bash
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache go build -o passctrl ./cmd
```

Run with an explicit configuration file:

```bash
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache \
  ./passctrl --config ./config/server.yaml
```

Alternatively, run straight from source during development:

```bash
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache \
  go run ./cmd --config ./config/server.yaml
```

### Observability

- Structured logs default to JSON. Tune via `server.logging.format`.
- Set `server.logging.correlationHeader` so `/auth` responses echo the same identifier in headers and payload, letting upstream services correlate requests and decisions.
- Increase verbosity (`debug`) to capture inbound request snapshots and rule execution summaries.

## Operate the Published Docker Image

PassCtrl ships a multi-stage container image aligned with homelab and production expectations.

### Runtime Environment Variables

| Variable | Meaning | Upstream / Response Impact |
| --- | --- | --- |
| `TZ` | Container timezone. | Affects timestamps in logs; no direct request impact. |
| `PUID` / `PGID` | In-container user and group IDs. | Ensures mounted config/rules/templates remain readable; no direct traffic impact. |
| `PASSCTRL_…` | Declarative overrides for server settings. | Highest precedence; modifies headers, cache behavior, or response policies immediately. |

### Example `docker-compose.yml`

```yaml
services:
  passctrl:
    image: ghcr.io/l0p7/passctrl:latest
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      - TZ=UTC
      - PUID=1000
      - PGID=1000
      - PASSCTRL_SERVER__RULES__RULESFOLDER=/rules
      - PASSCTRL_SERVER__TEMPLATES__TEMPLATESFOLDER=/templates
    volumes:
      - ./config:/config:ro
      - ./rules:/rules:ro
      - ./templates:/templates:ro
```

> Example: `examples/docker-compose.yml` mirrors this configuration so you can `docker compose up` directly from the repository.

Notes:

- Do not set the Compose `user:` field when using `PUID/PGID`; the entrypoint briefly runs as root to remap IDs before dropping privileges with `gosu`.
- When relying solely on environment overrides, the entrypoint auto-starts without a `/config/server.yaml` file. Mount one if you prefer file-based defaults.
- TCP port `8080` is exposed by default; override via `PASSCTRL_SERVER__LISTEN__PORT`.

### Local Docker Commands

```bash
docker build -t passctrl:local .

docker run --rm -p 8080:8080 \
  -e TZ=UTC -e PUID=1000 -e PGID=1000 \
  -e PASSCTRL_SERVER__RULES__RULESFOLDER=/rules \
  -e PASSCTRL_SERVER__TEMPLATES__TEMPLATESFOLDER=/templates \
  passctrl:local
```

## Configuration Entry Points

PassCtrl merges configuration layers in precedence order: **environment > files > defaults**. The loader records every contributing rules document in `cfg.RuleSources` and surfaces duplicates or missing dependencies in `cfg.SkippedDefinitions`.

Examples:

```bash
export PASSCTRL_SERVER__LISTEN__PORT=9090
export PASSCTRL_SERVER__LOGGING__LEVEL=debug
export PASSCTRL_SERVER__TEMPLATES__TEMPLATESALLOWENV=true
export PASSCTRL_SERVER__TEMPLATES__TEMPLATESALLOWEDENV=CLIENT_ID,CLIENT_SECRET
```

Effects:

- `listen.port` changes the listener port and the upstream target for trusted proxies.
- `logging.level=debug` increases request/response introspection without changing payloads.
- `templates.templatesAllowEnv=true` and `templates.templatesAllowedEnv` expose whitelisted environment variables to template rendering, which can influence outbound headers or response templates.

## Operational Checklist

- **Health probes**: Expose `/healthz` (aggregate) or `/<endpoint>/healthz` (per endpoint) to readiness monitors.
- **Explain endpoint**: Use `/explain` to inspect `SkippedDefinitions`, cache health, and rendered variable scopes when debugging.
- **Log shipping**: Forward JSON logs to your observability stack; each entry includes `component`, `agent`, `status`, `outcome`, and `correlation_id`.
- **Hot reload**: When `rules.rulesFolder` is configured, publish updates atomically (write to temporary file then move) to avoid transient parse errors. Every successful reload invalidates caches so upstream services receive fresh rule behavior.
