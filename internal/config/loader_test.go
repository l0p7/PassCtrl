package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
				contents := "server:\n  templates:\n    templatesFolder: /tmp/templates\n    templatesAllowEnv: true\n    templatesAllowedEnv:\n      - WHITELISTED\n"
				require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
				t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
				return []string{path}
			},
			assert: func(t *testing.T, cfg Config) {
				require.Equal(t, "/tmp/templates", cfg.Server.Templates.TemplatesFolder)
				require.True(t, cfg.Server.Templates.TemplatesAllowEnv)
				require.Equal(t, []string{"WHITELISTED"}, cfg.Server.Templates.TemplatesAllowedEnv)
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
