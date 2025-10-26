---
title: Rule Configuration Reference
description: Author rule chains that control backend requests, condition evaluation, and caller responses.
stage: Stage 3 - Author Rule Chains
permalink: /configuration/rules/
---

# Rule Configuration Reference

Rules are the smallest executable unit in PassCtrl. This guide details each rule field and clarifies how credentials, backend calls, and rendered responses flow through the runtime.

## How to Read This Guide

- Tables highlight how a field influences **Upstream Traffic** (requests sent to backends) and **Caller Responses** (headers, bodies, and statuses returned by PassCtrl).
- Examples showcase common policy shapes and how exported variables move between rules.

## Credential Intake (`auth`)

Use the `auth` block to accept or transform credentials before calling upstream services.

| Directive | Description | Upstream Impact | Caller Response Impact |
| --- | --- | --- | --- |
| `type: basic` | Accept HTTP Basic credentials. | Forwarded unchanged unless `forwardAs` rewrites them. | Admission failures render the endpoint’s fail response. |
| `type: bearer` | Accept Bearer tokens. | Token forwarded as-is or rewritten. | Same as above. |
| `type: header` | Capture credentials from a named header. | Header value injected into upstream requests per `forwardAs`. | Rules can surface the credential in deny messages if templates reference `.auth.input`. |
| `type: query` | Capture credentials from a query parameter. | Query value used to synthesize headers or tokens. | Same as above. |
| `type: none` | Synthesize credentials when none were provided upstream. | Generates static credentials for backend calls. | No direct response impact. |
| `forwardAs.type` (`basic`/`bearer`/`header`) | Transform accepted credentials. | Alters Authorization headers or adds new headers in backend requests. | `forwardAs` does not change caller-facing responses unless response templates read the transformed values. |

Credentials are exposed to templates via:

- `.auth.input` - last credential captured.
- `.auth.forward` - credential forwarded upstream.

## Backend Request Shape (`backendApi`)

The `backendApi` block renders outbound requests for the current rule. The curated request (`forward`), exported variables (`vars`), and prior backend responses are available to the templates.

| Field | Description | Upstream Impact | Caller Response Impact |
| --- | --- | --- | --- |
| `url` | Target endpoint for the backend call. Required when `backendApi` is present. | Determines backend destination. | None directly. |
| `method` | HTTP method (`GET` default). | Defines request semantics. | None. |
| `forwardProxyHeaders` | When `true`, replays sanitized proxy headers. | Preserves client `X-Forwarded-*` metadata. | None. |
| `headers.allow` / `strip` | Allow or remove headers from the curated request. | Controls which incoming headers reach the backend. | Indirect: templates cannot access stripped headers. |
| `headers.custom` | Synthesized headers rendered per request. | Adds new headers to backend calls (e.g., `X-Trace-ID`). | Template data used here can also be reused in response templates. |
| `query.allow` / `strip` / `custom` | Same semantics for query parameters. | Controls which query parameters are sent upstream. | Indirect: curated query parameters shape template context. |
| `body` | Inline Go template rendered per page (when paginating). | Controls backend payload; can include values from `forward`, `vars`, or previous backend responses. | None directly, though backend responses may change rule outcome. |
| `bodyFile` | Path template resolved inside the template sandbox. Renders file contents before sending upstream. | Same as `body`; enables reuse across rules. | None. |
| `acceptedStatuses` | List of HTTP status codes treated as success (default: 2xx). | Controls when pagination or downstream evaluation continues. | Failures trigger rule `fail` or `error` evaluation, influencing caller responses. |
| `pagination` | `type`, `maxPages`, etc. | Drives how many backend pages are fetched before deciding. | Long-running pagination can delay responses; results are captured in rule history for `/explain`. |

Remember: backend bodies are never cached—only decision metadata is stored.

## Rule Conditions

Rule conditions replace implicit status-based decisions with explicit CEL expressions using the rule activation (`raw`, `admission`, `forward`, `backend`, `vars`, `now`).

| Condition Block | Purpose | Upstream Impact | Caller Response Impact |
| --- | --- | --- | --- |
| `conditions.pass[]` | List of CEL expressions that produce a pass outcome when true. | None; evaluates after backend call. | Pass responses reuse endpoint status/body while merging any rule-defined headers. |
| `conditions.fail[]` | Force a fail outcome when any predicate returns true. | None. | Triggers `fail` response block. |
| `conditions.error[]` | Force an error outcome (bypasses cache). | None. | Triggers `error` response block; avoids caching. |

## Conditional Logic Deep Dive

CEL expressions evaluate against a rich activation so you can interrogate admission results, curated request data, backend responses, shared variables, and the current timestamp. The table below summarises the most common keys.

| Key | Description | Example Usage |
| --- | --- | --- |
| `raw` | Immutable snapshot of the inbound request (`method`, `path`, `host`, `headers`, `query`). | `raw.method == "GET"` |
| `admission` | Outcome from the admission agent (`authenticated`, `decision`, `clientIp`, `trustedProxy`). | `admission.decision == "pass"` |
| `forward` | Curated headers and query parameters after the forward request policy runs. | `lookup(forward.headers, "x-session-token") != ""` |
| `backend` | Summary of the latest backend call (`status`, `headers`, `body`, `bodyText`). | `backend.status == 200` |
| `vars` | Variables exported by previous rules (`global`, `rule`, `local`). | `lookup(vars.global, "tier") == "gold"` |
| `now` | UTC timestamp captured at evaluation time. | `now - duration("15m")` |

### Common Patterns

| Scenario | Predicate | Effect |
| --- | --- | --- |
| Enforce backend success | `backend.status in [200, 201]` | Declares pass only when the backend returned a successful code. |
| Short-circuit on inactive flag | `lookup(backend.body, "active") == false` | Marks the rule as `fail`, prompting deny templates or endpoint defaults. |
| Validate exported context | `lookup(vars.global, "subscription_plan") in ["plus", "enterprise"]` | Ensures downstream rules only run for paid subscribers. |
| Guard against missing variables | `lookup(vars.global, "validated_token") == ""` | Forces an error or fail when prerequisite data is absent. |
| Honour admission posture | `admission.authenticated == false` | Provides a final safety net when admission is optional. |

> Example: `examples/configs/backend-token-introspection.yaml` combines backend-derived conditions with exported variables to drive pass/fail outcomes across two rules.

### Tips

- Use the `lookup(map, key)` helper to safely access optional headers, query parameters, or JSON fields without triggering evaluation errors.
- Combine predicates with logical operators (`&&`, `||`) and membership tests (`in`) to express multi-step policies without writing long strings.
- When referencing timestamps, convert to durations explicitly (for example, `now - duration("15m")`).
- Prefer `admission.decision == "pass"` to avoid treating a soft-fail admission as successful.
- Break related tests into multiple entries (`conditions.fail[]`) so `/explain` surfaces the specific predicate that matched.

## Rule-Level Responses

The `responses` block overrides endpoint defaults when the rule is decisive. Pass, fail, and error directives short-circuit the endpoint response policy for headers only—the endpoint still controls status codes and bodies. Multiple pass directives in the chain are cumulative—the caller sees the union of headers emitted by every pass rule that executed. If omitted, the endpoint’s response policy runs.

| Field | Description | Upstream Impact | Caller Response Impact |
| --- | --- | --- | --- |
| `responses.pass.headers.allow` / `strip` | Start from backend/endpoint headers, then strip/apply allow rules. | None. | Controls which headers survive the pass response. |
| `responses.pass.headers.custom` | Template-rendered headers (e.g., `X-Permission-Level`). | None upstream; uses rule context only. | Adds headers to the caller response. |
| `responses.fail.headers.*` / `responses.error.headers.*` | Same header controls for deny/error outcomes. | None. | Lets a decisive rule annotate deny/error responses without changing status/body. |

Templates receive:

- `.backend.body` and `.backend.bodyText` - redacted backend payload (last page).
- `.vars` - global/rule/local variables.
- `.chain` - rule execution history.
- `.endpoint` - endpoint metadata (name, cache hints).

## Rule-Level Caching

Rule-level cache directives control memoization for the rule’s outcome (distinct from endpoint caching).

| Field | Description | Upstream Impact | Caller Response Impact |
| --- | --- | --- | --- |
| `cache.passTTL` / `cache.failTTL` | Duration per outcome (`"0s"` disables caching). | Cache hits reuse the prior outcome without reissuing the backend request. | Responses replay cached status, headers, and body descriptors. |
| `cache.followCacheControl` | Honor backend `Cache-Control` hints when true. | Backend directives (e.g., `max-age`) can shorten or extend the stored decision lifetime. | Caller-visible TTL aligns with backend guidance, reducing stale outcomes. |

Error outcomes (`error` or backend 5xx) are never cached.

> Example: `examples/configs/backend-token-introspection.yaml` caches successful introspection results for 90 seconds while leaving failure outcomes uncached.

## Variable Exports and Scopes

Rules project structured data into named scopes via the `variables` block.

| Scope | Description | Upstream Impact | Caller Response Impact |
| --- | --- | --- | --- |
| `variables.global` | Persist across the endpoint execution (and cached entries). | Affects downstream rules and future cached responses. | Values appear in response templates and `/explain`. |
| `variables.rule` | Scoped to this rule’s execution. | Available to later rules in the same chain during the same request. | Exposed to templating if this rule is decisive. |
| `variables.local` | Cleared after each rule execution (per page when paginating). | Useful for intermediate computations without leaking across rules. | Not visible to responses unless copied into `global` or `rule`. |

When evaluating templates, previously exported rule variables are also available under `rules.<ruleName>.variables.<key>`, allowing later rules and response policies to reference scoped data without promoting it to the global namespace.

Each variable maps a key to a CEL expression via `from`. Example: `variables.global.user_id.from: backend.body.user.id`.

> Example: `examples/configs/cached-multi-endpoint.yaml` exports `tier` and `user_id` so later rules and response templates can reference the curated values.

### Example Rule

```yaml
rules:
  fetch-profile:
    description: "Call identity API and surface user claims"
    auth:
      - type: bearer
        forwardAs:
          type: bearer
          token: "{{ .auth.input.token }}"
    backendApi:
      url: "https://identity.example.com/v1/profile"
      method: GET
      headers:
        allow: ["authorization"]
      acceptedStatuses: [200]
    conditions:
      pass:
        - backend.status == 200
      fail:
        - backend.status == 404
    responses:
      fail:
        status: 403
        body: |
          {"reason": "profile not found"}
    cache:
      passTTL: "2m"
      failTTL: "30s"
      followCacheControl: true
    variables:
      global:
        user_id:
          from: backend.body.id
        entitlements:
          from: backend.body.entitlements
```

This rule forwards the caller’s Bearer token upstream, treats 200 as pass, denies on 404, and exports user metadata for subsequent rules or the endpoint response policy.
