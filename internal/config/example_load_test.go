package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadExampleConfigs(t *testing.T) {
	// Get the project root (config package is at internal/config)
	wd, err := os.Getwd()
	require.NoError(t, err)
	projectRoot := filepath.Join(wd, "..", "..")

	examples := []struct {
		name     string
		path     string
		validate func(t *testing.T, cfg Config)
	}{
		{
			name: "basic-auth-gateway",
			path: "examples/configs/basic-auth-gateway.yaml",
			validate: func(t *testing.T, cfg Config) {
				require.Contains(t, cfg.Endpoints, "basic-gateway")
				ep := cfg.Endpoints["basic-gateway"]
				require.NotNil(t, ep.Authentication.Required)
				require.True(t, *ep.Authentication.Required)
				require.Equal(t, []string{"basic"}, ep.Authentication.Allow.Authorization)
				require.Equal(t, "basic", ep.Authentication.Challenge.Type)
				require.Equal(t, "Internal", ep.Authentication.Challenge.Realm)
				require.Equal(t, "UTF-8", ep.Authentication.Challenge.Charset)
			},
		},
		{
			name: "backend-token-introspection",
			path: "examples/configs/backend-token-introspection.yaml",
			validate: func(t *testing.T, cfg Config) {
				require.Contains(t, cfg.Endpoints, "introspection")
				ep := cfg.Endpoints["introspection"]
				require.NotNil(t, ep.Authentication.Required)
				require.True(t, *ep.Authentication.Required)
				require.Equal(t, []string{"bearer"}, ep.Authentication.Allow.Authorization)
				require.Equal(t, "bearer", ep.Authentication.Challenge.Type)
				require.Equal(t, "Identity", ep.Authentication.Challenge.Realm)
			},
		},
		{
			name: "cached-multi-endpoint",
			path: "examples/configs/cached-multi-endpoint.yaml",
			validate: func(t *testing.T, cfg Config) {
				require.Contains(t, cfg.Endpoints, "shared-default")
				ep := cfg.Endpoints["shared-default"]
				require.NotNil(t, ep.Authentication.Required)
				require.False(t, *ep.Authentication.Required)
				require.Equal(t, []string{"bearer"}, ep.Authentication.Allow.Authorization)
				require.Equal(t, []string{"x-session-token"}, ep.Authentication.Allow.Header)

				require.Contains(t, cfg.Endpoints, "admin-audit")
				adminEp := cfg.Endpoints["admin-audit"]
				require.NotNil(t, adminEp.Authentication.Required)
				require.True(t, *adminEp.Authentication.Required)
				require.Equal(t, []string{"bearer"}, adminEp.Authentication.Allow.Authorization)
				require.Equal(t, "bearer", adminEp.Authentication.Challenge.Type)
				require.Equal(t, "Admin", adminEp.Authentication.Challenge.Realm)
			},
		},
	}

	for _, tc := range examples {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			configPath := filepath.Join(projectRoot, tc.path)

			// Disable rules folder to use inline rules from the config file
			t.Setenv("PASSCTRL_SERVER__RULES__RULESFOLDER", "")

			loader := NewLoader("PASSCTRL", configPath)
			cfg, err := loader.Load(context.Background())
			require.NoError(t, err, "Failed to load %s", tc.path)

			tc.validate(t, cfg)
		})
	}
}
