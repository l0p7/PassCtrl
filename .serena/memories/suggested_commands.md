# Suggested Commands for PassCtrl Development

## Testing & Quality

### Run all unit tests
```bash
go test ./...
```

### Run linter with repository-local caches
This avoids permission issues by using local cache directories:
```bash
mkdir -p .gocache .gomodcache .golangci-lint
GOCACHE=$(pwd)/.gocache GOMODCACHE=$(pwd)/.gomodcache GOLANGCI_LINT_CACHE=$(pwd)/.golangci-lint golangci-lint run ./...
```

### Generate mocks (after modifying interfaces)
```bash
mockery --config .mockery.yml
```

## Development

### Build the binary
```bash
go build -o passctrl ./cmd
```

### Run the server
Requires a configuration file:
```bash
./passctrl -config ./examples/basic/config.yaml
```

### View available flags
```bash
./passctrl -h
```

## Dependency Management

### Update dependencies
```bash
go mod tidy
```

### Verify module integrity
```bash
go mod verify
```

## Issue Tracking (Beads)

### List issues
```bash
/beads:list
/beads:list --status=open
/beads:list --status=in_progress
```

### Find ready-to-work tasks
```bash
/beads:ready
```

### Show issue details
```bash
/beads:show <issue-id>
```

### Create a new issue
```bash
/beads:create "Issue title" task 2
```

### Update issue status
```bash
/beads:update <issue-id> --status=in_progress
```

### Close completed issue
```bash
/beads:close <issue-id> "Completed: description"
```

## System Utility Commands (Linux)

Standard Linux commands available:
- `git` - Version control
- `ls` - List directory contents
- `cd` - Change directory
- `grep` - Search text
- `find` - Find files
- `cat` - View file contents
- `mkdir` - Create directories
