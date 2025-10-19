package templates

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRendererInlineEnvAllowlist(t *testing.T) {
	dir := t.TempDir()
	sandbox, err := NewSandbox(dir, true, []string{"ALLOWED", "EMPTY"})
	require.NoError(t, err)
	t.Setenv("ALLOWED", "visible")
	t.Setenv("EMPTY", "")
	t.Setenv("DENIED", "hidden")

	renderer := NewRenderer(sandbox)
	tmpl, err := renderer.CompileInline("inline", "{{ env \"ALLOWED\" }} {{ env \"DENIED\" }}")
	require.NoError(t, err)
	rendered, err := tmpl.Render(map[string]any{})
	require.NoError(t, err)
	require.Equal(t, "visible ", rendered)
}

func TestRendererCompileFileHonoursSandbox(t *testing.T) {
	dir := t.TempDir()
	allowedDir := filepath.Join(dir, "templates")
	require.NoError(t, os.MkdirAll(allowedDir, 0o750))
	file := filepath.Join(allowedDir, "body.txt")
	require.NoError(t, os.WriteFile(file, []byte("hello {{ .name }}"), 0o600))
	sandbox, err := NewSandbox(allowedDir, false, nil)
	require.NoError(t, err)
	renderer := NewRenderer(sandbox)

	tmpl, err := renderer.CompileFile("body.txt")
	require.NoError(t, err)
	rendered, err := tmpl.Render(map[string]any{"name": "world"})
	require.NoError(t, err)
	require.Equal(t, "hello world", rendered)

	_, err = renderer.CompileFile("../escape.txt")
	require.Error(t, err)
}

func TestRendererStripsSprigFileHelpers(t *testing.T) {
	renderer := NewRenderer(nil)

	for _, name := range []string{"readFile", "mustReadFile", "readDir", "mustReadDir", "glob"} {
		_, ok := renderer.funcs[name]
		require.Falsef(t, ok, "expected sprig helper %q to be removed", name)
	}

	_, err := renderer.CompileInline("inline", "{{ readFile \"/etc/passwd\" }}")
	require.Error(t, err)
}

func TestRendererSandboxAccessorAndTemplateName(t *testing.T) {
	dir := t.TempDir()
	sandbox, err := NewSandbox(dir, false, nil)
	require.NoError(t, err)
	renderer := NewRenderer(sandbox)
	require.Equal(t, sandbox, renderer.Sandbox())

	tmpl, err := renderer.CompileInline("example", "static")
	require.NoError(t, err)
	require.Equal(t, "example", tmpl.Name())
}
