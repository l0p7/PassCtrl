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

	// LoadedEnvironment contains the environment variables loaded from server.variables.environment
	// using null-copy semantics. This is populated at config load time and made available
	// as variables.environment.* in CEL and templates.
	LoadedEnvironment map[string]string `koanf:"-"`

	// LoadedSecrets contains the secret file contents loaded from server.variables.secrets
	// using null-copy semantics. Files are read from /run/secrets/ directory. This is populated
	// at config load time and made available as variables.secrets.* in CEL and templates.
	LoadedSecrets map[string]string `koanf:"-"`
}

// ServerConfig collects the bootstrap knobs owned by the Server Configuration & Lifecycle agent.
type ServerConfig struct {
	Listen    ListenConfig          `koanf:"listen"`
	Logging   LoggingConfig         `koanf:"logging"`
	Rules     RulesConfig           `koanf:"rules"`
	Templates TemplatesConfig       `koanf:"templates"`
	Cache     ServerCacheConfig     `koanf:"cache"`
	Variables ServerVariablesConfig `koanf:"variables"`
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
	TemplatesFolder string `koanf:"templatesFolder"`
}

// ServerVariablesConfig controls server-level variables exposed to all endpoints and rules.
type ServerVariablesConfig struct {
	// Environment maps variable names to environment variable names using null-copy semantics:
	// - key: null → read env var with exact name `key`
	// - key: "ENV_VAR" → read env var `ENV_VAR`, expose as `variables.environment.key`
	// Missing env vars cause startup errors.
	Environment map[string]*string `koanf:"environment"`

	// Secrets maps variable names to secret file names in /run/secrets/ using null-copy semantics:
	// - key: null → read /run/secrets/key, expose as `variables.secrets.key`
	// - key: "filename" → read /run/secrets/filename, expose as `variables.secrets.key`
	// Missing secret files cause startup errors.
	Secrets map[string]*string `koanf:"secrets"`
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
	Response  *EndpointAuthResponseConfig `koanf:"response"`
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

// EndpointAuthResponseConfig customizes the response rendered on admission failure.
type EndpointAuthResponseConfig struct {
	Status   int               `koanf:"status"`
	Headers  map[string]string `koanf:"headers"`
	Body     string            `koanf:"body"`
	BodyFile string            `koanf:"bodyFile"`
}

type EndpointForwardProxyPolicyConfig struct {
	TrustedProxyIPs []string `koanf:"trustedProxyIPs"`
	DevelopmentMode bool     `koanf:"developmentMode"`
}

type EndpointForwardRequestPolicyConfig struct {
	ForwardProxyHeaders bool `koanf:"forwardProxyHeaders"`
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
	Status   int                `koanf:"status"`
	Body     string             `koanf:"body"`
	BodyFile string             `koanf:"bodyFile"`
	Headers  map[string]*string `koanf:"headers"`
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
	Match     []RuleAuthMatcher     `koanf:"match"`
	ForwardAs []RuleForwardAsConfig `koanf:"forwardAs"`
}

type RuleAuthMatcher struct {
	Type     string `koanf:"type"`     // basic|bearer|header|query|none
	Name     string `koanf:"name"`     // Required for header/query
	Value    any    `koanf:"value"`    // string or []string - for header/query/bearer (regex or literal)
	Username any    `koanf:"username"` // string or []string - for basic
	Password any    `koanf:"password"` // string or []string - for basic
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
	URL                 string               `koanf:"url"`
	Method              string               `koanf:"method"`
	ForwardProxyHeaders bool                 `koanf:"forwardProxyHeaders"`
	Headers             map[string]*string   `koanf:"headers"`
	Query               map[string]*string   `koanf:"query"`
	Body                string               `koanf:"body"`
	BodyFile            string               `koanf:"bodyFile"`
	AcceptedStatuses    []int                `koanf:"acceptedStatuses"`
	Pagination          RulePaginationConfig `koanf:"pagination"`
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
	Variables map[string]string `koanf:"variables"` // Exported to subsequent rules AND endpoint response templates
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
	FollowCacheControl bool               `koanf:"followCacheControl"`
	TTL                RuleCacheTTLConfig `koanf:"ttl"`
	Strict             *bool              `koanf:"strict"`             // nil = true (default)
	IncludeProxyHeaders *bool             `koanf:"includeProxyHeaders"` // nil = true (safe default)
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

// validateCacheTTLConfig validates that TTL duration strings are parseable.
func validateCacheTTLConfig(ttl RuleCacheTTLConfig, context string) error {
	if ttl.Pass != "" {
		if _, err := time.ParseDuration(ttl.Pass); err != nil {
			return fmt.Errorf("%s.pass: invalid duration %q: %w", context, ttl.Pass, err)
		}
	}
	if ttl.Fail != "" {
		if _, err := time.ParseDuration(ttl.Fail); err != nil {
			return fmt.Errorf("%s.fail: invalid duration %q: %w", context, ttl.Fail, err)
		}
	}
	if ttl.Error != "" {
		if _, err := time.ParseDuration(ttl.Error); err != nil {
			return fmt.Errorf("%s.error: invalid duration %q: %w", context, ttl.Error, err)
		}
	}
	return nil
}

// validateVariableMap validates variable expressions (CEL or Template).
// Variables can be empty (validation is lenient - runtime will catch evaluation errors).
func validateVariableMap(variables map[string]string, context string) error {
	// Variable expressions are validated at runtime during compilation
	// This validation just ensures the map structure is valid
	// Empty expressions are allowed (will be caught during rule compilation)
	for name, expr := range variables {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%s: empty variable name not allowed", context)
		}
		// Expression can be empty or whitespace - runtime will handle it
		_ = expr
	}
	return nil
}

// ParseValueConstraint converts the raw value constraint (string or []any) into []string.
func ParseValueConstraint(raw any, context string) ([]string, error) {
	if raw == nil {
		return nil, nil
	}

	switch v := raw.(type) {
	case string:
		return []string{v}, nil
	case []any:
		if len(v) == 0 {
			return nil, fmt.Errorf("%s: value constraint array cannot be empty", context)
		}
		strs := make([]string, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s: value constraint array element %d is not a string", context, i)
			}
			strs[i] = s
		}
		return strs, nil
	default:
		return nil, fmt.Errorf("%s: value constraint must be string or array of strings", context)
	}
}

// validateAuthDirective validates a single auth directive (match group + forwardAs).
func validateAuthDirective(directive RuleAuthDirective, index int, context string) error {
	directiveCtx := fmt.Sprintf("%s.auth[%d]", context, index)

	// Validate match array
	if len(directive.Match) == 0 {
		return fmt.Errorf("%s.match: at least one matcher required", directiveCtx)
	}

	for i, matcher := range directive.Match {
		if err := validateAuthMatcher(matcher, i, directiveCtx); err != nil {
			return err
		}
	}

	// Check for type: none restrictions
	hasNone := false
	hasOther := false
	for _, matcher := range directive.Match {
		if strings.ToLower(strings.TrimSpace(matcher.Type)) == "none" {
			hasNone = true
		} else {
			hasOther = true
		}
	}
	if hasNone && hasOther {
		return fmt.Errorf("%s.match: type 'none' cannot be combined with other types in the same match group", directiveCtx)
	}
	if hasNone && len(directive.Match) > 1 {
		return fmt.Errorf("%s.match: type 'none' must be the only matcher in the group", directiveCtx)
	}

	// Validate forwardAs array (check for duplicates)
	if err := validateForwardAsArray(directive.ForwardAs, directiveCtx); err != nil {
		return err
	}

	return nil
}

// validateAuthMatcher validates a single matcher in a match group.
func validateAuthMatcher(matcher RuleAuthMatcher, index int, parentContext string) error {
	matcherCtx := fmt.Sprintf("%s.match[%d]", parentContext, index)

	// Type required
	if strings.TrimSpace(matcher.Type) == "" {
		return fmt.Errorf("%s.type: required", matcherCtx)
	}

	typ := strings.ToLower(strings.TrimSpace(matcher.Type))
	switch typ {
	case "basic", "bearer", "header", "query", "none":
		// Valid types
	default:
		return fmt.Errorf("%s.type: unsupported type %q", matcherCtx, matcher.Type)
	}

	// Name required for header/query
	if (typ == "header" || typ == "query") && strings.TrimSpace(matcher.Name) == "" {
		return fmt.Errorf("%s.name: required for type %s", matcherCtx, typ)
	}

	// Validate value constraint applicability
	switch typ {
	case "header", "query", "bearer":
		if matcher.Username != nil {
			return fmt.Errorf("%s.username: constraint not valid for type %s", matcherCtx, typ)
		}
		if matcher.Password != nil {
			return fmt.Errorf("%s.password: constraint not valid for type %s", matcherCtx, typ)
		}
		// Validate value constraint structure
		if matcher.Value != nil {
			if _, err := ParseValueConstraint(matcher.Value, matcherCtx+".value"); err != nil {
				return err
			}
		}

	case "basic":
		if matcher.Value != nil {
			return fmt.Errorf("%s.value: constraint not valid for basic (use username/password)", matcherCtx)
		}
		// Validate username constraint structure
		if matcher.Username != nil {
			if _, err := ParseValueConstraint(matcher.Username, matcherCtx+".username"); err != nil {
				return err
			}
		}
		// Validate password constraint structure
		if matcher.Password != nil {
			if _, err := ParseValueConstraint(matcher.Password, matcherCtx+".password"); err != nil {
				return err
			}
		}

	case "none":
		if matcher.Value != nil || matcher.Username != nil || matcher.Password != nil {
			return fmt.Errorf("%s: value constraints not valid for type none", matcherCtx)
		}
	}

	return nil
}

// validateForwardAsArray checks for duplicate targets in forwardAs array.
func validateForwardAsArray(forwards []RuleForwardAsConfig, context string) error {
	if len(forwards) == 0 {
		// Empty forwardAs is valid (pass-through mode)
		return nil
	}

	seen := make(map[string]bool)
	for i, fwd := range forwards {
		key := buildForwardKey(fwd)
		if key == "" {
			return fmt.Errorf("%s.forwardAs[%d]: invalid forward type", context, i)
		}
		if seen[key] {
			return fmt.Errorf("%s.forwardAs[%d]: duplicate forward target %s", context, i, key)
		}
		seen[key] = true
	}

	return nil
}

// buildForwardKey creates a unique key for a forward target to detect duplicates.
func buildForwardKey(fwd RuleForwardAsConfig) string {
	typ := strings.ToLower(strings.TrimSpace(fwd.Type))
	switch typ {
	case "bearer", "basic":
		return "authorization"
	case "header":
		return "header:" + strings.ToLower(strings.TrimSpace(fwd.Name))
	case "query":
		return "query:" + strings.TrimSpace(fwd.Name)
	case "none":
		return "none"
	default:
		return ""
	}
}

// validateBackendHeaders ensures authorization header is not specified in backend config.
// Authorization must be handled through the auth block for proper credential stripping.
func validateBackendHeaders(headers map[string]*string, context string) error {
	for name := range headers {
		if strings.ToLower(strings.TrimSpace(name)) == "authorization" {
			return fmt.Errorf("%s: authorization header forbidden - use auth.forwardAs instead", context)
		}
	}
	return nil
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
		// Validate endpoint variables (CEL or Template expressions)
		if err := validateVariableMap(endpoint.Variables, fmt.Sprintf("endpoints[%s].variables", name)); err != nil {
			return err
		}
	}
	for name, rule := range c.Rules {
		// Validate rule auth directives (match groups + forwardAs)
		for i, authDirective := range rule.Auth {
			if err := validateAuthDirective(authDirective, i, fmt.Sprintf("rules[%s]", name)); err != nil {
				return err
			}
		}
		// Validate backend headers (forbid authorization header)
		if err := validateBackendHeaders(rule.BackendAPI.Headers, fmt.Sprintf("rules[%s].backendApi.headers", name)); err != nil {
			return err
		}
		// Validate rule cache TTL durations
		if err := validateCacheTTLConfig(rule.Cache.TTL, fmt.Sprintf("rules[%s].cache.ttl", name)); err != nil {
			return err
		}
		// Validate rule local variables (CEL or Template expressions)
		if err := validateVariableMap(rule.Variables, fmt.Sprintf("rules[%s].variables", name)); err != nil {
			return err
		}
		// Validate response exported variables
		if err := validateVariableMap(rule.Responses.Pass.Variables, fmt.Sprintf("rules[%s].responses.pass.variables", name)); err != nil {
			return err
		}
		if err := validateVariableMap(rule.Responses.Fail.Variables, fmt.Sprintf("rules[%s].responses.fail.variables", name)); err != nil {
			return err
		}
		if err := validateVariableMap(rule.Responses.Error.Variables, fmt.Sprintf("rules[%s].responses.error.variables", name)); err != nil {
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
				TemplatesFolder: "./templates",
			},
			Variables: ServerVariablesConfig{
				Environment: make(map[string]*string),
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
