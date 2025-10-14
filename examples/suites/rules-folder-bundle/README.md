# Rules Folder Bundle

This bundle demonstrates how to split endpoint and rule definitions across a
watched `rulesFolder`. The server configuration only declares bootstrap knobs
and points at the directory under `rules/`, letting the loader merge YAML files
from the tree while keeping hot-reload semantics.

## Layout

```
server.yaml                      # server bootstrap configuration
rules/
  endpoints.yaml                 # endpoint catalog loaded from the folder
  rules/
    session.yaml                 # session lookup + entitlement enforcement rules
templates/
  deny.json.tmpl                 # shared deny response referenced by the endpoint
README.md                        # helper notes for operators (this file)
```

The rule files focus on two cooperating rules:

1. `lookup-session` retrieves metadata from a backing session service and caches
   the positive/negative results separately.
2. `require-entitlement` checks the exported entitlement flag and publishes the
   user identifier globally so response templates and downstream endpoints can
   reuse it.

## Running the Example

From the repository root:

```bash
go run ./cmd --config ./examples/suites/rules-folder-bundle/server.yaml
```

Because the configuration references the repository-relative `rules/` and
`templates/` folders, running the command from the repository root ensures the
loader can resolve every file.
