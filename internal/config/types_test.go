package config

import "testing"

func TestConfigValidate(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should validate, got %v", err)
	}

	invalidPort := cfg
	invalidPort.Server.Listen.Port = -1
	if err := invalidPort.Validate(); err == nil {
		t.Fatalf("expected failure when port is invalid")
	}

	conflictingRules := cfg
	conflictingRules.Server.Rules.RulesFile = "rules.yaml"
	if err := conflictingRules.Validate(); err == nil {
		t.Fatalf("expected failure when both rulesFolder and rulesFile are set")
	}
}

func TestDefaultConfigValues(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Server.Listen.Address != "0.0.0.0" {
		t.Errorf("expected listen address 0.0.0.0, got %q", cfg.Server.Listen.Address)
	}
	if cfg.Server.Listen.Port != 8080 {
		t.Errorf("expected listen port 8080, got %d", cfg.Server.Listen.Port)
	}
	if cfg.Server.Logging.Level != "info" {
		t.Errorf("expected logging level info, got %q", cfg.Server.Logging.Level)
	}
	if cfg.Server.Rules.RulesFolder != "./rules" {
		t.Errorf("expected rules folder ./rules, got %q", cfg.Server.Rules.RulesFolder)
	}
	if cfg.Server.Templates.TemplatesFolder != "./templates" {
		t.Errorf("expected templates folder ./templates, got %q", cfg.Server.Templates.TemplatesFolder)
	}
	if cfg.Server.Templates.TemplatesAllowEnv {
		t.Errorf("expected templatesAllowEnv to default to false")
	}
	if len(cfg.Server.Templates.TemplatesAllowedEnv) != 0 {
		t.Errorf("expected templatesAllowedEnv to default empty, got %v", cfg.Server.Templates.TemplatesAllowedEnv)
	}
}
