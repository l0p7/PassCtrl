package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildRuleBundleMergesSources(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	rulesFile := filepath.Join(dir, "rules.yaml")
	contents := "endpoints:\n  file-endpoint:\n    description: from file\n    rules:\n      - name: file-rule\nrules:\n  file-rule:\n    description: file rule\n"
	require.NoError(t, os.WriteFile(rulesFile, []byte(contents), 0o600))

	inlineEndpoints := map[string]EndpointConfig{
		"inline-endpoint": {Description: "inline"},
	}
	inlineRules := map[string]RuleConfig{
		"inline-rule": {Description: "inline"},
	}

	bundle, err := buildRuleBundle(ctx, inlineEndpoints, inlineRules, RulesConfig{RulesFile: rulesFile})
	require.NoError(t, err)
	require.Len(t, bundle.Endpoints, 2)
	require.Contains(t, bundle.Endpoints, "inline-endpoint")
	require.Contains(t, bundle.Endpoints, "file-endpoint")
	require.Len(t, bundle.Rules, 2)
	require.Contains(t, bundle.Sources, inlineSourceName)
	require.Contains(t, bundle.Sources, filepath.Clean(rulesFile))
	require.Empty(t, bundle.Skipped)
}

func TestBuildRuleBundleSkipsDuplicates(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	rulesFile := filepath.Join(dir, "rules.yaml")
	contents := "endpoints:\n  dup-endpoint:\n    description: from file\nrules:\n  dup-rule:\n    description: from file\n"
	require.NoError(t, os.WriteFile(rulesFile, []byte(contents), 0o600))

	inlineEndpoints := map[string]EndpointConfig{
		"dup-endpoint": {Description: "inline"},
	}
	inlineRules := map[string]RuleConfig{
		"dup-rule": {Description: "inline"},
	}

	bundle, err := buildRuleBundle(ctx, inlineEndpoints, inlineRules, RulesConfig{RulesFile: rulesFile})
	require.NoError(t, err)
	require.Empty(t, bundle.Endpoints)
	require.Empty(t, bundle.Rules)
	require.Len(t, bundle.Skipped, 2)
	for _, skip := range bundle.Skipped {
		require.Contains(t, skip.Sources, inlineSourceName)
		require.Contains(t, skip.Sources, filepath.Clean(rulesFile))
		require.Equal(t, "duplicate definition", skip.Reason)
	}
}

func TestBuildRuleBundleSkipsEndpointsMissingRules(t *testing.T) {
	ctx := context.Background()
	inlineEndpoints := map[string]EndpointConfig{
		"healthy-endpoint": {
			Rules: []EndpointRuleReference{{Name: "present-rule"}},
		},
		"broken-endpoint": {
			Rules: []EndpointRuleReference{{Name: "missing-rule"}, {Name: "present-rule"}},
		},
	}
	inlineRules := map[string]RuleConfig{
		"present-rule": {Description: "inline"},
	}

	bundle, err := buildRuleBundle(ctx, inlineEndpoints, inlineRules, RulesConfig{})
	require.NoError(t, err)
	require.Len(t, bundle.Endpoints, 1)
	require.Contains(t, bundle.Endpoints, "healthy-endpoint")
	require.Len(t, bundle.Skipped, 1)
	skipped := bundle.Skipped[0]
	require.Equal(t, "endpoint", skipped.Kind)
	require.Equal(t, "broken-endpoint", skipped.Name)
	require.Equal(t, "missing rule dependencies: missing-rule", skipped.Reason)
	require.Contains(t, skipped.Sources, inlineSourceName)
}

func TestBuildRuleBundleSkipsInvalidExpressions(t *testing.T) {
	ctx := context.Background()
	inlineRules := map[string]RuleConfig{
		"bad-rule": {
			Conditions: RuleConditionConfig{
				Pass: []string{"1 + 1"},
			},
		},
	}

	bundle, err := buildRuleBundle(ctx, nil, inlineRules, RulesConfig{})
	require.NoError(t, err)
	require.Empty(t, bundle.Rules)
	require.Len(t, bundle.Skipped, 1)
	skipped := bundle.Skipped[0]
	require.Equal(t, "rule", skipped.Kind)
	require.Equal(t, "bad-rule", skipped.Name)
	require.Contains(t, skipped.Reason, "invalid rule expressions")
}
