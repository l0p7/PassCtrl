# Example Configurations

The files in this folder illustrate different endpoint and rule-chain topologies.
They can be mounted directly into a container image or used as a starting point
when composing your own configuration bundle.

## Single-File Configurations (`configs/`)

- `server.yaml` – Minimal server bootstrap settings shared by all scenarios.
- `configs/basic-auth-gateway.yaml` – Single endpoint that validates Basic
  credentials and returns a templated deny response when authentication fails.
- `configs/backend-token-introspection.yaml` – Demonstrates a rule chain that
  calls an upstream API to validate bearer tokens and forwards curated headers
  downstream.
- `configs/cached-multi-endpoint.yaml` – Multi-endpoint bundle showing how to set
  a default endpoint, mix cached/uncached rule chains, and surface health
  metadata via `/explain`.

## Configuration Suites (`suites/`)

The suites group together server configs, templates, and supporting rule files to
mirror common deployment layouts:

- `suites/rules-folder-bundle` – Uses `server.rules.rulesFolder` to load
  endpoints and rules from a directory tree, highlighting hot-reload semantics
  and shared deny templates.
- `suites/redis-cache-cluster` – Enables the Redis/Valkey cache backend, shows
  how to propagate rule-level cache hints, and renders cached pass responses via
  templates.
- `suites/template-env-bundle` – Opts into the template environment allowlist so
  deny responses and custom headers can render deployment-specific values such as
  support contacts or upstream base URLs.

Each configuration embeds inline documentation comments to highlight which agent
consumes a given setting. Copy the relevant sections into your deployment bundle
and adjust the rule names to match your rule library.
