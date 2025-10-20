package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildRuleBundle(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name   string
		setup  func(t *testing.T) (map[string]EndpointConfig, map[string]RuleConfig, RulesConfig)
		assert func(t *testing.T, bundle RuleBundle, err error)
	}{
		{
			name: "merges inline and file sources",
			setup: func(t *testing.T) (map[string]EndpointConfig, map[string]RuleConfig, RulesConfig) {
				dir := t.TempDir()
				rulesFile := filepath.Join(dir, "rules.yaml")
				contents := "endpoints:\n  file-endpoint:\n    description: from file\n    rules:\n      - name: file-rule\nrules:\n  file-rule:\n    description: file rule\n"
				require.NoError(t, os.WriteFile(rulesFile, []byte(contents), 0o600))
				endpoints := map[string]EndpointConfig{"inline-endpoint": {Description: "inline"}}
				rules := map[string]RuleConfig{"inline-rule": {Description: "inline"}}
				return endpoints, rules, RulesConfig{RulesFile: rulesFile}
			},
			assert: func(t *testing.T, bundle RuleBundle, err error) {
				require.NoError(t, err)
				require.Len(t, bundle.Endpoints, 2)
				require.Contains(t, bundle.Endpoints, "inline-endpoint")
				require.Contains(t, bundle.Endpoints, "file-endpoint")
				require.Len(t, bundle.Rules, 2)
				require.Len(t, bundle.Sources, 2)
				require.Contains(t, bundle.Sources, inlineSourceName)
				require.Empty(t, bundle.Skipped)
			},
		},
		{
			name: "skips duplicate definitions",
			setup: func(t *testing.T) (map[string]EndpointConfig, map[string]RuleConfig, RulesConfig) {
				dir := t.TempDir()
				rulesFile := filepath.Join(dir, "rules.yaml")
				contents := "endpoints:\n  dup-endpoint:\n    description: from file\nrules:\n  dup-rule:\n    description: from file\n"
				require.NoError(t, os.WriteFile(rulesFile, []byte(contents), 0o600))
				endpoints := map[string]EndpointConfig{"dup-endpoint": {Description: "inline"}}
				rules := map[string]RuleConfig{"dup-rule": {Description: "inline"}}
				return endpoints, rules, RulesConfig{RulesFile: rulesFile}
			},
			assert: func(t *testing.T, bundle RuleBundle, err error) {
				require.NoError(t, err)
				require.Empty(t, bundle.Endpoints)
				require.Empty(t, bundle.Rules)
				require.Len(t, bundle.Skipped, 2)
				for _, skip := range bundle.Skipped {
					require.Contains(t, skip.Sources, inlineSourceName)
					require.Len(t, skip.Sources, 2)
					require.Equal(t, "duplicate definition", skip.Reason)
				}
			},
		},
		{
			name: "skips endpoints missing rules",
			setup: func(t *testing.T) (map[string]EndpointConfig, map[string]RuleConfig, RulesConfig) {
				endpoints := map[string]EndpointConfig{
					"healthy-endpoint": {Rules: []EndpointRuleReference{{Name: "present-rule"}}},
					"broken-endpoint":  {Rules: []EndpointRuleReference{{Name: "missing-rule"}, {Name: "present-rule"}}},
				}
				rules := map[string]RuleConfig{"present-rule": {Description: "inline"}}
				return endpoints, rules, RulesConfig{}
			},
			assert: func(t *testing.T, bundle RuleBundle, err error) {
				require.NoError(t, err)
				require.Len(t, bundle.Endpoints, 1)
				require.Contains(t, bundle.Endpoints, "healthy-endpoint")
				require.Len(t, bundle.Skipped, 1)
				skipped := bundle.Skipped[0]
				require.Equal(t, "endpoint", skipped.Kind)
				require.Equal(t, "broken-endpoint", skipped.Name)
				require.Equal(t, "missing rule dependencies: missing-rule", skipped.Reason)
			},
		},
		{
			name: "skips invalid expressions",
			setup: func(t *testing.T) (map[string]EndpointConfig, map[string]RuleConfig, RulesConfig) {
				rules := map[string]RuleConfig{
					"bad-rule": {Conditions: RuleConditionConfig{Pass: []string{"1 + 1"}}},
				}
				return nil, rules, RulesConfig{}
			},
			assert: func(t *testing.T, bundle RuleBundle, err error) {
				require.NoError(t, err)
				require.Empty(t, bundle.Rules)
				require.Len(t, bundle.Skipped, 1)
				skipped := bundle.Skipped[0]
				require.Equal(t, "rule", skipped.Kind)
				require.Equal(t, "bad-rule", skipped.Name)
				require.Contains(t, skipped.Reason, "invalid rule expressions")
			},
		},
		{
			name: "skips invalid variable expressions",
			setup: func(t *testing.T) (map[string]EndpointConfig, map[string]RuleConfig, RulesConfig) {
				rules := map[string]RuleConfig{
					"bad-vars": {
						Variables: RuleVariablesConfig{
							Rule: map[string]RuleVariableSpec{
								"user": {From: "!!invalid"},
							},
						},
					},
				}
				return nil, rules, RulesConfig{}
			},
			assert: func(t *testing.T, bundle RuleBundle, err error) {
				require.NoError(t, err)
				require.Empty(t, bundle.Rules)
				require.Len(t, bundle.Skipped, 1)
				skipped := bundle.Skipped[0]
				require.Equal(t, "rule", skipped.Kind)
				require.Equal(t, "bad-vars", skipped.Name)
				require.Contains(t, skipped.Reason, "variables.rule[user]")
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			endpoints, rules, cfg := tc.setup(t)
			bundle, err := buildRuleBundle(ctx, endpoints, rules, cfg)
			tc.assert(t, bundle, err)
		})
	}
}
