package templates

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewSandboxValidatesRoot(t *testing.T) {
	tests := []struct {
		name    string
		root    string
		wantErr bool
	}{
		{
			name:    "rejects empty root",
			root:    "",
			wantErr: true,
		},
		{
			name: "initializes sandbox",
			root: t.TempDir(),
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			sb, err := NewSandbox(tc.root)
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, sb)
				return
			}
			require.NoError(t, err)
			require.Equal(t, filepath.Clean(tc.root), sb.Root())
		})
	}
}

func TestSandboxResolve(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "templates")
	require.NoError(t, os.MkdirAll(nested, 0o750))
	target := filepath.Join(nested, "example.tmpl")
	require.NoError(t, os.WriteFile(target, []byte("hi"), 0o600))

	sb, err := NewSandbox(nested)
	require.NoError(t, err)

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "resolves file", input: "example.tmpl", want: target},
		{name: "normalises relative path", input: "./sub/../example.tmpl", want: target},
		{name: "rejects escape", input: "../outside", wantErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resolved, err := sb.Resolve(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "escapes")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, resolved)
		})
	}
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

	sb, err := NewSandbox(root)
	require.NoError(t, err)

	_, err = sb.Resolve("link.tmpl")
	require.Error(t, err)
	require.Contains(t, err.Error(), "escapes")
}

func TestSandboxResolveNilReceiver(t *testing.T) {
	t.Run("nil receiver returns error", func(t *testing.T) {
		var sb *Sandbox
		_, err := sb.Resolve("anything")
		require.Error(t, err)
	})
}

func TestSandboxResolveMissingFile(t *testing.T) {
	dir := t.TempDir()
	sb, err := NewSandbox(dir)
	require.NoError(t, err)
	t.Run("returns not exist error", func(t *testing.T) {
		_, err = sb.Resolve("does-not-exist.tmpl")
		require.Error(t, err)
		require.ErrorIs(t, err, os.ErrNotExist)
	})
}
