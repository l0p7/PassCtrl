package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoaderReturnsDefaultsWhenNoOverrides(t *testing.T) {
	t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
	loader := NewLoader("PASSCTRL")
	cfg, err := loader.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, 8080, cfg.Server.Listen.Port)
}

func TestLoaderMergesFileOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yaml")
	require.NoError(t, os.WriteFile(path, []byte("server:\n  listen:\n    port: 9090\n"), 0o600))

	t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
	loader := NewLoader("PASSCTRL", path)
	cfg, err := loader.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, 9090, cfg.Server.Listen.Port)
}

func TestLoaderPrefersEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yaml")
	require.NoError(t, os.WriteFile(path, []byte("server:\n  listen:\n    port: 9090\n"), 0o600))

	const envKey = "PASSCTRL_SERVER__LISTEN__PORT"
	require.NoError(t, os.Setenv(envKey, "9091"))
	t.Cleanup(func() { _ = os.Unsetenv(envKey) })

	t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())

	loader := NewLoader("PASSCTRL", path)
	cfg, err := loader.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, 9091, cfg.Server.Listen.Port)
}

func TestLoaderReadsTemplateBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yaml")
	contents := "server:\n  templates:\n    templatesFolder: /tmp/templates\n    templatesAllowEnv: true\n    templatesAllowedEnv:\n      - WHITELISTED\n"
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))

	t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
	loader := NewLoader("PASSCTRL", path)
	cfg, err := loader.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, "/tmp/templates", cfg.Server.Templates.TemplatesFolder)
	require.True(t, cfg.Server.Templates.TemplatesAllowEnv)
	require.Equal(t, []string{"WHITELISTED"}, cfg.Server.Templates.TemplatesAllowedEnv)
}

func TestLoaderPrefersEnvOverridesForTemplates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yaml")
	contents := "server:\n  templates:\n    templatesFolder: /tmp/templates\n"
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))

	const envKey = "PASSCTRL_SERVER__TEMPLATES__TEMPLATESFOLDER"
	require.NoError(t, os.Setenv(envKey, "/override"))
	t.Cleanup(func() { _ = os.Unsetenv(envKey) })

	t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
	loader := NewLoader("PASSCTRL", path)
	cfg, err := loader.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, "/override", cfg.Server.Templates.TemplatesFolder)
}

func TestLoaderFailsWhenFileMissing(t *testing.T) {
	t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
	loader := NewLoader("PASSCTRL", "missing.yaml")
	_, err := loader.Load(context.Background())
	require.Error(t, err)
}

func TestLoaderLoadsRuleFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	rulesPath := filepath.Join(dir, "rules.yaml")
	ruleContents := "endpoints:\n  file-endpoint:\n    description: from file\n    rules:\n      - name: file-rule\nrules:\n  file-rule:\n    description: from file\n"
	require.NoError(t, os.WriteFile(rulesPath, []byte(ruleContents), 0o600))

	serverPath := filepath.Join(dir, "server.yaml")
	serverContents := "server:\n  rules:\n    rulesFolder: \"\"\n    rulesFile: %s\nendpoints:\n  inline-endpoint:\n    description: inline\n    rules:\n      - name: inline-rule\nrules:\n  inline-rule:\n    description: inline\n"
	require.NoError(t, os.WriteFile(serverPath, []byte(fmt.Sprintf(serverContents, rulesPath)), 0o600))

	loader := NewLoader("PASSCTRL", serverPath)
	cfg, err := loader.Load(ctx)
	require.NoError(t, err)
	require.Contains(t, cfg.Endpoints, "inline-endpoint")
	require.Contains(t, cfg.Endpoints, "file-endpoint")
	require.Contains(t, cfg.Rules, "file-rule")
	require.NotEmpty(t, cfg.RuleSources)
	require.Empty(t, cfg.SkippedDefinitions)
}
