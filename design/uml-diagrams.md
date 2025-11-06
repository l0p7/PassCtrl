# UML Decision Flow Diagrams

This document captures high-level UML activity diagrams that illustrate the v2 decision flows for endpoints, rule chains, and individual rules. Each diagram highlights when key variables or cached objects are set so implementers can validate the control points against the prose design docs.

## Endpoint Decision Flow

```mermaid
flowchart TD
    A[HTTP Request Arrives] --> B[Extract rawState]
    B -->|sets endpointContext.rawState.*| C[Validate Forward Proxy Policy]
    C --> D{Trusted proxy?}
    D -- No --> E[Reject with 403]
    D -- Yes --> F[Authenticate Request]
    F -->|sets endpointContext.auth.status and .input.*| G{Auth satisfied?}
    G -- No --> H[Render responsePolicy.fail]
    G -- Yes --> I[Apply forwardRequestPolicy]
    I -->|sanitizes proxy headers when enabled| J[Enter Rule Chain]
    J --> K[Evaluate Rules Sequentially]
    K --> L{All rules pass?}
    L -- Yes --> M[responsePolicy.pass]
    L -- No --> N{Failure or Error?}
    N -- Failure --> O[responsePolicy.fail]
    N -- Error --> P[responsePolicy.error]
    M --> R[Render pass response (body/bodyFile if configured)]
    O --> V[Cache fail result per TTL if configured]
    P --> W[Bypass caching (5xx not cached)]
    M --> X[Cache pass result per TTL if configured]
    R --> Z[Send endpoint-defined response]
```

## Rule Chain Evaluation Flow

```mermaid
flowchart LR
    A[Start Rule Chain] --> B[Initialize chainContext]
    B -->|sets chainContext.variables.global from previous cache (if any)| C[Iterate rules by order]
    C --> D{Rule executes}
    D --> E[Capture rule outcome]
    E -->|append to chainContext.history| F{Pass?}
    F -- Yes --> G[Merge exported variables]
    F -- No --> H{Fail or Error?}
    H -- Fail --> I[Stop chain, mark failure]
    H -- Error --> J[Stop chain, mark error]
    G --> K{More rules?}
    K -- Yes --> C
    K -- No --> L[Mark chain success]
    I --> M[Expose fail variables to responsePolicy]
    J --> N[Expose error variables to responsePolicy]
    L --> O[Expose pass variables to responsePolicy]
```

## Individual Rule Execution Flow

```mermaid
flowchart TD
    A[Enter Rule] --> B[Load ruleContext]
    B -->|initializes ruleContext.variables.local and ruleContext.variables.rule| C[Auth Directives]
    C -->|populates ruleContext.auth.input.*| D{Credentials accepted?}
    D -- No --> E[Return fail result]
    D -- Yes --> F[Transform Credentials]
    F -->|sets ruleContext.auth.forward.*| G[Prepare Backend Request]
    G -->|render headers/query/body templates| H[Invoke Backend API]
    H -->|store ruleContext.backend.response.status/headers/body| I{Response status accepted?}
    I -- No --> J[Apply conditions.fail/error]
    I -- Yes --> K[Apply conditions.pass overrides]
    J --> L{Error condition met?}
    L -- Yes --> M[Return error result]
    L -- No --> N[Return fail result]
    K --> O[Derive pass variables]
    N --> P[Derive fail variables]
    M --> Q[Derive error variables]
    O -->|export as configured scopes| R[Assemble pass response]
    P -->|export scoped variables| S[Assemble fail response]
    Q -->|export scoped variables| T[Assemble error response]
    R --> U[Return pass result]
    S --> V[Return fail result]
    T --> W[Return error result]
```

### Variable Scope Notes

* `global` variables set in **Derive pass/fail/error variables** override earlier values for subsequent rules.
* `rule` variables are written to `chainContext.history[ruleName].variables` during response assembly.
* `local` variables remain inside `ruleContext` and are discarded after the rule returns.

