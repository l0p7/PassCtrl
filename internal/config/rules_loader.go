package config

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	kjson "github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"github.com/l0p7/passctrl/internal/expr"
)

const inlineSourceName = "inline-config"

// RuleBundle captures the merged endpoint/rule definitions after loading every
// configured source. Runtime agents can use the metadata to explain what was
// loaded and why certain definitions were skipped.
type RuleBundle struct {
	Endpoints map[string]EndpointConfig
	Rules     map[string]RuleConfig
	Sources   []string
	Skipped   []DefinitionSkip
}

type ruleDocument struct {
	Endpoints map[string]EndpointConfig `koanf:"endpoints"`
	Rules     map[string]RuleConfig     `koanf:"rules"`
}

type ruleAggregator struct {
	endpoints       map[string]EndpointConfig
	endpointSources map[string]string
	endpointSkips   map[string]*DefinitionSkip

	rules       map[string]RuleConfig
	ruleSources map[string]string
	ruleSkips   map[string]*DefinitionSkip

	sources map[string]struct{}
}

func newRuleAggregator() *ruleAggregator {
	return &ruleAggregator{
		endpoints:       make(map[string]EndpointConfig),
		endpointSources: make(map[string]string),
		endpointSkips:   make(map[string]*DefinitionSkip),
		rules:           make(map[string]RuleConfig),
		ruleSources:     make(map[string]string),
		ruleSkips:       make(map[string]*DefinitionSkip),
		sources:         make(map[string]struct{}),
	}
}

func (a *ruleAggregator) addDocument(doc ruleDocument, source string) {
	if source != "" {
		a.sources[source] = struct{}{}
	}
	for name, cfg := range doc.Endpoints {
		a.addEndpoint(name, cfg, source)
	}
	for name, cfg := range doc.Rules {
		a.addRule(name, cfg, source)
	}
}

func (a *ruleAggregator) validateRuleExpressions(env *expr.Environment) {
	for name, cfg := range a.rules {
		if err := validateRuleExpressions(cfg, env); err != nil {
			source := a.ruleSources[name]
			reason := fmt.Sprintf("invalid rule expressions: %v", err)
			a.recordRuleSkip(name, reason, source)
			delete(a.ruleSources, name)
			delete(a.rules, name)
		}
	}
}

func (a *ruleAggregator) addEndpoint(name string, cfg EndpointConfig, source string) {
	if existing, ok := a.endpointSkips[name]; ok {
		existing.Sources = appendUnique(existing.Sources, source)
		return
	}
	if prev, ok := a.endpointSources[name]; ok {
		a.recordEndpointSkip(name, "duplicate definition", prev, source)
		delete(a.endpointSources, name)
		delete(a.endpoints, name)
		return
	}
	a.endpointSources[name] = source
	a.endpoints[name] = cfg
}

func (a *ruleAggregator) addRule(name string, cfg RuleConfig, source string) {
	if existing, ok := a.ruleSkips[name]; ok {
		existing.Sources = appendUnique(existing.Sources, source)
		return
	}
	if prev, ok := a.ruleSources[name]; ok {
		a.recordRuleSkip(name, "duplicate definition", prev, source)
		delete(a.ruleSources, name)
		delete(a.rules, name)
		return
	}
	a.ruleSources[name] = source
	a.rules[name] = cfg
}

func (a *ruleAggregator) recordEndpointSkip(name, reason string, sources ...string) {
	if skip, ok := a.endpointSkips[name]; ok {
		if skip.Reason == "" {
			skip.Reason = reason
		}
		for _, src := range sources {
			skip.Sources = appendUnique(skip.Sources, src)
		}
		return
	}
	skip := &DefinitionSkip{
		Kind:    "endpoint",
		Name:    name,
		Reason:  reason,
		Sources: []string{},
	}
	for _, src := range sources {
		skip.Sources = appendUnique(skip.Sources, src)
	}
	a.endpointSkips[name] = skip
}

func (a *ruleAggregator) recordRuleSkip(name, reason string, sources ...string) {
	if skip, ok := a.ruleSkips[name]; ok {
		if skip.Reason == "" {
			skip.Reason = reason
		}
		for _, src := range sources {
			skip.Sources = appendUnique(skip.Sources, src)
		}
		return
	}
	skip := &DefinitionSkip{
		Kind:    "rule",
		Name:    name,
		Reason:  reason,
		Sources: []string{},
	}
	for _, src := range sources {
		skip.Sources = appendUnique(skip.Sources, src)
	}
	a.ruleSkips[name] = skip
}

// pruneInvalidEndpoints quarantines endpoints whose rule chains reference
// definitions that never materialized. Without this guard the runtime would fail
// later in the pipeline; capturing the issue here records the offending rules in
// SkippedDefinitions so health checks can surface a precise diagnosis.
func (a *ruleAggregator) pruneInvalidEndpoints() {
	for name, cfg := range a.endpoints {
		missingSet := make(map[string]struct{})
		for _, ref := range cfg.Rules {
			if ref.Name == "" {
				continue
			}
			if _, ok := a.rules[ref.Name]; ok {
				continue
			}
			missingSet[ref.Name] = struct{}{}
		}
		if len(missingSet) == 0 {
			continue
		}
		missing := make([]string, 0, len(missingSet))
		for ruleName := range missingSet {
			missing = append(missing, ruleName)
		}
		sort.Strings(missing)
		source := a.endpointSources[name]
		reason := fmt.Sprintf("missing rule dependencies: %s", strings.Join(missing, ", "))
		a.recordEndpointSkip(name, reason, source)
		delete(a.endpointSources, name)
		delete(a.endpoints, name)
	}
}

func (a *ruleAggregator) bundle() RuleBundle {
	a.pruneInvalidEndpoints()
	endpoints := make(map[string]EndpointConfig, len(a.endpoints))
	for name, cfg := range a.endpoints {
		endpoints[name] = cfg
	}
	rules := make(map[string]RuleConfig, len(a.rules))
	for name, cfg := range a.rules {
		rules[name] = cfg
	}
	skipped := make([]DefinitionSkip, 0, len(a.endpointSkips)+len(a.ruleSkips))
	for _, skip := range a.endpointSkips {
		sort.Strings(skip.Sources)
		skipped = append(skipped, *skip)
	}
	for _, skip := range a.ruleSkips {
		sort.Strings(skip.Sources)
		skipped = append(skipped, *skip)
	}
	sort.Slice(skipped, func(i, j int) bool {
		if skipped[i].Kind == skipped[j].Kind {
			return skipped[i].Name < skipped[j].Name
		}
		return skipped[i].Kind < skipped[j].Kind
	})
	sources := make([]string, 0, len(a.sources))
	for src := range a.sources {
		if src != "" {
			sources = append(sources, src)
		}
	}
	sort.Strings(sources)
	return RuleBundle{Endpoints: endpoints, Rules: rules, Sources: sources, Skipped: skipped}
}

func appendUnique(list []string, value string) []string {
	if value == "" {
		return list
	}
	if !slices.Contains(list, value) {
		list = append(list, value)
	}
	return list
}

func buildRuleBundle(ctx context.Context, inlineEndpoints map[string]EndpointConfig, inlineRules map[string]RuleConfig, rulesCfg RulesConfig) (RuleBundle, error) {
	agg := newRuleAggregator()
	if len(inlineEndpoints) > 0 || len(inlineRules) > 0 {
		agg.addDocument(ruleDocument{Endpoints: inlineEndpoints, Rules: inlineRules}, inlineSourceName)
	}

	files, err := collectRuleSources(ctx, rulesCfg)
	if err != nil {
		return RuleBundle{}, err
	}
	for _, path := range files {
		select {
		case <-ctx.Done():
			return RuleBundle{}, ctx.Err()
		default:
		}
		doc, err := loadRuleDocument(path)
		if err != nil {
			return RuleBundle{}, err
		}
		agg.addDocument(doc, path)
	}
	env, err := expr.NewEnvironment()
	if err != nil {
		return RuleBundle{}, err
	}
	agg.validateRuleExpressions(env)
	return agg.bundle(), nil
}

func validateRuleExpressions(cfg RuleConfig, env *expr.Environment) error {
	if err := validateConditionList(env, "pass", cfg.Conditions.Pass); err != nil {
		return err
	}
	if err := validateConditionList(env, "fail", cfg.Conditions.Fail); err != nil {
		return err
	}
	if err := validateConditionList(env, "error", cfg.Conditions.Error); err != nil {
		return err
	}
	// TODO(PassCtrl-40): Implement v2 variable validation for local variables
	// cfg.Variables is now map[string]string for local variables only
	// Validation will be updated when implementing hybrid CEL/Template evaluation
	_ = cfg.Variables
	return nil
}

func validateConditionList(env *expr.Environment, name string, expressions []string) error {
	for idx, expression := range expressions {
		trimmed := strings.TrimSpace(expression)
		if trimmed == "" {
			continue
		}
		if _, err := env.Compile(trimmed); err != nil {
			return fmt.Errorf("conditions.%s[%d]: %w", name, idx, err)
		}
	}
	return nil
}

func collectRuleSources(ctx context.Context, rulesCfg RulesConfig) ([]string, error) {
	if rulesCfg.RulesFile != "" {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if err := ensureFileExists(rulesCfg.RulesFile); err != nil {
			return nil, err
		}
		return []string{rulesCfg.RulesFile}, nil
	}
	if rulesCfg.RulesFolder == "" {
		return nil, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	stat, err := os.Stat(rulesCfg.RulesFolder)
	if err != nil {
		return nil, fmt.Errorf("config: rules folder %s: %w", rulesCfg.RulesFolder, err)
	}
	if !stat.IsDir() {
		return nil, fmt.Errorf("config: rules folder %s is not a directory", rulesCfg.RulesFolder)
	}
	var files []string
	err = filepath.WalkDir(rulesCfg.RulesFolder, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !isSupportedRulesFile(path) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("config: walk rules folder %s: %w", rulesCfg.RulesFolder, err)
	}
	sort.Strings(files)
	return files, nil
}

func ensureFileExists(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("config: rules file %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("config: rules file %s: expected a file, found directory", path)
	}
	return nil
}

func loadRuleDocument(path string) (ruleDocument, error) {
	parser, err := parserFor(path)
	if err != nil {
		return ruleDocument{}, err
	}
	k := koanf.New(".")
	if err := k.Load(file.Provider(path), parser); err != nil {
		return ruleDocument{}, fmt.Errorf("config: load rules from %s: %w", path, err)
	}
	var doc ruleDocument
	if err := k.Unmarshal("", &doc); err != nil {
		return ruleDocument{}, fmt.Errorf("config: decode rules from %s: %w", path, err)
	}
	if doc.Endpoints == nil {
		doc.Endpoints = make(map[string]EndpointConfig)
	}
	if doc.Rules == nil {
		doc.Rules = make(map[string]RuleConfig)
	}
	return doc, nil
}

func parserFor(path string) (koanf.Parser, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml", ".huml":
		return yaml.Parser(), nil
	case ".json":
		return kjson.Parser(), nil
	case ".toml", ".tml":
		return toml.Parser(), nil
	default:
		return nil, fmt.Errorf("config: unsupported rules file extension %s", ext)
	}
}

func isSupportedRulesFile(path string) bool {
	_, err := parserFor(path)
	return err == nil
}

func cloneEndpointMap(in map[string]EndpointConfig) map[string]EndpointConfig {
	if len(in) == 0 {
		return nil
	}
	return maps.Clone(in)
}

func cloneRuleMap(in map[string]RuleConfig) map[string]RuleConfig {
	if len(in) == 0 {
		return nil
	}
	return maps.Clone(in)
}
