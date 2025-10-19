package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigValidate(t *testing.T) {
	cfg := DefaultConfig()
	require.NoError(t, cfg.Validate())

	invalidPort := cfg
	invalidPort.Server.Listen.Port = -1
	require.Error(t, invalidPort.Validate())

	conflictingRules := cfg
	conflictingRules.Server.Rules.RulesFile = "rules.yaml"
	require.Error(t, conflictingRules.Validate())
}

func TestDefaultConfigValues(t *testing.T) {
	cfg := DefaultConfig()
	require.Equal(t, "0.0.0.0", cfg.Server.Listen.Address)
	require.Equal(t, 8080, cfg.Server.Listen.Port)
	require.Equal(t, "info", cfg.Server.Logging.Level)
	require.Equal(t, "./rules", cfg.Server.Rules.RulesFolder)
	require.Equal(t, "./templates", cfg.Server.Templates.TemplatesFolder)
	require.False(t, cfg.Server.Templates.TemplatesAllowEnv)
	require.Empty(t, cfg.Server.Templates.TemplatesAllowedEnv)
}
