package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestLoaderReturnsDefaultsWhenNoOverrides(t *testing.T) {
	t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
	loader := NewLoader("PASSCTRL")
	cfg, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("loader should not fail without overrides: %v", err)
	}
	if cfg.Server.Listen.Port != 8080 {
		t.Fatalf("expected default port 8080, got %d", cfg.Server.Listen.Port)
	}
}

func TestLoaderMergesFileOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yaml")
	if err := os.WriteFile(path, []byte("server:\n  listen:\n    port: 9090\n"), 0o600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
	loader := NewLoader("PASSCTRL", path)
	cfg, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("loader should use file override: %v", err)
	}
	if cfg.Server.Listen.Port != 9090 {
		t.Fatalf("expected port 9090, got %d", cfg.Server.Listen.Port)
	}
}

func TestLoaderPrefersEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yaml")
	if err := os.WriteFile(path, []byte("server:\n  listen:\n    port: 9090\n"), 0o600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	const envKey = "PASSCTRL_SERVER__LISTEN__PORT"
	if err := os.Setenv(envKey, "9091"); err != nil {
		t.Fatalf("setenv failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv(envKey) })

	t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())

	loader := NewLoader("PASSCTRL", path)
	cfg, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("loader should prefer env override: %v", err)
	}
	if cfg.Server.Listen.Port != 9091 {
		t.Fatalf("expected port 9091, got %d", cfg.Server.Listen.Port)
	}
}

func TestLoaderReadsTemplateBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yaml")
	contents := "server:\n  templates:\n    templatesFolder: /tmp/templates\n    templatesAllowEnv: true\n    templatesAllowedEnv:\n      - WHITELISTED\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
	loader := NewLoader("PASSCTRL", path)
	cfg, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("loader should parse templates block: %v", err)
	}
	if cfg.Server.Templates.TemplatesFolder != "/tmp/templates" {
		t.Fatalf("expected templates folder override, got %q", cfg.Server.Templates.TemplatesFolder)
	}
	if !cfg.Server.Templates.TemplatesAllowEnv {
		t.Fatalf("expected templatesAllowEnv to be true")
	}
	if len(cfg.Server.Templates.TemplatesAllowedEnv) != 1 || cfg.Server.Templates.TemplatesAllowedEnv[0] != "WHITELISTED" {
		t.Fatalf("unexpected templatesAllowedEnv: %v", cfg.Server.Templates.TemplatesAllowedEnv)
	}
}

func TestLoaderPrefersEnvOverridesForTemplates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yaml")
	contents := "server:\n  templates:\n    templatesFolder: /tmp/templates\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	const envKey = "PASSCTRL_SERVER__TEMPLATES__TEMPLATESFOLDER"
	if err := os.Setenv(envKey, "/override"); err != nil {
		t.Fatalf("setenv failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Unsetenv(envKey) })

	t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
	loader := NewLoader("PASSCTRL", path)
	cfg, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("loader should apply env override: %v", err)
	}
	if cfg.Server.Templates.TemplatesFolder != "/override" {
		t.Fatalf("expected env override to win, got %q", cfg.Server.Templates.TemplatesFolder)
	}
}

func TestLoaderFailsWhenFileMissing(t *testing.T) {
	t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", t.TempDir())
	loader := NewLoader("PASSCTRL", "missing.yaml")
	if _, err := loader.Load(context.Background()); err == nil {
		t.Fatalf("expected error when config file is missing")
	}
}

func TestLoaderLoadsRuleFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	rulesPath := filepath.Join(dir, "rules.yaml")
	ruleContents := "endpoints:\n  file-endpoint:\n    description: from file\n    rules:\n      - name: file-rule\nrules:\n  file-rule:\n    description: from file\n"
	if err := os.WriteFile(rulesPath, []byte(ruleContents), 0o600); err != nil {
		t.Fatalf("failed to write rules document: %v", err)
	}

	serverPath := filepath.Join(dir, "server.yaml")
	serverContents := "server:\n  rules:\n    rulesFolder: \"\"\n    rulesFile: %s\nendpoints:\n  inline-endpoint:\n    description: inline\n    rules:\n      - name: inline-rule\nrules:\n  inline-rule:\n    description: inline\n"
	if err := os.WriteFile(serverPath, []byte(fmt.Sprintf(serverContents, rulesPath)), 0o600); err != nil {
		t.Fatalf("failed to write server config: %v", err)
	}

	loader := NewLoader("PASSCTRL", serverPath)
	cfg, err := loader.Load(ctx)
	if err != nil {
		t.Fatalf("loader should combine rule sources: %v", err)
	}
	if _, ok := cfg.Endpoints["inline-endpoint"]; !ok {
		t.Fatalf("expected inline endpoint present")
	}
	if _, ok := cfg.Endpoints["file-endpoint"]; !ok {
		t.Fatalf("expected file endpoint present")
	}
	if _, ok := cfg.Rules["file-rule"]; !ok {
		t.Fatalf("expected file rule present")
	}
	if len(cfg.RuleSources) == 0 {
		t.Fatalf("expected rule sources recorded, got %v", cfg.RuleSources)
	}
	if len(cfg.SkippedDefinitions) != 0 {
		t.Fatalf("expected no skipped definitions, got %v", cfg.SkippedDefinitions)
	}
}
