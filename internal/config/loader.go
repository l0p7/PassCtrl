package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// Loader hydrates the runtime configuration while respecting env > file > default precedence.
type Loader struct {
	envPrefix string
	files     []string
}

// NewLoader prepares a config hydrator that honors the env-first contract before touching files or defaults.
func NewLoader(envPrefix string, files ...string) *Loader {
	return &Loader{
		envPrefix: envPrefix,
		files:     files,
	}
}

// Load assembles the effective snapshot so the lifecycle agent can make decisions using the documented precedence rules.
func (l *Loader) Load(ctx context.Context) (Config, error) {
	defaultCfg := DefaultConfig()
	k := koanf.New(".")

	if err := k.Load(confmap.Provider(structToMap(defaultCfg), "."), nil); err != nil {
		return Config{}, fmt.Errorf("config: load defaults: %w", err)
	}

	for _, path := range l.files {
		if path == "" {
			continue
		}
		select {
		case <-ctx.Done():
			return Config{}, ctx.Err()
		default:
		}
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return Config{}, fmt.Errorf("config: file %s not found", path)
			}
			return Config{}, fmt.Errorf("config: stat %s: %w", path, err)
		}
		if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
			return Config{}, fmt.Errorf("config: load file %s: %w", path, err)
		}
	}

	if l.envPrefix != "" {
		canonical := map[string]string{
			"server.rules.rulesfolder":             "server.rules.rulesFolder",
			"server.rules.rulesfile":               "server.rules.rulesFile",
			"server.templates.templatesfolder":     "server.templates.templatesFolder",
			"server.templates.templatesallowenv":   "server.templates.templatesAllowEnv",
			"server.templates.templatesallowedenv": "server.templates.templatesAllowedEnv",
			"server.cache.ttlseconds":              "server.cache.ttlSeconds",
			"server.cache.keysalt":                 "server.cache.keySalt",
			"server.cache.redis.tls.cafile":        "server.cache.redis.tls.caFile",
		}
		transform := func(s string) string {
			// Double underscores signal a nested path (SERVER__LISTEN__PORT -> server.listen.port).
			key := strings.TrimPrefix(s, l.envPrefix+"_")
			key = strings.ReplaceAll(key, "__", ".")
			lower := strings.ToLower(key)
			if mapped, ok := canonical[lower]; ok {
				return mapped
			}
			// Single underscores are removed so LISTEN_PORT collapses into listenport when callers
			// choose not to use double underscores for object nesting.
			key = strings.ReplaceAll(key, "_", "")
			return strings.ToLower(key)
		}
		if err := k.Load(env.Provider(l.envPrefix, ".", transform), nil); err != nil {
			return Config{}, fmt.Errorf("config: load env: %w", err)
		}
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return Config{}, fmt.Errorf("config: unmarshal: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	cfg.InlineEndpoints = cloneEndpointMap(cfg.Endpoints)
	cfg.InlineRules = cloneRuleMap(cfg.Rules)

	bundle, err := buildRuleBundle(ctx, cfg.InlineEndpoints, cfg.InlineRules, cfg.Server.Rules)
	if err != nil {
		return Config{}, err
	}
	cfg.Endpoints = bundle.Endpoints
	cfg.Rules = bundle.Rules
	cfg.RuleSources = bundle.Sources
	cfg.SkippedDefinitions = bundle.Skipped
	return cfg, nil
}

// structToMap converts DefaultConfig into a map for the koanf confmap provider.
func structToMap(cfg Config) map[string]any {
	return map[string]any{
		"server": map[string]any{
			"listen": map[string]any{
				"address": cfg.Server.Listen.Address,
				"port":    cfg.Server.Listen.Port,
			},
			"logging": map[string]any{
				"level":             cfg.Server.Logging.Level,
				"format":            cfg.Server.Logging.Format,
				"correlationHeader": cfg.Server.Logging.CorrelationHeader,
			},
			"rules": map[string]any{
				"rulesFolder": cfg.Server.Rules.RulesFolder,
				"rulesFile":   cfg.Server.Rules.RulesFile,
			},
			"templates": map[string]any{
				"templatesFolder":     cfg.Server.Templates.TemplatesFolder,
				"templatesAllowEnv":   cfg.Server.Templates.TemplatesAllowEnv,
				"templatesAllowedEnv": cfg.Server.Templates.TemplatesAllowedEnv,
			},
			"cache": map[string]any{
				"backend":    cfg.Server.Cache.Backend,
				"ttlSeconds": cfg.Server.Cache.TTLSeconds,
				"keySalt":    cfg.Server.Cache.KeySalt,
				"epoch":      cfg.Server.Cache.Epoch,
				"redis": map[string]any{
					"address":  cfg.Server.Cache.Redis.Address,
					"username": cfg.Server.Cache.Redis.Username,
					"password": cfg.Server.Cache.Redis.Password,
					"db":       cfg.Server.Cache.Redis.DB,
					"tls": map[string]any{
						"enabled": cfg.Server.Cache.Redis.TLS.Enabled,
						"caFile":  cfg.Server.Cache.Redis.TLS.CAFile,
					},
				},
			},
		},
	}
}
