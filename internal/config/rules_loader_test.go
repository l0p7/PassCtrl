package config

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuildRuleBundleMergesSources(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	rulesFile := filepath.Join(dir, "rules.yaml")
	contents := "endpoints:\n  file-endpoint:\n    description: from file\n    rules:\n      - name: file-rule\nrules:\n  file-rule:\n    description: file rule\n"
	if err := os.WriteFile(rulesFile, []byte(contents), 0o600); err != nil {
		t.Fatalf("failed to write rules file: %v", err)
	}

	inlineEndpoints := map[string]EndpointConfig{
		"inline-endpoint": {Description: "inline"},
	}
	inlineRules := map[string]RuleConfig{
		"inline-rule": {Description: "inline"},
	}

	bundle, err := buildRuleBundle(ctx, inlineEndpoints, inlineRules, RulesConfig{RulesFile: rulesFile})
	if err != nil {
		t.Fatalf("buildRuleBundle should succeed: %v", err)
	}
	if len(bundle.Endpoints) != 2 {
		t.Fatalf("expected two endpoints, got %d", len(bundle.Endpoints))
	}
	if _, ok := bundle.Endpoints["inline-endpoint"]; !ok {
		t.Fatalf("expected inline endpoint present")
	}
	if _, ok := bundle.Endpoints["file-endpoint"]; !ok {
		t.Fatalf("expected file endpoint present")
	}
	if len(bundle.Rules) != 2 {
		t.Fatalf("expected two rules, got %d", len(bundle.Rules))
	}
	if !slices.Contains(bundle.Sources, inlineSourceName) {
		t.Fatalf("expected inline source recorded, got %v", bundle.Sources)
	}
	if !slices.Contains(bundle.Sources, filepath.Clean(rulesFile)) {
		t.Fatalf("expected file source recorded, got %v", bundle.Sources)
	}
	if len(bundle.Skipped) != 0 {
		t.Fatalf("expected no skipped definitions, got %v", bundle.Skipped)
	}
}

func TestBuildRuleBundleSkipsDuplicates(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	rulesFile := filepath.Join(dir, "rules.yaml")
	contents := "endpoints:\n  dup-endpoint:\n    description: from file\nrules:\n  dup-rule:\n    description: from file\n"
	if err := os.WriteFile(rulesFile, []byte(contents), 0o600); err != nil {
		t.Fatalf("failed to write rules file: %v", err)
	}

	inlineEndpoints := map[string]EndpointConfig{
		"dup-endpoint": {Description: "inline"},
	}
	inlineRules := map[string]RuleConfig{
		"dup-rule": {Description: "inline"},
	}

	bundle, err := buildRuleBundle(ctx, inlineEndpoints, inlineRules, RulesConfig{RulesFile: rulesFile})
	if err != nil {
		t.Fatalf("buildRuleBundle should succeed: %v", err)
	}
	if len(bundle.Endpoints) != 0 {
		t.Fatalf("expected duplicate endpoints to be skipped, got %v", bundle.Endpoints)
	}
	if len(bundle.Rules) != 0 {
		t.Fatalf("expected duplicate rules to be skipped, got %v", bundle.Rules)
	}
	if len(bundle.Skipped) != 2 {
		t.Fatalf("expected two skipped entries, got %d", len(bundle.Skipped))
	}
	for _, skip := range bundle.Skipped {
		if !slices.Contains(skip.Sources, inlineSourceName) {
			t.Fatalf("expected inline source recorded in skip: %v", skip)
		}
		if !slices.Contains(skip.Sources, filepath.Clean(rulesFile)) {
			t.Fatalf("expected file source recorded in skip: %v", skip)
		}
		if skip.Reason != "duplicate definition" {
			t.Fatalf("unexpected skip reason: %v", skip.Reason)
		}
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
	if err != nil {
		t.Fatalf("buildRuleBundle should succeed: %v", err)
	}
	if len(bundle.Endpoints) != 1 {
		t.Fatalf("expected only the healthy endpoint to remain, got %v", bundle.Endpoints)
	}
	if _, ok := bundle.Endpoints["healthy-endpoint"]; !ok {
		t.Fatalf("healthy endpoint missing after bundle")
	}
	if len(bundle.Skipped) != 1 {
		t.Fatalf("expected a single skipped endpoint, got %v", bundle.Skipped)
	}
	skipped := bundle.Skipped[0]
	if skipped.Kind != "endpoint" {
		t.Fatalf("expected endpoint skip, got %v", skipped.Kind)
	}
	if skipped.Name != "broken-endpoint" {
		t.Fatalf("expected broken endpoint to be skipped, got %v", skipped.Name)
	}
	expectedReason := "missing rule dependencies: missing-rule"
	if skipped.Reason != expectedReason {
		t.Fatalf("expected skip reason %q, got %q", expectedReason, skipped.Reason)
	}
	if !slices.Contains(skipped.Sources, inlineSourceName) {
		t.Fatalf("expected inline source recorded, got %v", skipped.Sources)
	}
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
	if err != nil {
		t.Fatalf("buildRuleBundle should succeed: %v", err)
	}
	if len(bundle.Rules) != 0 {
		t.Fatalf("expected invalid rule to be skipped, got %v", bundle.Rules)
	}
	if len(bundle.Skipped) != 1 {
		t.Fatalf("expected a single skipped rule, got %v", bundle.Skipped)
	}
	skipped := bundle.Skipped[0]
	if skipped.Kind != "rule" {
		t.Fatalf("expected rule skip, got %v", skipped.Kind)
	}
	if skipped.Name != "bad-rule" {
		t.Fatalf("expected bad-rule to be skipped, got %v", skipped.Name)
	}
	if !strings.Contains(skipped.Reason, "invalid rule expressions") {
		t.Fatalf("expected invalid expression reason, got %q", skipped.Reason)
	}
}
