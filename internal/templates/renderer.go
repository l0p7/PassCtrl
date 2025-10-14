package templates

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	sprig "github.com/Masterminds/sprig/v3"
)

// Renderer compiles and executes templates using the configured sandbox. Inline
// templates inherit the sandbox's environment allow list while file-backed
// templates resolve paths through the sandbox root to prevent traversal.
type Renderer struct {
	sandbox *Sandbox
	funcs   template.FuncMap
}

// Template represents a compiled template ready for execution. Templates are
// safe for concurrent use.
type Template struct {
	name     string
	renderer *Renderer
	tmpl     *template.Template
}

// NewRenderer constructs a renderer bound to the provided sandbox. When the
// sandbox is nil, inline templates remain available but environment helpers
// resolve to empty strings and file-backed templates are disabled.
func NewRenderer(sandbox *Sandbox) *Renderer {
	funcs := sprig.TxtFuncMap()
	// Override environment helpers so they honor the sandbox policy rather
	// than reading from the unrestricted process environment. Remove Sprig's
	// filesystem helpers so templates cannot reach outside the sandbox via
	// readFile/readDir/glob style functions which bypass path resolution.
	restricted := []string{
		"env",
		"expandenv",
		"readDir",
		"mustReadDir",
		"readFile",
		"mustReadFile",
		"glob",
	}
	for _, name := range restricted {
		delete(funcs, name)
	}

	r := &Renderer{sandbox: sandbox, funcs: make(template.FuncMap, len(funcs)+2)}
	for name, fn := range funcs {
		r.funcs[name] = fn
	}
	r.funcs["env"] = func(key string) string {
		if r == nil || r.sandbox == nil {
			return ""
		}
		env := r.sandbox.Environment()
		return env[key]
	}
	r.funcs["expandenv"] = func(input string) string {
		if r == nil || r.sandbox == nil {
			return os.Expand(input, func(string) string { return "" })
		}
		env := r.sandbox.Environment()
		return os.Expand(input, func(key string) string { return env[key] })
	}
	return r
}

// Sandbox exposes the renderer's sandbox primarily for observability and
// testing.
func (r *Renderer) Sandbox() *Sandbox { return r.sandbox }

// CompileInline parses an inline template source. Empty or whitespace-only
// sources return nil without error to simplify optional configuration fields.
func (r *Renderer) CompileInline(name, source string) (*Template, error) {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return nil, nil
	}
	if name == "" {
		name = "inline"
	}
	tmpl, err := template.New(name).Funcs(r.funcs).Option("missingkey=zero").Parse(source)
	if err != nil {
		return nil, fmt.Errorf("templates: compile %q: %w", name, err)
	}
	return &Template{name: name, renderer: r, tmpl: tmpl}, nil
}

// CompileFile resolves and parses a template file via the sandbox. The provided
// path may be absolute or relative to the sandbox root. Attempts to escape the
// sandbox return an error.
func (r *Renderer) CompileFile(path string) (*Template, error) {
	if r == nil || r.sandbox == nil {
		return nil, errors.New("templates: file templates require a sandbox")
	}
	resolved, err := r.sandbox.Resolve(path)
	if err != nil {
		return nil, err
	}
	contents, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("templates: read %q: %w", path, err)
	}
	name := filepath.Base(resolved)
	return r.CompileInline(name, string(contents))
}

// Render executes the compiled template with the supplied data returning the
// rendered string. Errors are propagated for callers to surface or log.
func (t *Template) Render(data any) (string, error) {
	if t == nil {
		return "", errors.New("templates: nil template")
	}
	var buf bytes.Buffer
	if err := t.tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("templates: execute %q: %w", t.name, err)
	}
	return buf.String(), nil
}

// Name exposes the logical template name which callers may embed in logs.
func (t *Template) Name() string {
	if t == nil {
		return ""
	}
	return t.name
}
