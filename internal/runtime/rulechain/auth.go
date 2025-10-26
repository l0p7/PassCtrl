package rulechain

import (
	"fmt"
	"strings"

	"github.com/l0p7/passctrl/internal/templates"
)

// AuthDirectiveSpec captures the declarative rule authentication directive.
type AuthDirectiveSpec struct {
	Type    string
	Name    string
	Forward AuthForwardSpec
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

// AuthDirective is the compiled representation used during rule execution.
type AuthDirective struct {
	Type      string
	Name      string
	matchName string
	Forward   AuthForwardDefinition
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
		typ := strings.ToLower(strings.TrimSpace(spec.Type))
		switch typ {
		case "":
			return nil, fmt.Errorf("auth[%d]: type required", idx)
		case "basic", "bearer", "header", "query", "none":
		default:
			return nil, fmt.Errorf("auth[%d]: unsupported type %q", idx, spec.Type)
		}

		name := strings.TrimSpace(spec.Name)
		if (typ == "header" || typ == "query") && name == "" {
			return nil, fmt.Errorf("auth[%d]: name required for type %s", idx, typ)
		}

		forward, err := compileAuthForward(ruleName, idx, renderer, spec.Forward)
		if err != nil {
			return nil, fmt.Errorf("auth[%d]: %w", idx, err)
		}

		out = append(out, AuthDirective{
			Type:      typ,
			Name:      name,
			matchName: strings.ToLower(name),
			Forward:   forward,
		})
	}
	return out, nil
}

func compileAuthForward(ruleName string, index int, renderer *templates.Renderer, spec AuthForwardSpec) (AuthForwardDefinition, error) {
	forwardType := strings.ToLower(strings.TrimSpace(spec.Type))
	switch forwardType {
	case "", "basic", "bearer", "header", "query", "none":
	default:
		return AuthForwardDefinition{}, fmt.Errorf("forwardAs.type unsupported: %s", spec.Type)
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
	if strings.TrimSpace(spec.Name) != "" {
		def.NameTemplate, err = renderer.CompileInline(fmt.Sprintf("%s:auth:%d:forward:name", ruleName, index), spec.Name)
		if err != nil {
			return AuthForwardDefinition{}, fmt.Errorf("forwardAs.name template: %w", err)
		}
	}
	if strings.TrimSpace(spec.Value) != "" {
		def.ValueTemplate, err = renderer.CompileInline(fmt.Sprintf("%s:auth:%d:forward:value", ruleName, index), spec.Value)
		if err != nil {
			return AuthForwardDefinition{}, fmt.Errorf("forwardAs.value template: %w", err)
		}
	}
	if strings.TrimSpace(spec.Token) != "" {
		def.TokenTemplate, err = renderer.CompileInline(fmt.Sprintf("%s:auth:%d:forward:token", ruleName, index), spec.Token)
		if err != nil {
			return AuthForwardDefinition{}, fmt.Errorf("forwardAs.token template: %w", err)
		}
	}
	if strings.TrimSpace(spec.User) != "" {
		def.UserTemplate, err = renderer.CompileInline(fmt.Sprintf("%s:auth:%d:forward:user", ruleName, index), spec.User)
		if err != nil {
			return AuthForwardDefinition{}, fmt.Errorf("forwardAs.user template: %w", err)
		}
	}
	if strings.TrimSpace(spec.Password) != "" {
		def.PasswordTemplate, err = renderer.CompileInline(fmt.Sprintf("%s:auth:%d:forward:password", ruleName, index), spec.Password)
		if err != nil {
			return AuthForwardDefinition{}, fmt.Errorf("forwardAs.password template: %w", err)
		}
	}
	return def, nil
}
