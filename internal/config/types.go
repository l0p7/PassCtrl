package config

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Config holds every server-level option plus nested endpoint artifacts once they are loaded.
type Config struct {
	Server    ServerConfig              `koanf:"server"`
	Endpoints map[string]EndpointConfig `koanf:"endpoints"`
	Rules     map[string]RuleConfig     `koanf:"rules"`

	InlineEndpoints map[string]EndpointConfig `koanf:"-"`
	InlineRules     map[string]RuleConfig     `koanf:"-"`

	// RuleSources records which files contributed endpoint or rule definitions once
	// the loader resolves the configured sources. It is excluded from koanf so the
	// value only reflects runtime discovery rather than static input documents.
	RuleSources []string `koanf:"-"`
	// SkippedDefinitions captures duplicate or otherwise invalid definitions the
	// loader intentionally disabled. Downstream agents can surface these in health
	// checks without re-parsing raw files.
	SkippedDefinitions []DefinitionSkip `koanf:"-"`
}

// ServerConfig collects the bootstrap knobs owned by the Server Configuration & Lifecycle agent.
type ServerConfig struct {
	Listen    ListenConfig      `koanf:"listen"`
	Logging   LoggingConfig     `koanf:"logging"`
	Rules     RulesConfig       `koanf:"rules"`
	Templates TemplatesConfig   `koanf:"templates"`
	Cache     ServerCacheConfig `koanf:"cache"`
}

// ListenConfig instructs the HTTP listener about bind address and port.
type ListenConfig struct {
	Address string `koanf:"address"`
	Port    int    `koanf:"port"`
}

// LoggingConfig expresses log level, format, and correlation ID wiring.
type LoggingConfig struct {
	Level             string `koanf:"level"`
	Format            string `koanf:"format"`
	CorrelationHeader string `koanf:"correlationHeader"`
}

// RulesConfig announces how rule documents are sourced.
type RulesConfig struct {
	RulesFolder string `koanf:"rulesFolder"`
	RulesFile   string `koanf:"rulesFile"`
}

// TemplatesConfig captures the template sandbox root.
type TemplatesConfig struct {
	TemplatesFolder     string   `koanf:"templatesFolder"`
	TemplatesAllowEnv   bool     `koanf:"templatesAllowEnv"`
	TemplatesAllowedEnv []string `koanf:"templatesAllowedEnv"`
}

type ServerCacheConfig struct {
	Backend    string                 `koanf:"backend"`
	TTLSeconds int                    `koanf:"ttlSeconds"`
	KeySalt    string                 `koanf:"keySalt"`
	Epoch      int                    `koanf:"epoch"`
	Redis      ServerRedisCacheConfig `koanf:"redis"`
}

type ServerRedisCacheConfig struct {
	Address  string               `koanf:"address"`
	Username string               `koanf:"username"`
	Password string               `koanf:"password"`
	DB       int                  `koanf:"db"`
	TLS      ServerRedisTLSConfig `koanf:"tls"`
}

type ServerRedisTLSConfig struct {
	Enabled bool   `koanf:"enabled"`
	CAFile  string `koanf:"caFile"`
}

// DefinitionSkip describes a configuration artifact that the loader intentionally
// ignored because it violated invariants (for example duplicate names across
// files). Runtime agents can surface these in health checks so operators know
// which definitions were quarantined.
type DefinitionSkip struct {
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	Reason  string   `json:"reason"`
	Sources []string `json:"sources"`
}

// EndpointConfig mirrors the endpoint schema documented under design/config-structure.md.
// Each nested struct keeps the field names stable so future runtime agents can rely on a
// consistent representation even while their execution logic is still landing.
type EndpointConfig struct {
	Description          string                             `koanf:"description"`
	Variables            map[string]string                  `koanf:"variables"`
	Authentication       EndpointAuthenticationConfig       `koanf:"authentication"`
	ForwardProxyPolicy   EndpointForwardProxyPolicyConfig   `koanf:"forwardProxyPolicy"`
	ForwardRequestPolicy EndpointForwardRequestPolicyConfig `koanf:"forwardRequestPolicy"`
	Rules                []EndpointRuleReference            `koanf:"rules"`
	ResponsePolicy       EndpointResponsePolicyConfig       `koanf:"responsePolicy"`
	Cache                EndpointCacheConfig                `koanf:"cache"`
}

type EndpointAuthenticationConfig struct {
	Required  *bool                       `koanf:"required"`
	Allow     EndpointAuthAllowConfig     `koanf:"allow"`
	Challenge EndpointAuthChallengeConfig `koanf:"challenge"`
}

type EndpointAuthAllowConfig struct {
	Authorization []string `koanf:"authorization"`
	Header        []string `koanf:"header"`
	Query         []string `koanf:"query"`
	None          bool     `koanf:"none"`
}

type EndpointAuthChallengeConfig struct {
	Type    string `koanf:"type"`
	Realm   string `koanf:"realm"`
	Charset string `koanf:"charset"`
}

type EndpointForwardProxyPolicyConfig struct {
	TrustedProxyIPs []string `koanf:"trustedProxyIPs"`
	DevelopmentMode bool     `koanf:"developmentMode"`
}

type EndpointForwardRequestPolicyConfig struct {
	ForwardProxyHeaders bool                      `koanf:"forwardProxyHeaders"`
	Headers             ForwardRuleCategoryConfig `koanf:"headers"`
	Query               ForwardRuleCategoryConfig `koanf:"query"`
}

type ForwardRuleCategoryConfig struct {
	Allow  []string          `koanf:"allow"`
	Strip  []string          `koanf:"strip"`
	Custom map[string]string `koanf:"custom"`
}

type EndpointRuleReference struct {
	Name string `koanf:"name"`
}

type EndpointResponsePolicyConfig struct {
	Pass  EndpointResponseConfig `koanf:"pass"`
	Fail  EndpointResponseConfig `koanf:"fail"`
	Error EndpointResponseConfig `koanf:"error"`
}

type EndpointResponseConfig struct {
	Status   int                       `koanf:"status"`
	Body     string                    `koanf:"body"`
	BodyFile string                    `koanf:"bodyFile"`
	Headers  ForwardRuleCategoryConfig `koanf:"headers"`
}

type EndpointCacheConfig struct {
	ResultTTL string `koanf:"resultTTL"`
}

// RuleConfig captures the declarative controls available to a single rule. The
// concrete execution agents will consume this structure once implemented.
type RuleConfig struct {
	Description string              `koanf:"description"`
	Auth        []RuleAuthDirective `koanf:"auth"`
	BackendAPI  RuleBackendConfig   `koanf:"backendApi"`
	Conditions  RuleConditionConfig `koanf:"conditions"`
	Responses   RuleResponsesConfig `koanf:"responses"`
	Variables   RuleVariablesConfig `koanf:"variables"`
	Cache       RuleCacheConfig     `koanf:"cache"`
}

type RuleAuthDirective struct {
	Type      string              `koanf:"type"`
	Name      string              `koanf:"name"`
	ForwardAs RuleForwardAsConfig `koanf:"forwardAs"`
}

type RuleForwardAsConfig struct {
	Type     string `koanf:"type"`
	Token    string `koanf:"token"`
	Name     string `koanf:"name"`
	Value    string `koanf:"value"`
	User     string `koanf:"user"`
	Password string `koanf:"password"`
}

type RuleBackendConfig struct {
	URL                 string                    `koanf:"url"`
	Method              string                    `koanf:"method"`
	ForwardProxyHeaders bool                      `koanf:"forwardProxyHeaders"`
	Headers             ForwardRuleCategoryConfig `koanf:"headers"`
	Query               ForwardRuleCategoryConfig `koanf:"query"`
	Body                string                    `koanf:"body"`
	BodyFile            string                    `koanf:"bodyFile"`
	AcceptedStatuses    []int                     `koanf:"acceptedStatuses"`
	Pagination          RulePaginationConfig      `koanf:"pagination"`
}

type RulePaginationConfig struct {
	Type     string `koanf:"type"`
	MaxPages int    `koanf:"maxPages"`
}

type RuleConditionConfig struct {
	Pass  []string `koanf:"pass"`
	Fail  []string `koanf:"fail"`
	Error []string `koanf:"error"`
}

type RuleResponsesConfig struct {
	Pass  RuleResponseConfig `koanf:"pass"`
	Fail  RuleResponseConfig `koanf:"fail"`
	Error RuleResponseConfig `koanf:"error"`
}

type RuleResponseConfig struct {
	Headers   ForwardRuleCategoryConfig `koanf:"headers"`
	Variables map[string]string         `koanf:"variables"`
}

// RuleVariablesConfig defines local variables scoped to the rule.
// Variables are either CEL expressions or Go templates (detected by presence of {{).
// Local variables are available within the rule for conditions and exported variables,
// but are not exported to other rules or cached.
type RuleVariablesConfig map[string]string

type RuleVariableSpec struct {
	From string `koanf:"from"`
}

type RuleCacheConfig struct {
	FollowCacheControl bool              `koanf:"followCacheControl"`
	TTL                RuleCacheTTLConfig `koanf:"ttl"`
	Strict             *bool             `koanf:"strict"` // nil = true (default)
}

type RuleCacheTTLConfig struct {
	Pass  string `koanf:"pass"`  // Duration: "5m", "30s", etc.
	Fail  string `koanf:"fail"`  // Duration: "30s", "1m", etc.
	Error string `koanf:"error"` // Always "0s" - errors never cached
}

// IsStrict returns true if the cache key should include upstream variable hashes.
// Defaults to true (safe mode) if not explicitly set.
func (c RuleCacheConfig) IsStrict() bool {
	if c.Strict == nil {
		return true
	}
	return *c.Strict
}

// GetTTL returns the configured TTL for the given outcome.
// Error outcomes always return 0 (never cached).
func (c RuleCacheConfig) GetTTL(outcome string) time.Duration {
	if outcome == "error" {
		return 0
	}

	var durationStr string
	switch outcome {
	case "pass":
		durationStr = c.TTL.Pass
	case "fail":
		durationStr = c.TTL.Fail
	default:
		return 0
	}

	if durationStr == "" {
		return 0
	}

	duration, err := time.ParseDuration(durationStr)
	if err != nil {
		return 0
	}
	return duration
}

// Validate enforces invariants that keep the runtime predictable before serving traffic.
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("config: nil")
	}
	if c.Server.Listen.Port <= 0 || c.Server.Listen.Port > 65535 {
		return fmt.Errorf("config: listen.port invalid: %d", c.Server.Listen.Port)
	}
	if c.Server.Rules.RulesFolder != "" && c.Server.Rules.RulesFile != "" {
		return errors.New("config: rulesFolder and rulesFile are mutually exclusive")
	}
	if c.Server.Cache.TTLSeconds < 0 {
		return fmt.Errorf("config: server.cache.ttlSeconds invalid: %d", c.Server.Cache.TTLSeconds)
	}
	if c.Server.Cache.Epoch < 0 {
		return fmt.Errorf("config: server.cache.epoch invalid: %d", c.Server.Cache.Epoch)
	}
	backend := strings.TrimSpace(strings.ToLower(c.Server.Cache.Backend))
	switch backend {
	case "", "memory":
	case "redis":
		if strings.TrimSpace(c.Server.Cache.Redis.Address) == "" {
			return errors.New("config: server.cache.redis.address required for redis backend")
		}
	default:
		return fmt.Errorf("config: server.cache.backend unsupported: %s", c.Server.Cache.Backend)
	}
	for name, endpoint := range c.Endpoints {
		if err := validateEndpointAuthentication(name, endpoint.Authentication); err != nil {
			return err
		}
	}
	return nil
}

// DefaultConfig returns the baseline values that align with the design defaults.
func DefaultConfig() Config {
	return Config{
		Server: ServerConfig{
			Listen: ListenConfig{
				Address: "0.0.0.0",
				Port:    8080,
			},
			Logging: LoggingConfig{
				Level:             "info",
				Format:            "json",
				CorrelationHeader: "X-Request-ID",
			},
			Rules: RulesConfig{
				RulesFolder: "./rules",
			},
			Templates: TemplatesConfig{
				TemplatesFolder:   "./templates",
				TemplatesAllowEnv: false,
			},
			Cache: ServerCacheConfig{
				Backend:    "memory",
				TTLSeconds: 30,
				Epoch:      1,
			},
		},
	}
}

func validateEndpointAuthentication(name string, auth EndpointAuthenticationConfig) error {
	authorizationConfigured := false
	for i, provider := range auth.Allow.Authorization {
		trimmed := strings.TrimSpace(strings.ToLower(provider))
		if trimmed == "" {
			return fmt.Errorf("config: endpoint %q authentication.allow.authorization[%d] empty", name, i)
		}
		switch trimmed {
		case "basic", "bearer":
			authorizationConfigured = true
		default:
			return fmt.Errorf("config: endpoint %q authentication.allow.authorization[%d] unsupported: %s", name, i, provider)
		}
		auth.Allow.Authorization[i] = trimmed
	}
	allowConfigured := authorizationConfigured || len(auth.Allow.Header) > 0 || len(auth.Allow.Query) > 0 || auth.Allow.None
	if !allowConfigured {
		return fmt.Errorf("config: endpoint %q authentication allow block requires at least one provider", name)
	}
	for i, header := range auth.Allow.Header {
		if strings.TrimSpace(header) == "" {
			return fmt.Errorf("config: endpoint %q authentication.allow.header[%d] empty", name, i)
		}
	}
	for i, query := range auth.Allow.Query {
		if strings.TrimSpace(query) == "" {
			return fmt.Errorf("config: endpoint %q authentication.allow.query[%d] empty", name, i)
		}
	}
	challengeType := strings.TrimSpace(strings.ToLower(auth.Challenge.Type))
	if challengeType != "" {
		switch challengeType {
		case "basic", "bearer":
		default:
			return fmt.Errorf("config: endpoint %q authentication.challenge.type unsupported: %s", name, auth.Challenge.Type)
		}
		if strings.TrimSpace(auth.Challenge.Realm) == "" {
			return fmt.Errorf("config: endpoint %q authentication.challenge.realm required when type is %s", name, auth.Challenge.Type)
		}
		if challengeType == "bearer" && strings.TrimSpace(auth.Challenge.Charset) != "" {
			return fmt.Errorf("config: endpoint %q authentication.challenge.charset only supported for basic challenges", name)
		}
	}
	return nil
}
