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

	// Test authorization header validation
	t.Run("authorization header in backendApi headers forbidden", func(t *testing.T) {
		authHeader := strPtr("Bearer token")
		invalidAuth := DefaultConfig()
		invalidAuth.Endpoints = map[string]EndpointConfig{
			"test": {
				Authentication: EndpointAuthenticationConfig{
					Allow: EndpointAuthAllowConfig{Authorization: []string{"bearer"}},
				},
			},
		}
		invalidAuth.Rules = map[string]RuleConfig{
			"test-rule": {
				BackendAPI: RuleBackendConfig{
					URL:    "https://backend.example/verify",
					Method: "POST",
					Headers: map[string]*string{
						"authorization": authHeader, // Forbidden - must use auth.forwardAs
					},
				},
			},
		}
		err := invalidAuth.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "authorization header forbidden")
		require.Contains(t, err.Error(), "use auth.forwardAs instead")
	})

	t.Run("authorization header case-insensitive validation", func(t *testing.T) {
		authHeader := strPtr("Bearer token")
		invalidAuth := DefaultConfig()
		invalidAuth.Endpoints = map[string]EndpointConfig{
			"test": {
				Authentication: EndpointAuthenticationConfig{
					Allow: EndpointAuthAllowConfig{Authorization: []string{"bearer"}},
				},
			},
		}
		invalidAuth.Rules = map[string]RuleConfig{
			"test-rule": {
				BackendAPI: RuleBackendConfig{
					URL:    "https://backend.example/verify",
					Method: "POST",
					Headers: map[string]*string{
						"Authorization": authHeader, // Mixed case - still forbidden
					},
				},
			},
		}
		err := invalidAuth.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "authorization header forbidden")
	})

	t.Run("authorization header with whitespace validation", func(t *testing.T) {
		authHeader := strPtr("Bearer token")
		invalidAuth := DefaultConfig()
		invalidAuth.Endpoints = map[string]EndpointConfig{
			"test": {
				Authentication: EndpointAuthenticationConfig{
					Allow: EndpointAuthAllowConfig{Authorization: []string{"bearer"}},
				},
			},
		}
		invalidAuth.Rules = map[string]RuleConfig{
			"test-rule": {
				BackendAPI: RuleBackendConfig{
					URL:    "https://backend.example/verify",
					Method: "POST",
					Headers: map[string]*string{
						"  Authorization  ": authHeader, // Whitespace - still forbidden
					},
				},
			},
		}
		err := invalidAuth.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "authorization header forbidden")
	})

	t.Run("other headers allowed in backendApi", func(t *testing.T) {
		validBackend := DefaultConfig()
		validBackend.Endpoints = map[string]EndpointConfig{
			"test": {
				Authentication: EndpointAuthenticationConfig{
					Allow: EndpointAuthAllowConfig{Authorization: []string{"bearer"}},
				},
			},
		}
		validBackend.Rules = map[string]RuleConfig{
			"test-rule": {
				BackendAPI: RuleBackendConfig{
					URL:    "https://backend.example/verify",
					Method: "POST",
					Headers: map[string]*string{
						"x-request-id": strPtr("{{ .raw.headers.x-request-id }}"),
						"content-type": strPtr("application/json"),
					},
				},
			},
		}
		require.NoError(t, validBackend.Validate())
	})
}

func strPtr(s string) *string {
	return &s
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
