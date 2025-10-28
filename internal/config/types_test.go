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

	endpointWithoutAllow := cfg
	endpointWithoutAllow.Endpoints = map[string]EndpointConfig{
		"default": {},
	}
	require.Error(t, endpointWithoutAllow.Validate())

	validEndpoint := cfg
	validEndpoint.Endpoints = map[string]EndpointConfig{
		"default": {
			Authentication: EndpointAuthenticationConfig{
				Allow: EndpointAuthAllowConfig{Authorization: []string{"bearer"}},
			},
		},
	}
	require.NoError(t, validEndpoint.Validate())

	// Test TTL validation
	t.Run("invalid TTL duration strings", func(t *testing.T) {
		invalidTTL := DefaultConfig()
		invalidTTL.Endpoints = map[string]EndpointConfig{
			"test": {
				Authentication: EndpointAuthenticationConfig{
					Allow: EndpointAuthAllowConfig{Authorization: []string{"bearer"}},
				},
			},
		}
		invalidTTL.Rules = map[string]RuleConfig{
			"test-rule": {
				Cache: RuleCacheConfig{
					TTL: RuleCacheTTLConfig{
						Pass: "invalid-duration",
					},
				},
			},
		}
		require.Error(t, invalidTTL.Validate())
	})

	t.Run("valid TTL duration strings", func(t *testing.T) {
		validTTL := DefaultConfig()
		validTTL.Endpoints = map[string]EndpointConfig{
			"test": {
				Authentication: EndpointAuthenticationConfig{
					Allow: EndpointAuthAllowConfig{Authorization: []string{"bearer"}},
				},
			},
		}
		validTTL.Rules = map[string]RuleConfig{
			"test-rule": {
				Cache: RuleCacheConfig{
					TTL: RuleCacheTTLConfig{
						Pass:  "5m",
						Fail:  "30s",
						Error: "0s",
					},
				},
			},
		}
		require.NoError(t, validTTL.Validate())
	})

	// Test variable validation
	t.Run("empty variable name", func(t *testing.T) {
		invalidVar := DefaultConfig()
		invalidVar.Endpoints = map[string]EndpointConfig{
			"test": {
				Authentication: EndpointAuthenticationConfig{
					Allow: EndpointAuthAllowConfig{Authorization: []string{"bearer"}},
				},
				Variables: map[string]string{
					"": "some expression",
				},
			},
		}
		require.Error(t, invalidVar.Validate())
	})

	t.Run("valid variables", func(t *testing.T) {
		validVars := DefaultConfig()
		validVars.Endpoints = map[string]EndpointConfig{
			"test": {
				Authentication: EndpointAuthenticationConfig{
					Allow: EndpointAuthAllowConfig{Authorization: []string{"bearer"}},
				},
				Variables: map[string]string{
					"user_id": "request.header.get('X-User-ID')",
					"tenant":  "{{ .request.query.tenant }}",
				},
			},
		}
		validVars.Rules = map[string]RuleConfig{
			"test-rule": {
				Variables: map[string]string{
					"temp_id": "backend.body.userId",
				},
				Responses: RuleResponsesConfig{
					Pass: RuleResponseConfig{
						Variables: map[string]string{
							"user_id": "variables.temp_id",
						},
					},
				},
			},
		}
		require.NoError(t, validVars.Validate())
	})
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
