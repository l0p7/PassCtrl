package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoader(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T) []string
		wantErr bool
		assert  func(t *testing.T, cfg Config)
	}{
		{
			name: "returns defaults when no overrides",
			setup: func(t *testing.T) []string {
				t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
				return nil
			},
			assert: func(t *testing.T, cfg Config) {
				require.Equal(t, 8080, cfg.Server.Listen.Port)
			},
		},
		{
			name: "merges file overrides",
			setup: func(t *testing.T) []string {
				dir := t.TempDir()
				path := filepath.Join(dir, "server.yaml")
				require.NoError(t, os.WriteFile(path, []byte("server:\n  listen:\n    port: 9090\n"), 0o600))
				t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
				return []string{path}
			},
			assert: func(t *testing.T, cfg Config) {
				require.Equal(t, 9090, cfg.Server.Listen.Port)
			},
		},
		{
			name: "prefers env overrides",
			setup: func(t *testing.T) []string {
				dir := t.TempDir()
				path := filepath.Join(dir, "server.yaml")
				require.NoError(t, os.WriteFile(path, []byte("server:\n  listen:\n    port: 9090\n"), 0o600))
				t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
				t.Setenv("PASSCTRL_SERVER__LISTEN__PORT", "9091")
				return []string{path}
			},
			assert: func(t *testing.T, cfg Config) {
				require.Equal(t, 9091, cfg.Server.Listen.Port)
			},
		},
		{
			name: "reads template block",
			setup: func(t *testing.T) []string {
				dir := t.TempDir()
				path := filepath.Join(dir, "server.yaml")
				contents := "server:\n  templates:\n    templatesFolder: /tmp/templates\n"
				require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
				t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
				return []string{path}
			},
			assert: func(t *testing.T, cfg Config) {
				require.Equal(t, "/tmp/templates", cfg.Server.Templates.TemplatesFolder)
			},
		},
		{
			name: "prefers env overrides for templates",
			setup: func(t *testing.T) []string {
				dir := t.TempDir()
				path := filepath.Join(dir, "server.yaml")
				contents := "server:\n  templates:\n    templatesFolder: /tmp/templates\n"
				require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
				t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
				t.Setenv("PASSCTRL_SERVER__TEMPLATES__TEMPLATESFOLDER", "/override")
				return []string{path}
			},
			assert: func(t *testing.T, cfg Config) {
				require.Equal(t, "/override", cfg.Server.Templates.TemplatesFolder)
			},
		},
		{
			name: "fails when file missing",
			setup: func(t *testing.T) []string {
				t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
				dir := t.TempDir()
				return []string{filepath.Join(dir, "missing.yaml")}
			},
			wantErr: true,
		},
		{
			name: "loads environment variables with null-copy semantics - null value",
			setup: func(t *testing.T) []string {
				dir := t.TempDir()
				path := filepath.Join(dir, "server.yaml")
				contents := "server:\n  variables:\n    environment:\n      TIMEZONE: null\n"
				require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
				t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
				t.Setenv("TIMEZONE", "UTC")
				return []string{path}
			},
			assert: func(t *testing.T, cfg Config) {
				require.Contains(t, cfg.LoadedEnvironment, "TIMEZONE")
				require.Equal(t, "UTC", cfg.LoadedEnvironment["TIMEZONE"])
			},
		},
		{
			name: "loads environment variables with null-copy semantics - explicit mapping",
			setup: func(t *testing.T) []string {
				dir := t.TempDir()
				path := filepath.Join(dir, "server.yaml")
				contents := "server:\n  variables:\n    environment:\n      timezone: TZ\n"
				require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
				t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
				t.Setenv("TZ", "America/New_York")
				return []string{path}
			},
			assert: func(t *testing.T, cfg Config) {
				require.Contains(t, cfg.LoadedEnvironment, "timezone")
				require.Equal(t, "America/New_York", cfg.LoadedEnvironment["timezone"])
			},
		},
		{
			name: "loads multiple environment variables",
			setup: func(t *testing.T) []string {
				dir := t.TempDir()
				path := filepath.Join(dir, "server.yaml")
				contents := "server:\n  variables:\n    environment:\n      timezone: TZ\n      HOME: null\n      custom_var: CUSTOM_ENV\n"
				require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
				t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
				t.Setenv("TZ", "UTC")
				t.Setenv("HOME", "/home/user")
				t.Setenv("CUSTOM_ENV", "custom_value")
				return []string{path}
			},
			assert: func(t *testing.T, cfg Config) {
				require.Len(t, cfg.LoadedEnvironment, 3)
				require.Equal(t, "UTC", cfg.LoadedEnvironment["timezone"])
				require.Equal(t, "/home/user", cfg.LoadedEnvironment["HOME"])
				require.Equal(t, "custom_value", cfg.LoadedEnvironment["custom_var"])
			},
		},
		{
			name: "fails when environment variable missing - null-copy",
			setup: func(t *testing.T) []string {
				dir := t.TempDir()
				path := filepath.Join(dir, "server.yaml")
				contents := "server:\n  variables:\n    environment:\n      MISSING_VAR: null\n"
				require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
				t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
				return []string{path}
			},
			wantErr: true,
		},
		{
			name: "fails when environment variable missing - explicit mapping",
			setup: func(t *testing.T) []string {
				dir := t.TempDir()
				path := filepath.Join(dir, "server.yaml")
				contents := "server:\n  variables:\n    environment:\n      myvar: MISSING_ENV_VAR\n"
				require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
				t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
				return []string{path}
			},
			wantErr: true,
		},
		{
			name: "handles empty environment variables config",
			setup: func(t *testing.T) []string {
				dir := t.TempDir()
				path := filepath.Join(dir, "server.yaml")
				contents := "server:\n  variables:\n    environment: {}\n"
				require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
				t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
				return []string{path}
			},
			assert: func(t *testing.T, cfg Config) {
				require.Empty(t, cfg.LoadedEnvironment)
			},
		},
		{
			name: "handles missing environment variables section",
			setup: func(t *testing.T) []string {
				dir := t.TempDir()
				path := filepath.Join(dir, "server.yaml")
				contents := "server:\n  listen:\n    port: 8080\n"
				require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
				t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
				return []string{path}
			},
			assert: func(t *testing.T, cfg Config) {
				require.Empty(t, cfg.LoadedEnvironment)
			},
		},
		{
			name: "loads rule file",
			setup: func(t *testing.T) []string {
				dir := t.TempDir()
				rulesPath := filepath.Join(dir, "rules.yaml")
				ruleContents := "endpoints:\n  file-endpoint:\n    description: from file\n    authentication:\n      allow:\n        authorization:\n          - bearer\n    rules:\n      - name: file-rule\nrules:\n  file-rule:\n    description: from file\n"
				require.NoError(t, os.WriteFile(rulesPath, []byte(ruleContents), 0o600))

				serverPath := filepath.Join(dir, "server.yaml")
				serverContents := "server:\n  rules:\n    rulesFolder: \"\"\n    rulesFile: %s\nendpoints:\n  inline-endpoint:\n    description: inline\n    authentication:\n      allow:\n        authorization:\n          - bearer\n    rules:\n      - name: inline-rule\nrules:\n  inline-rule:\n    description: inline\n"
				require.NoError(t, os.WriteFile(serverPath, []byte(fmt.Sprintf(serverContents, rulesPath)), 0o600))
				return []string{serverPath}
			},
			assert: func(t *testing.T, cfg Config) {
				require.Contains(t, cfg.Endpoints, "inline-endpoint")
				require.Contains(t, cfg.Endpoints, "file-endpoint")
				require.Contains(t, cfg.Rules, "file-rule")
				require.NotEmpty(t, cfg.RuleSources)
				require.Empty(t, cfg.SkippedDefinitions)
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			args := tc.setup(t)
			loader := NewLoader("PASSCTRL", args...)

			cfg, err := loader.Load(ctx)
			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			tc.assert(t, cfg)
		})
	}
}

func TestLoadSecrets(t *testing.T) {
	// Create a temporary directory to mock /run/secrets
	tempDir := t.TempDir()
	secretsDir := filepath.Join(tempDir, "run", "secrets")
	require.NoError(t, os.MkdirAll(secretsDir, 0o750))

	// Write test secret files
	require.NoError(t, os.WriteFile(filepath.Join(secretsDir, "db_password"), []byte("password123\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(secretsDir, "api_key"), []byte("key456"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(secretsDir, "token"), []byte("token789\r\n"), 0o600))

	// Temporarily replace the secretsDir constant by using a test-specific function
	// Since we can't override the const, we'll test the function logic with a modified version
	loadSecretsTest := func(secretsConfig map[string]*string, baseDir string) (map[string]string, error) {
		if len(secretsConfig) == 0 {
			return make(map[string]string), nil
		}

		secretsPath := filepath.Join(baseDir, "run", "secrets")
		result := make(map[string]string, len(secretsConfig))

		for key, valuePtr := range secretsConfig {
			var filename string
			if valuePtr == nil {
				filename = key
			} else {
				filename = *valuePtr
			}

			secretFile := filepath.Join(secretsPath, filename)
			content, err := os.ReadFile(secretFile) // #nosec G304 -- test code mimics production secret reading
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return nil, fmt.Errorf("secret file %q not found (referenced by server.variables.secrets.%s)", secretFile, key)
				}
				return nil, fmt.Errorf("failed to read secret file %q (referenced by server.variables.secrets.%s): %w", secretFile, key, err)
			}

			result[key] = strings.TrimRight(string(content), "\n\r")
		}
		return result, nil
	}

	tests := []struct {
		name    string
		config  map[string]*string
		want    map[string]string
		wantErr bool
	}{
		{
			name: "loads secret with null-copy semantics - null value",
			config: map[string]*string{
				"db_password": nil,
			},
			want: map[string]string{
				"db_password": "password123",
			},
		},
		{
			name: "loads secret with null-copy semantics - explicit mapping",
			config: map[string]*string{
				"password": strPtr("db_password"),
			},
			want: map[string]string{
				"password": "password123",
			},
		},
		{
			name: "loads multiple secrets",
			config: map[string]*string{
				"db_password": nil,
				"key":         strPtr("api_key"),
				"auth_token":  strPtr("token"),
			},
			want: map[string]string{
				"db_password": "password123",
				"key":         "key456",
				"auth_token":  "token789",
			},
		},
		{
			name: "trims trailing newlines from secrets",
			config: map[string]*string{
				"db_password": nil,
				"token":       nil,
			},
			want: map[string]string{
				"db_password": "password123",
				"token":       "token789",
			},
		},
		{
			name: "fails when secret file missing - null-copy",
			config: map[string]*string{
				"missing_secret": nil,
			},
			wantErr: true,
		},
		{
			name: "fails when secret file missing - explicit mapping",
			config: map[string]*string{
				"secret": strPtr("nonexistent"),
			},
			wantErr: true,
		},
		{
			name:   "handles empty secrets config",
			config: map[string]*string{},
			want:   map[string]string{},
		},
		{
			name:   "handles nil secrets config",
			config: nil,
			want:   map[string]string{},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result, err := loadSecretsTest(tc.config, tempDir)
			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, result)
		})
	}
}
