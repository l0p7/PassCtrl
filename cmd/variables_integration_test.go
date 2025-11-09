package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gavv/httpexpect/v2"
	"github.com/stretchr/testify/require"
)

// TestIntegrationEnvironmentVariables verifies that environment variables
// are accessible via both CEL expressions and Go templates.
//
// This test demonstrates:
// 1. CEL access without {{ }} delimiters in endpoint variables
// 2. Go template access with {{ }} delimiters in headers and bodies
// 3. Null-copy semantics for environment variable loading
// 4. Variables propagate from endpoint-level to response rendering
//
// Note: Secrets testing requires /run/secrets directory which needs elevated
// privileges or Docker volume mounts. Consider making secrets directory
// configurable for easier testing, or use Docker-based integration tests.
func TestIntegrationEnvironmentVariables(t *testing.T) {
	if os.Getenv("PASSCTRL_INTEGRATION") == "" {
		t.Skip("set PASSCTRL_INTEGRATION=1 to run integration tests")
	}
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	temp := t.TempDir()
	port := allocatePort(t)
	configPath := writeEnvVarsConfig(t, temp, port)

	// Set environment variables that the config expects
	envVars := map[string]string{
		"PASSCTRL_TEST_TIER":          "premium",
		"PASSCTRL_TEST_API_KEY":       "test-key-12345",
		"PASSCTRL_TEST_SUPPORT_EMAIL": "support@example.com",
	}

	process := startServerProcess(t, configPath, envVars)
	defer process.stop(t)

	client := &http.Client{Timeout: 5 * time.Second}
	waitForEndpoint(t, client, integrationURL(port, "/env-cel/auth"), 45*time.Second, map[string]string{
		"Authorization": "Bearer test-token",
	})

	expect := httpexpect.WithConfig(httpexpect.Config{
		BaseURL:  integrationURL(port, ""),
		Reporter: httpexpect.NewRequireReporter(t),
		Client:   client,
	})

	t.Run("CEL endpoint variable accesses environment variable", func(t *testing.T) {
		// Endpoint variable uses CEL: variables.environment.TIER
		// Response headers should show the evaluated value
		result := expect.GET("/env-cel/auth").
			WithHeader("Authorization", "Bearer test-user").
			Expect()

		result.Status(http.StatusOK)
		// X-Tier header comes from endpoint variable evaluated via CEL
		result.Header("X-Tier").IsEqual("premium")
	})

	t.Run("Go template endpoint variable accesses environment variable", func(t *testing.T) {
		result := expect.GET("/env-template/auth").
			WithHeader("Authorization", "Bearer test-user").
			Expect()

		result.Status(http.StatusOK)
		// X-Support header comes from endpoint variable evaluated via Go template
		result.Header("X-Support").IsEqual("support@example.com")
	})

	t.Run("Environment variables accessible in response body templates", func(t *testing.T) {
		result := expect.GET("/env-combined/auth").
			WithHeader("Authorization", "Bearer test-user").
			Expect()

		result.Status(http.StatusOK)
		body := result.Body().Raw()
		// Body template directly accesses environment variables
		require.Contains(t, body, "support@example.com", "response body should contain support email from env var")
		require.Contains(t, body, "premium", "response body should contain tier from env var")
		require.Contains(t, body, "test-key-12345", "response body should contain API key from env var")
	})

	t.Run("Rule condition uses CEL with environment variable", func(t *testing.T) {
		// Rule uses CEL condition to check if tier matches
		result := expect.GET("/env-rule-cel/auth").
			WithQuery("tier", "premium").
			WithHeader("Authorization", "Bearer test-user").
			Expect()

		result.Status(http.StatusOK)
		body := result.Body().Raw()
		require.Contains(t, body, "tier matches", "should pass when tier matches env var")

		// Non-matching tier should fail
		result = expect.GET("/env-rule-cel/auth").
			WithQuery("tier", "basic").
			WithHeader("Authorization", "Bearer test-user").
			Expect()

		result.Status(http.StatusForbidden)
		body = result.Body().Raw()
		require.Contains(t, body, "tier mismatch", "should fail when tier doesn't match env var")
	})
}

// writeEnvVarsConfig creates a configuration file that exercises both CEL
// and Go template access to environment variables.
func writeEnvVarsConfig(t *testing.T, dir string, port int) string {
	t.Helper()

	if err := os.MkdirAll(dir, 0o750); err != nil {
		require.NoError(t, err, "failed to ensure config folder")
	}

	cfg := map[string]any{
		"server": map[string]any{
			"listen": map[string]any{
				"address": "127.0.0.1",
				"port":    port,
			},
			"logging": map[string]any{
				"format":            "text",
				"level":             "warn",
				"correlationHeader": "X-Request-ID",
			},
			"rules": map[string]any{
				"rulesFolder": "",
			},
			"cache": map[string]any{
				"backend":    "memory",
				"ttlSeconds": 5,
			},
			"variables": map[string]any{
				"environment": map[string]any{
					// Null-copy semantics: value specifies source env var name
					"TIER":          "PASSCTRL_TEST_TIER",
					"API_KEY":       "PASSCTRL_TEST_API_KEY",
					"SUPPORT_EMAIL": "PASSCTRL_TEST_SUPPORT_EMAIL",
				},
			},
		},
		"endpoints": map[string]any{
			// Endpoint 1: CEL in endpoint variable
			"env-cel": map[string]any{
				"authentication": map[string]any{
					"required": true,
					"allow": map[string]any{
						"authorization": []string{"bearer"},
					},
				},
				"variables": map[string]string{
					// CEL expression (no {{ }}) accessing environment variable
					"tier_value": "variables.environment.TIER",
				},
				"rules": []map[string]any{
					{"name": "always-pass"},
				},
				"responsePolicy": map[string]any{
					"pass": map[string]any{
						"status": http.StatusOK,
						"headers": map[string]any{
							"custom": map[string]string{
								// Template accessing endpoint variable (evaluated via CEL)
								"X-Tier": "{{ .variables.global.tier_value }}",
							},
						},
						"body": `{"tier":"{{ .variables.global.tier_value }}"}`,
					},
				},
			},
			// Endpoint 2: Go template in endpoint variable
			"env-template": map[string]any{
				"authentication": map[string]any{
					"required": true,
					"allow": map[string]any{
						"authorization": []string{"bearer"},
					},
				},
				"variables": map[string]string{
					// Go template ({{ }}) accessing environment variable
					"support_email": "{{ .variables.environment.SUPPORT_EMAIL }}",
				},
				"rules": []map[string]any{
					{"name": "always-pass"},
				},
				"responsePolicy": map[string]any{
					"pass": map[string]any{
						"status": http.StatusOK,
						"headers": map[string]any{
							"custom": map[string]string{
								// Template accessing endpoint variable (evaluated via Go template)
								"X-Support": "{{ .variables.global.support_email }}",
							},
						},
						"body": `{"support":"{{ .variables.global.support_email }}"}`,
					},
				},
			},
			// Endpoint 3: Direct template access in response
			"env-combined": map[string]any{
				"authentication": map[string]any{
					"required": true,
					"allow": map[string]any{
						"authorization": []string{"bearer"},
					},
				},
				"rules": []map[string]any{
					{"name": "always-pass"},
				},
				"responsePolicy": map[string]any{
					"pass": map[string]any{
						"status": http.StatusOK,
						"body": `{"tier":"{{ .variables.environment.TIER }}","apiKey":"{{ .variables.environment.API_KEY }}","support":"{{ .variables.environment.SUPPORT_EMAIL }}"}`,
					},
				},
			},
			// Endpoint 4: Rule condition with CEL
			"env-rule-cel": map[string]any{
				"authentication": map[string]any{
					"required": true,
					"allow": map[string]any{
						"authorization": []string{"bearer"},
					},
				},
				"rules": []map[string]any{
					{"name": "check-tier-matches"},
				},
				"responsePolicy": map[string]any{
					"pass": map[string]any{
						"status": http.StatusOK,
						"body":   `{"status":"pass","message":"tier matches"}`,
					},
					"fail": map[string]any{
						"status": http.StatusForbidden,
						"body":   `{"status":"fail","message":"tier mismatch"}`,
					},
				},
			},
		},
		"rules": map[string]any{
			"always-pass": map[string]any{
				"description": "Always passes to test endpoint variables",
				"conditions": map[string]any{
					"pass": []string{"true"},
				},
			},
			"check-tier-matches": map[string]any{
				"description": "Uses CEL to check tier query param against env var",
				"conditions": map[string]any{
					// CEL condition comparing query param to environment variable
					"pass": []string{
						`lookup(forward.query, "tier") == variables.environment.TIER`,
					},
					"fail": []string{
						`lookup(forward.query, "tier") != variables.environment.TIER`,
					},
				},
			},
		},
	}

	contents, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err, "failed to marshal config")
	path := filepath.Join(dir, "env-vars-config.json")
	require.NoError(t, os.WriteFile(path, contents, 0o600), "failed to write config")
	return path
}
