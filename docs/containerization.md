# Containerization

This project ships a first-class container image with runtime options that align to
common homelab and server environments. The image supports three key knobs via
environment variables:

- `TZ` – Timezone for the container (e.g., `Europe/Berlin`).
- `PUID` – UID for the runtime user inside the container.
- `PGID` – GID for the runtime group inside the container.

The entrypoint adjusts the container user and group IDs on startup and then drops
privileges accordingly. This makes file ownership of bind-mounted volumes predictable
regardless of the host’s user IDs.

## Image

A multi-stage `Dockerfile` builds the `passctrl` binary and prepares a minimal Debian
runtime with `tzdata` and `gosu` for privilege dropping. The default HTTP bind is
`0.0.0.0:8080` and port `8080` is exposed.

Runtime directories intended for bind-mounts:

- `/config` – optional server config file (e.g., `/config/server.yaml`).
- `/rules` – optional rules folder if configured.
- `/templates` – optional templates folder.

## Configuration in Containers

PassCtrl’s configuration still observes the documented precedence `env > file > default`.
Inside containers you can either:

- Provide a config file and start with `--config /config/server.yaml` (the entrypoint
  will do this automatically if that file exists), or
- Override specific fields via environment variables using the prefix `PASSCTRL_`.

Examples:

```bash
# Override rules and templates folders to match bind mounts
PASSCTRL_SERVER__RULES__RULESFOLDER=/rules
PASSCTRL_SERVER__TEMPLATES__TEMPLATESFOLDER=/templates
# Change port
PASSCTRL_SERVER__LISTEN__PORT=9090
```

## Docker Compose

The following example uses `TZ`, `PUID`, and `PGID` to align the container’s runtime
user and group to the host, and mounts configuration directories into the container.

```yaml
services:
  passctrl:
    build: .
    image: passctrl:local
    container_name: passctrl
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      - TZ=UTC
      - PUID=1000
      - PGID=1000
      - PASSCTRL_SERVER__RULES__RULESFOLDER=/rules
      - PASSCTRL_SERVER__TEMPLATES__TEMPLATESFOLDER=/templates
      # optionally load config file if mounted at /config/server.yaml
      # or override other fields with PASSCTRL_* env vars
    volumes:
      - ./config:/config:ro
      - ./rules:/rules:ro
      - ./templates:/templates:ro
```

Notes:

- Do not set the Compose `user:` field when using `PUID/PGID`. The entrypoint needs
  to run as root briefly to reshape the in-container user/group before dropping
  privileges with `gosu`.
- If you prefer Compose’s `user:` control instead of `PUID/PGID`, remove the `PUID`
  and `PGID` environment variables, add `user: "1000:1000"`, and keep `TZ` as an env
  var. In that mode the image will run directly as the specified user and skip the
  entrypoint’s UID/GID adjustments.

## Local Build & Run

```bash
# Build image
docker build -t passctrl:local .

# Run with ephemeral config only (env overrides)
docker run --rm -p 8080:8080 \
  -e TZ=UTC -e PUID=1000 -e PGID=1000 \
  -e PASSCTRL_SERVER__RULES__RULESFOLDER=/rules \
  -e PASSCTRL_SERVER__TEMPLATES__TEMPLATESFOLDER=/templates \
  passctrl:local

# Or run with a mounted config file
mkdir -p ./config ./rules ./templates
cp examples/server.yaml ./config/server.yaml 2>/dev/null || true

docker run --rm -p 8080:8080 \
  -e TZ=UTC -e PUID=1000 -e PGID=1000 \
  -v $(pwd)/config:/config:ro \
  -v $(pwd)/rules:/rules:ro \
  -v $(pwd)/templates:/templates:ro \
  passctrl:local
```

## Operational Behavior

- Timezone: `TZ` is applied by linking `/etc/localtime` and writing `/etc/timezone` if
  the zone exists in `tzdata`.
- UID/GID: the entrypoint ensures a user `app` exists with the requested `PUID:PGID`,
  chowns `/config`, `/rules`, and `/templates` if present, and execs `passctrl` via
  `gosu` so the process runs without root privileges.
- Logs: continue to use `log/slog` as configured; no special container logging logic
  is added.

