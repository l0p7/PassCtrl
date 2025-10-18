package templates

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRendererInlineEnvAllowlist(t *testing.T) {
	dir := t.TempDir()
	sandbox, err := NewSandbox(dir, true, []string{"ALLOWED", "EMPTY"})
	if err != nil {
		t.Fatalf("sandbox create: %v", err)
	}
	t.Setenv("ALLOWED", "visible")
	t.Setenv("EMPTY", "")
	t.Setenv("DENIED", "hidden")

	renderer := NewRenderer(sandbox)
	tmpl, err := renderer.CompileInline("inline", "{{ env \"ALLOWED\" }} {{ env \"DENIED\" }}")
	if err != nil {
		t.Fatalf("compile inline: %v", err)
	}
	rendered, err := tmpl.Render(map[string]any{})
	if err != nil {
		t.Fatalf("render inline: %v", err)
	}
	if rendered != "visible " {
		t.Fatalf("unexpected render output: %q", rendered)
	}
}

func TestRendererCompileFileHonoursSandbox(t *testing.T) {
	dir := t.TempDir()
	allowedDir := filepath.Join(dir, "templates")
	if err := os.MkdirAll(allowedDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := filepath.Join(allowedDir, "body.txt")
	if err := os.WriteFile(file, []byte("hello {{ .name }}"), 0o600); err != nil {
		t.Fatalf("write template: %v", err)
	}
	sandbox, err := NewSandbox(allowedDir, false, nil)
	if err != nil {
		t.Fatalf("sandbox create: %v", err)
	}
	renderer := NewRenderer(sandbox)

	tmpl, err := renderer.CompileFile("body.txt")
	if err != nil {
		t.Fatalf("compile file: %v", err)
	}
	rendered, err := tmpl.Render(map[string]any{"name": "world"})
	if err != nil {
		t.Fatalf("render file: %v", err)
	}
	if rendered != "hello world" {
		t.Fatalf("unexpected render output: %q", rendered)
	}

	if _, err := renderer.CompileFile("../escape.txt"); err == nil {
		t.Fatalf("expected escape attempt to fail")
	}
}

func TestRendererStripsSprigFileHelpers(t *testing.T) {
	renderer := NewRenderer(nil)

	for _, name := range []string{"readFile", "mustReadFile", "readDir", "mustReadDir", "glob"} {
		if _, ok := renderer.funcs[name]; ok {
			t.Fatalf("expected sprig helper %q to be removed", name)
		}
	}

	if _, err := renderer.CompileInline("inline", "{{ readFile \"/etc/passwd\" }}"); err == nil {
		t.Fatalf("expected readFile usage to fail")
	}
}

func TestRendererSandboxAccessorAndTemplateName(t *testing.T) {
	dir := t.TempDir()
	sandbox, err := NewSandbox(dir, false, nil)
	if err != nil {
		t.Fatalf("sandbox create: %v", err)
	}
	renderer := NewRenderer(sandbox)
	if renderer.Sandbox() != sandbox {
		t.Fatalf("expected sandbox accessor to return underlying sandbox")
	}

	tmpl, err := renderer.CompileInline("example", "static")
	if err != nil {
		t.Fatalf("compile inline: %v", err)
	}
	if tmpl.Name() != "example" {
		t.Fatalf("expected template name to be preserved, got %q", tmpl.Name())
	}
}
