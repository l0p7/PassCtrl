package rulechain

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/l0p7/passctrl/internal/templates"
)

// AuthDirectiveSpec captures the declarative rule authentication directive (match group).
type AuthDirectiveSpec struct {
	Match     []AuthMatcherSpec
	ForwardAs []AuthForwardSpec
}

// AuthMatcherSpec describes a single matcher in a match group.
type AuthMatcherSpec struct {
	Type     string
	Name     string
	Value    []string // Parsed value constraints (literal or regex patterns)
	Username []string // For basic auth
	Password []string // For basic auth
}

// AuthForwardSpec describes how a matched credential should be forwarded.
type AuthForwardSpec struct {
	Type     string
	Name     string
	Value    string
	Token    string
	User     string
	Password string
}

// AuthDirective is the compiled representation used during rule execution (match group).
type AuthDirective struct {
	Matchers []AuthMatcher
	Forwards []AuthForwardDefinition
}

// AuthMatcher is a compiled matcher with value matchers.
type AuthMatcher struct {
	Type             string
	Name             string
	MatchName        string // Lowercase for case-insensitive header matching
	ValueMatchers    []ValueMatcher
	UsernameMatchers []ValueMatcher
	PasswordMatchers []ValueMatcher
}

// ValueMatcher is the exported interface for value matching (used by runtime)
type ValueMatcher interface {
	Matches(input string) bool
}

// valueMatcher represents either a literal string or a compiled regex pattern.
type valueMatcher struct {
	literal string         // Empty if this is a regex matcher
	regex   *regexp.Regexp // Nil if this is a literal matcher
}

// Matches returns true if the input string matches this value matcher
func (vm valueMatcher) Matches(input string) bool {
	if vm.regex != nil {
		return vm.regex.MatchString(input)
	}
	return vm.literal == input
}

// AuthForwardDefinition stores the compiled forwarding configuration.
type AuthForwardDefinition struct {
	Type             string
	Name             string
	Value            string
	Token            string
	User             string
	Password         string
	NameTemplate     *templates.Template
	ValueTemplate    *templates.Template
	TokenTemplate    *templates.Template
	UserTemplate     *templates.Template
	PasswordTemplate *templates.Template
}

func compileAuthDirectives(ruleName string, specs []AuthDirectiveSpec, renderer *templates.Renderer) ([]AuthDirective, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	out := make([]AuthDirective, 0, len(specs))
	for idx, spec := range specs {
		directive, err := compileAuthDirective(ruleName, idx, spec, renderer)
		if err != nil {
			return nil, fmt.Errorf("auth[%d]: %w", idx, err)
		}
		out = append(out, directive)
	}
	return out, nil
}

func compileAuthDirective(ruleName string, index int, spec AuthDirectiveSpec, renderer *templates.Renderer) (AuthDirective, error) {
	// Compile matchers
	if len(spec.Match) == 0 {
		return AuthDirective{}, fmt.Errorf("match: at least one matcher required")
	}

	matchers := make([]AuthMatcher, 0, len(spec.Match))
	for i, matcherSpec := range spec.Match {
		matcher, err := compileAuthMatcher(matcherSpec)
		if err != nil {
			return AuthDirective{}, fmt.Errorf("match[%d]: %w", i, err)
		}
		matchers = append(matchers, matcher)
	}

	// Compile forwards
	forwards := make([]AuthForwardDefinition, 0, len(spec.ForwardAs))
	for i, fwdSpec := range spec.ForwardAs {
		fwd, err := compileAuthForward(ruleName, index, i, renderer, fwdSpec)
		if err != nil {
			return AuthDirective{}, fmt.Errorf("forwardAs[%d]: %w", i, err)
		}
		forwards = append(forwards, fwd)
	}

	return AuthDirective{
		Matchers: matchers,
		Forwards: forwards,
	}, nil
}

func compileAuthMatcher(spec AuthMatcherSpec) (AuthMatcher, error) {
	typ := strings.ToLower(strings.TrimSpace(spec.Type))
	if typ == "" {
		return AuthMatcher{}, fmt.Errorf("type required")
	}

	switch typ {
	case "basic", "bearer", "header", "query", "none":
		// Valid types
	default:
		return AuthMatcher{}, fmt.Errorf("unsupported type %q", spec.Type)
	}

	// Name required for header/query
	name := strings.TrimSpace(spec.Name)
	if (typ == "header" || typ == "query") && name == "" {
		return AuthMatcher{}, fmt.Errorf("name required for type %s", typ)
	}

	matcher := AuthMatcher{
		Type:      typ,
		Name:      name,
		MatchName: strings.ToLower(name),
	}

	var err error

	// Compile value matchers based on type
	switch typ {
	case "header", "query", "bearer":
		if len(spec.Value) > 0 {
			matcher.ValueMatchers, err = compileValueMatchers(spec.Value)
			if err != nil {
				return AuthMatcher{}, fmt.Errorf("value: %w", err)
			}
		}

	case "basic":
		if len(spec.Username) > 0 {
			matcher.UsernameMatchers, err = compileValueMatchers(spec.Username)
			if err != nil {
				return AuthMatcher{}, fmt.Errorf("username: %w", err)
			}
		}
		if len(spec.Password) > 0 {
			matcher.PasswordMatchers, err = compileValueMatchers(spec.Password)
			if err != nil {
				return AuthMatcher{}, fmt.Errorf("password: %w", err)
			}
		}

	case "none":
		// No value matchers for none type
	}

	return matcher, nil
}

func compileValueMatchers(values []string) ([]ValueMatcher, error) {
	if len(values) == 0 {
		return nil, nil
	}

	matchers := make([]ValueMatcher, len(values))
	for i, v := range values {
		matcher, err := compileValueMatcher(v)
		if err != nil {
			return nil, fmt.Errorf("[%d]: %w", i, err)
		}
		matchers[i] = matcher
	}
	return matchers, nil
}

func compileValueMatcher(value string) (valueMatcher, error) {
	// Check if this is a regex pattern (starts and ends with /)
	if strings.HasPrefix(value, "/") && strings.HasSuffix(value, "/") && len(value) > 2 {
		pattern := value[1 : len(value)-1]

		// Compile regex
		re, err := regexp.Compile(pattern)
		if err != nil {
			return valueMatcher{}, fmt.Errorf("invalid regex pattern %q: %w", value, err)
		}

		return valueMatcher{regex: re}, nil
	}

	// Literal string
	return valueMatcher{literal: value}, nil
}

func compileAuthForward(ruleName string, directiveIndex int, forwardIndex int, renderer *templates.Renderer, spec AuthForwardSpec) (AuthForwardDefinition, error) {
	forwardType := strings.ToLower(strings.TrimSpace(spec.Type))
	switch forwardType {
	case "", "basic", "bearer", "header", "query", "none":
	default:
		return AuthForwardDefinition{}, fmt.Errorf("type unsupported: %s", spec.Type)
	}

	def := AuthForwardDefinition{
		Type:     forwardType,
		Name:     strings.TrimSpace(spec.Name),
		Value:    spec.Value,
		Token:    spec.Token,
		User:     spec.User,
		Password: spec.Password,
	}

	if renderer == nil {
		return def, nil
	}

	var err error
	templateNamePrefix := fmt.Sprintf("%s:auth:%d:forward:%d", ruleName, directiveIndex, forwardIndex)

	if strings.TrimSpace(spec.Name) != "" {
		def.NameTemplate, err = renderer.CompileInline(fmt.Sprintf("%s:name", templateNamePrefix), spec.Name)
		if err != nil {
			return AuthForwardDefinition{}, fmt.Errorf("name template: %w", err)
		}
	}
	if strings.TrimSpace(spec.Value) != "" {
		def.ValueTemplate, err = renderer.CompileInline(fmt.Sprintf("%s:value", templateNamePrefix), spec.Value)
		if err != nil {
			return AuthForwardDefinition{}, fmt.Errorf("value template: %w", err)
		}
	}
	if strings.TrimSpace(spec.Token) != "" {
		def.TokenTemplate, err = renderer.CompileInline(fmt.Sprintf("%s:token", templateNamePrefix), spec.Token)
		if err != nil {
			return AuthForwardDefinition{}, fmt.Errorf("token template: %w", err)
		}
	}
	if strings.TrimSpace(spec.User) != "" {
		def.UserTemplate, err = renderer.CompileInline(fmt.Sprintf("%s:user", templateNamePrefix), spec.User)
		if err != nil {
			return AuthForwardDefinition{}, fmt.Errorf("user template: %w", err)
		}
	}
	if strings.TrimSpace(spec.Password) != "" {
		def.PasswordTemplate, err = renderer.CompileInline(fmt.Sprintf("%s:password", templateNamePrefix), spec.Password)
		if err != nil {
			return AuthForwardDefinition{}, fmt.Errorf("password template: %w", err)
		}
	}
	return def, nil
}
