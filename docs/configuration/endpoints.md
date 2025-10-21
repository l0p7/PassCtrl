---
title: Endpoint Configuration Reference
description: Define endpoints, forward request policy, and response defaults while tracking how data moves upstream and downstream.
stage: Stage 2 - Configure Endpoints
permalink: /configuration/endpoints/
---

# Endpoint Configuration Reference

Endpoints glue admission, forward request policy, rule chains, and caching together. This stage explains every server-level and endpoint-level setting, clarifying how each decision changes the upstream request shape and the response that callers receive.

## Reading the Tables

Each section includes a **Behavior** column that explains:

- **Upstream impact** - which headers, query parameters, or payload elements reach backend services.
- **Response impact** - how PassCtrl responds to the caller when the setting is active.

## Server-Level Settings

The `server` block sets deployment-wide behavior. These values load before any endpoint or rule definitions and influence how configuration is merged and monitored.

| Setting | Description | Upstream Impact | Response Impact |
| --- | --- | --- | --- |
| `server.listen.address` | Bind address for the HTTP listener. | Determines which network interface accepts inbound requests. | None. |
| `server.listen.port` | TCP port exposed by the runtime. | Controls target port for trusted proxies and health checks. | None, aside from impact on readiness endpoints. |
| `server.logging.level` | `debug`, `info`, `warn`, `error`. | None. | Higher verbosity surfaces more execution detail to logs, aiding response troubleshooting. |
| `server.logging.format` | `json` or `text`. | None. | Alters log serialization only. |
| `server.logging.correlationHeader` | Header name used to propagate correlation IDs. | Header value is forwarded only when the forward policy allows it. | `/auth` responses echo the header so callers can link outcomes to logs. |
| `server.rules.rulesFolder` | Directory watched for endpoint/rule documents. | New or updated rules change which headers/variables get forwarded upstream. | Reloads flush caches, so responses reflect the latest definitions. |
| `server.rules.rulesFile` | Single configuration file (no hot reload). | Same as rulesFolder but static. | Same as rulesFolder. |
| `server.templates.templatesFolder` | Root for template lookups. | Determines which template files can influence outbound backend requests. | Controls the templates used to render bodies and headers returned to callers. |
| `server.templates.templatesAllowEnv` | Boolean gate for environment variable access inside templates. | Exposing env variables may inject secret material into upstream requests. | Template-rendered responses can include environment-driven content when enabled. |
| `server.templates.templatesAllowedEnv` | Allowlist of environment variables accessible to templates. | Only listed keys can affect backend payloads or headers. | Only listed keys can appear in rendered responses. |
| `server.cache.backend` | Cache backend used for endpoint decisions (`memory` or `redis`). | Determines where cached decisions live; shared backends let replicas reuse results without repeating upstream calls. | Enables reuse of pass/fail metadata for callers. |
| `server.cache.ttlSeconds` | Default TTL applied to cached endpoint results. | Longer TTL reduces upstream traffic when outcomes repeat. | Responses replay cached status, headers, and bodies until expiry. |
| `server.cache.keySalt` | Optional salt appended to cache keys. | Prevents collisions between environments sharing a cache backend. | None. |
| `server.cache.epoch` | Integer appended to cache keys to invalidate globally. | Incrementing forces the runtime to treat cached entries as stale. | Subsequent requests trigger fresh rule evaluation before returning responses. |
| `server.cache.redis.*` | Address, auth, and TLS settings for Redis backends. | Configures how the runtime reaches Redis when `backend: redis`. | None. |

> Example: `examples/server.yaml` applies these defaults and can be used with any of the bundled endpoint/rule configurations.

## Endpoint Definition

Endpoints live under the `endpoints` key. Each endpoint wires together admission policy, forward request policy, rule ordering, default responses, and caching hints.

| Field | Description | Upstream Impact | Response Impact |
| --- | --- | --- | --- |
| `description` | Optional operator-facing summary. | None. | None. |
| `authentication.required` | Whether admission must succeed before rule execution. | If `false`, endpoint may continue with anonymous callers. | Failed admission triggers `responsePolicy.fail`. |
| `authentication.allow` | Accepted authentication mechanisms (`basic`, `bearer`, `header`, `query`). | Determines which credentials can seed rule execution. | Drives the `WWW-Authenticate` hint when admission fails. |
| `authentication.challenge` | Value placed in the `WWW-Authenticate` header on failure. | None. | Advertises authentication expectations to callers. |
| `forwardProxyPolicy.trustedProxyIPs` | CIDR list of trusted proxies. | Determines whether proxy headers are honored. | Admission failures occur when requests originate from untrusted hops. |
| `forwardProxyPolicy.developmentMode` | Loosens strict proxy enforcement for local testing. | Allows partially trusted hops; not for production. | Emits warnings instead of hard failures. |
| `forwardRequestPolicy` | Header/query curation instructions (see below). | Defines which headers and query parameters reach backend services. | Determines which curated values are available to response templates. |
| `rules` | Ordered list of rule references (`- name: fetch-profile`). | Determines rule execution order and upstream call sequence. | Controls which rule outcome is returned to callers. |
| `responsePolicy` | Default pass/fail/error policy used when the decisive rule omits overrides. | None. | Shapes HTTP status, headers, and body returned when rule responses omit overrides. |
| `cache` | Endpoint-level cache directives. | Cache hits can shortcut upstream calls entirely. | Cached responses reuse stored status, headers, and body descriptors. |

> Example: `examples/configs/cached-multi-endpoint.yaml` wires two endpoints with contrasting authentication, forward proxy, and caching settings using this schema.

### Example skeleton

```yaml
endpoints:
  auth-gateway:
    description: "Validate Basic credentials and call the profile API"
    authentication:
      required: true
      allow: ["basic"]
      challenge: "Basic realm=\"Auth\""
    forwardRequestPolicy:
      headers:
        allow: ["authorization"]
      query:
        strip: ["utm_*"]
    forwardProxyPolicy:
      trustedProxyIPs: ["10.0.0.0/8", "192.168.0.0/16"]
    rules:
      - name: validate-credentials
      - name: fetch-profile
    responsePolicy:
      pass:
        status: 200
      fail:
        status: 403
    cache:
      resultTTL: 60s
```

## Forward Request Policy

The forward request policy curates the request snapshot shared with rules and backends. Operators can allow, strip, or synthesize values.

| Field | Description | Upstream Impact | Response Impact |
| --- | --- | --- | --- |
| `headers.allow` | Literal header names (case-insensitive) to forward from the raw request. Supports wildcards (`*`). | Allowed headers are available to backend calls and rule templates. | Templates can access allowed headers when rendering responses. |
| `headers.strip` | Header names to remove after the allow step. | Prevents sensitive headers from leaking upstream even if listed in `allow`. | Removed headers are also hidden from response templates. |
| `headers.custom` | Key/value map rendered as Go templates. | Synthesized headers are injected into backend requests for every rule. | Available to response templates via `forward.headers`. |
| `forwardProxyHeaders` | Boolean. When `true`, sanitized `X-Forwarded-*` and RFC7239 `Forwarded` headers are re-emitted. | Preserves proxy metadata for upstream services. | None, unless response templates refer to forwarded headers. |
| `query.allow` / `query.strip` / `query.custom` | Equivalent controls for query parameters. | Controls which query parameters reach backends. | Queried values remain in the curated request for templates. |

**Evaluation order**: allow → strip → custom. Wildcards apply before strip so you can allow `x-*` and explicitly strip `x-internal`.

## Response Policy Defaults

Endpoint response defaults run when the decisive rule does not provide an override. They mirror the per-rule response blocks but operate at the endpoint level.

| Field | Description | Upstream Impact | Response Impact |
| --- | --- | --- | --- |
| `response.pass.status` | Default HTTP status for successful outcomes. | None. | Sets caller status when rules omit explicit statuses. |
| `response.pass.headers.allow` | Start from decisive rule headers before stripping. | None. | Controls which headers survive to the caller. |
| `response.pass.headers.strip` | Remove specific headers. | None. | Removes headers from the final response. |
| `response.pass.headers.custom` | Synthesized headers rendered from templates. | None. | Adds new headers to caller responses. |
| `response.pass.body` / `bodyFile` | Inline or file-backed templates. | None. | Renders the `/auth` body when rules omit overrides. |
| `response.fail.*`, `response.error.*` | Same structure for deny/error outcomes. | None. | Determines fallback status, headers, and bodies for failure or error responses. |

Response templates have access to:

- `raw` - immutable request snapshot.
- `forward` - curated headers/query parameters.
- `vars` - variables exported by rules.
- `chain` - aggregate rule history (including decisive rule metadata).

## Caching Hints

Endpoint-level caching stores decision metadata (not backend bodies). Cache hits bypass rule execution entirely, so upstream services are not called.

| Field | Description | Upstream Impact | Response Impact |
| --- | --- | --- | --- |
| `cache.resultTTL` | Duration string controlling how long pass/fail outcomes remain cached for this endpoint. When omitted, the runtime falls back to `server.cache.ttlSeconds`. | Larger TTLs reduce traffic to upstream services by reusing decisions. | Callers receive cached status, headers, and body descriptors until the TTL expires. |

Caches invalidate automatically whenever configuration changes touch the endpoint or any of its rules. Error outcomes (`error` block or backend 5xx) are never cached.

> Example: `examples/configs/cached-multi-endpoint.yaml` shows `resultTTL` in action for a cached browser endpoint while disabling caching for an admin endpoint in the same file.
