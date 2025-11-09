# Docker Secrets Example

This example demonstrates how to use Docker secrets (or Kubernetes Secrets) with PassCtrl.

## Overview

PassCtrl supports loading secrets from `/run/secrets/` at startup, which is the standard location for:
- Docker Swarm secrets
- Docker Compose secrets (file-based)
- Kubernetes Secrets mounted as volumes

Secrets are:
- Loaded at startup with fail-fast validation (missing secrets cause startup failure)
- Exposed as `variables.secrets.*` in both CEL expressions and Go templates
- Automatically trimmed of trailing newlines (Docker adds these)
- Configured using null-copy semantics for security

## Quick Start

### 1. Create Secret Files

```bash
mkdir -p secrets
echo "supersecret_db_password_123" > secrets/db_password.txt
echo "sk-api-key-1234567890abcdef" > secrets/api_key.txt
echo "webhook_secret_shared_key_xyz" > secrets/webhook_secret.txt
```

### 2. Start the Stack

```bash
docker-compose up
```

### 3. Test the Endpoints

**Test API endpoint with backend authentication:**
```bash
curl -H "Authorization: Bearer test-token" \
     http://localhost:8080/api/auth
```

**Test webhook signature verification:**
```bash
curl -H "X-Webhook-Signature: test-signature" \
     http://localhost:8080/webhook/auth
```

## Configuration Patterns

### Null-Copy Semantics

In `server.variables.secrets`:
- `db_password: null` → reads `/run/secrets/db_password` and exposes as `variables.secrets.db_password`
- `api_key: "custom_file"` → reads `/run/secrets/custom_file` and exposes as `variables.secrets.api_key`

### Using Secrets in CEL

```yaml
conditions:
  pass:
    # Access secret in CEL expression
    - 'variables.secrets.api_key == "expected-key"'
```

### Using Secrets in Go Templates

```yaml
headers:
  templates:
    # Use secret in backend request header
    X-Api-Key: "{{ .variables.secrets.api_key }}"
```

### Security Best Practices

⚠️ **Never expose secrets in responses to clients:**

```yaml
# ❌ BAD: Exposes secret to client
responsePolicy:
  pass:
    headers:
      custom:
        X-Secret: "{{ .variables.secrets.api_key }}"  # INSECURE!

# ✅ GOOD: Use secret only for backend requests
backendApi:
  headers:
    templates:
      X-Api-Key: "{{ .variables.secrets.api_key }}"  # Secure
```

## Kubernetes Deployment

For Kubernetes, create Secret objects and mount them to `/run/secrets/`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: passctrl-secrets
type: Opaque
stringData:
  db_password: "supersecret_db_password_123"
  api_key: "sk-api-key-1234567890abcdef"
  webhook_secret: "webhook_secret_shared_key_xyz"
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: passctrl
spec:
  template:
    spec:
      containers:
      - name: passctrl
        image: passctrl:latest
        volumeMounts:
        - name: secrets
          mountPath: /run/secrets
          readOnly: true
      volumes:
      - name: secrets
        secret:
          secretName: passctrl-secrets
```

## Testing Secrets Locally

### Option 1: Use Docker Compose (Recommended)

Follow the Quick Start above.

### Option 2: Manual Secret Files

If running PassCtrl natively (without Docker):

```bash
# Create secrets directory
sudo mkdir -p /run/secrets

# Write secrets (requires root)
echo "supersecret_db_password_123" | sudo tee /run/secrets/db_password
echo "sk-api-key-1234567890abcdef" | sudo tee /run/secrets/api_key
echo "webhook_secret_shared_key_xyz" | sudo tee /run/secrets/webhook_secret

# Fix permissions
sudo chmod 600 /run/secrets/*

# Run PassCtrl
./passctrl -config examples/docker-secrets/config.yaml
```

### Option 3: Configure Secrets Directory (Testing)

**Note:** The secrets directory is currently hardcoded to `/run/secrets/` in `internal/config/loader.go:207`.

For easier testing, consider making this configurable via environment variable:

```bash
# Future enhancement:
export PASSCTRL_SECRETS_DIR=/tmp/passctrl-test-secrets
mkdir -p /tmp/passctrl-test-secrets
echo "test-secret" > /tmp/passctrl-test-secrets/api_key
```

See `CODEBASE_ANALYSIS.md` for more details on this limitation.

## Troubleshooting

### Startup Fails: "secret file not found"

**Error:**
```
config: load secrets: secret file "/run/secrets/api_key" not found (referenced by server.variables.secrets.api_key)
```

**Solution:** Ensure the secret file exists and is readable:
```bash
# Check if secret exists
ls -la /run/secrets/

# For Docker Compose, verify secrets section
docker-compose config

# For Kubernetes, verify secret is mounted
kubectl describe pod <pod-name>
```

### Trailing Newlines in Secrets

Docker automatically adds trailing newlines to secret files. PassCtrl automatically trims them, so you don't need to worry about this.

```bash
# Docker writes secrets with newline
echo "secret" | docker secret create my-secret -
# PassCtrl reads: "secret" (newline trimmed)
```

## Files

- `docker-compose.yml` - Docker Compose stack with secrets
- `config.yaml` - PassCtrl configuration using secrets
- `secrets/` - Directory containing secret files (create manually)
- `README.md` - This file

## Related Examples

- `examples/env-vars-cel-and-templates.yaml` - Environment variables usage
- `examples/suites/template-env-bundle/` - Environment variables with templates
- `cmd/variables_integration_test.go` - Integration tests for variables
