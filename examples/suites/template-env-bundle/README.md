# Template Environment Bundle

This configuration highlights the template sandbox controls that allow a curated
set of environment variables to flow into rendered headers and response bodies.
Use it to validate deny messaging, operator support links, and other
deployment-specific strings without hardcoding them into the configuration.

## Layout

```
server.yaml       # inline endpoints and rules with template allowlist controls
templates/
  deny.json.tmpl  # deny response referencing allowed environment variables
```

## Running the Example

Set the expected environment variables before starting the server:

```bash
export SUPPORT_EMAIL=support@example.com
export UPSTREAM_BASE_URL=https://api.example.com

go run ./cmd --config ./examples/suites/template-env-bundle/server.yaml
```

The rule chain denies unauthenticated requests and renders `SUPPORT_EMAIL` in the
response template. Successful evaluations forward `UPSTREAM_BASE_URL` as a custom
header for downstream services to inspect.
