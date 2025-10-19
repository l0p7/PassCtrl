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

	tests := []struct {
		name string
		env  string
		want string
	}{
		{name: "allowed variable", env: "ALLOWED", want: "visible"},
		{name: "empty variable", env: "EMPTY", want: ""},
		{name: "denied variable", env: "DENIED", want: ""},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := renderer.CompileInline("inline", "{{ env \""+tc.env+"\" }}")
			require.NoError(t, err)
			rendered, err := tmpl.Render(map[string]any{})
			require.NoError(t, err)
			require.Equal(t, tc.want, rendered)
		})
	}
}

func TestRendererCompileFileHonoursSandbox(t *testing.T) {
	dir := t.TempDir()
	allowedDir := filepath.Join(dir, "templates")
	require.NoError(t, os.MkdirAll(allowedDir, 0o750))
	allowedFile := filepath.Join(allowedDir, "body.txt")
	require.NoError(t, os.WriteFile(allowedFile, []byte("hello {{ .name }}"), 0o600))
	sandbox, err := NewSandbox(allowedDir, false, nil)
	require.NoError(t, err)
	renderer := NewRenderer(sandbox)

	tests := []struct {
		name    string
		path    string
		context map[string]any
		want    string
		wantErr bool
	}{
		{
			name:    "renders file inside sandbox",
			path:    "body.txt",
			context: map[string]any{"name": "world"},
			want:    "hello world",
		},
		{
			name:    "rejects escaping sandbox",
			path:    "../escape.txt",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := renderer.CompileFile(tc.path)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			rendered, err := tmpl.Render(tc.context)
			require.NoError(t, err)
			require.Equal(t, tc.want, rendered)
		})
	}
}

func TestRendererStripsSprigFileHelpers(t *testing.T) {
	renderer := NewRenderer(nil)

	helpers := []string{"readFile", "mustReadFile", "readDir", "mustReadDir", "glob"}
	for _, name := range helpers {
		name := name
		t.Run("removes "+name, func(t *testing.T) {
			_, ok := renderer.funcs[name]
			require.Falsef(t, ok, "expected sprig helper %q to be removed", name)
		})
	}

	t.Run("rejects removed helper", func(t *testing.T) {
		_, err := renderer.CompileInline("inline", "{{ readFile \"/etc/passwd\" }}")
		require.Error(t, err)
	})
}

func TestRendererSandboxAccessorAndTemplateName(t *testing.T) {
	dir := t.TempDir()
	sandbox, err := NewSandbox(dir, false, nil)
	require.NoError(t, err)
	renderer := NewRenderer(sandbox)

	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			name: "exposes sandbox accessor",
			check: func(t *testing.T) {
				require.Equal(t, sandbox, renderer.Sandbox())
			},
		},
		{
			name: "retains template name",
			check: func(t *testing.T) {
				tmpl, err := renderer.CompileInline("example", "static")
				require.NoError(t, err)
				require.Equal(t, "example", tmpl.Name())
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, tc.check)
	}
}
