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

	// Load environment variables using null-copy semantics
	loadedEnv, err := loadEnvironmentVariables(cfg.Server.Variables.Environment)
	if err != nil {
		return Config{}, fmt.Errorf("config: load environment variables: %w", err)
	}
	cfg.LoadedEnvironment = loadedEnv

	// Load secrets from /run/secrets using null-copy semantics
	loadedSecrets, err := loadSecrets(cfg.Server.Variables.Secrets)
	if err != nil {
		return Config{}, fmt.Errorf("config: load secrets: %w", err)
	}
	cfg.LoadedSecrets = loadedSecrets

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
				"templatesFolder": cfg.Server.Templates.TemplatesFolder,
			},
			"variables": map[string]any{
				"environment": cfg.Server.Variables.Environment,
				"secrets":     cfg.Server.Variables.Secrets,
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

// loadEnvironmentVariables loads environment variables from the config using null-copy semantics:
// - key: null → read env var with exact name `key`
// - key: "ENV_VAR" → read env var `ENV_VAR`, expose as `variables.environment.key`
// Returns an error if any referenced environment variable is missing.
func loadEnvironmentVariables(envConfig map[string]*string) (map[string]string, error) {
	if len(envConfig) == 0 {
		return make(map[string]string), nil
	}

	result := make(map[string]string, len(envConfig))
	for key, valuePtr := range envConfig {
		var envVarName string
		if valuePtr == nil {
			// Null-copy: read env var with exact name as key
			envVarName = key
		} else {
			// Read env var specified in value
			envVarName = *valuePtr
		}

		value, exists := os.LookupEnv(envVarName)
		if !exists {
			return nil, fmt.Errorf("environment variable %q not found (referenced by server.variables.environment.%s)", envVarName, key)
		}
		result[key] = value
	}
	return result, nil
}

// loadSecrets loads secret file contents from /run/secrets/ using null-copy semantics:
// - key: null → read /run/secrets/key, expose as `variables.secrets.key`
// - key: "filename" → read /run/secrets/filename, expose as `variables.secrets.key`
// Returns an error if any referenced secret file is missing or unreadable.
func loadSecrets(secretsConfig map[string]*string) (map[string]string, error) {
	if len(secretsConfig) == 0 {
		return make(map[string]string), nil
	}

	const secretsDir = "/run/secrets"
	result := make(map[string]string, len(secretsConfig))

	for key, valuePtr := range secretsConfig {
		var filename string
		if valuePtr == nil {
			// Null-copy: read secret file with exact name as key
			filename = key
		} else {
			// Read secret file specified in value
			filename = *valuePtr
		}

		secretPath := fmt.Sprintf("%s/%s", secretsDir, filename)
		content, err := os.ReadFile(secretPath) // #nosec G304 -- reading Docker secrets from /run/secrets with configured filenames
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("secret file %q not found (referenced by server.variables.secrets.%s)", secretPath, key)
			}
			return nil, fmt.Errorf("failed to read secret file %q (referenced by server.variables.secrets.%s): %w", secretPath, key, err)
		}

		// Trim trailing newline that Docker adds to secret files
		result[key] = strings.TrimRight(string(content), "\n\r")
	}
	return result, nil
}
