package templates

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewSandboxValidatesRoot(t *testing.T) {
	sb, err := NewSandbox("", false, nil)
	require.Error(t, err)
	require.Nil(t, sb)

	dir := t.TempDir()
	sb, err = NewSandbox(dir, true, []string{"FOO"})
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(dir), sb.Root())
	require.Equal(t, []string{"FOO"}, sb.AllowedEnv())
}

func TestSandboxResolve(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "templates")
	require.NoError(t, os.MkdirAll(nested, 0o750))
	target := filepath.Join(nested, "example.tmpl")
	require.NoError(t, os.WriteFile(target, []byte("hi"), 0o600))

	sb, err := NewSandbox(nested, false, nil)
	require.NoError(t, err)

	resolved, err := sb.Resolve("example.tmpl")
	require.NoError(t, err)
	require.Equal(t, target, resolved)

	resolved, err = sb.Resolve("./sub/../example.tmpl")
	require.NoError(t, err)
	require.Equal(t, target, resolved)

	_, err = sb.Resolve("../outside")
	require.Error(t, err)
	require.Contains(t, err.Error(), "escapes")
}

func TestSandboxResolveSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require admin on Windows CI")
	}
	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "data.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("secret"), 0o600))

	link := filepath.Join(root, "link.tmpl")
	require.NoError(t, os.Symlink(outsideFile, link))

	sb, err := NewSandbox(root, false, nil)
	require.NoError(t, err)

	_, err = sb.Resolve("link.tmpl")
	require.Error(t, err)
	require.Contains(t, err.Error(), "escapes")
}

func TestSandboxEnvironment(t *testing.T) {
	dir := t.TempDir()
	sb, err := NewSandbox(dir, true, []string{"ALLOWED", "IGNORED"})
	require.NoError(t, err)
	t.Setenv("ALLOWED", "value")
	t.Setenv("IGNORED", "drop-me")

	env := sb.Environment()
	require.Len(t, env, 2)
	require.Equal(t, "value", env["ALLOWED"])
	require.Equal(t, "drop-me", env["IGNORED"])

	disabled, err := NewSandbox(dir, false, []string{"ALLOWED"})
	require.NoError(t, err)
	require.Empty(t, disabled.Environment())
}

func TestSandboxEnvironmentFiltersMissing(t *testing.T) {
	dir := t.TempDir()
	sb, err := NewSandbox(dir, true, []string{"SET", "MISSING"})
	require.NoError(t, err)
	t.Setenv("SET", "ok")

	env := sb.Environment()
	require.Len(t, env, 1)
	require.Equal(t, "ok", env["SET"])
	_, exists := env["MISSING"]
	require.False(t, exists)
}

func TestSandboxResolveNilReceiver(t *testing.T) {
	var sb *Sandbox
	_, err := sb.Resolve("anything")
	require.Error(t, err)
}

func TestSandboxResolveMissingFile(t *testing.T) {
	dir := t.TempDir()
	sb, err := NewSandbox(dir, false, nil)
	require.NoError(t, err)
	_, err = sb.Resolve("does-not-exist.tmpl")
	require.Error(t, err)
	require.ErrorIs(t, err, os.ErrNotExist)
}
