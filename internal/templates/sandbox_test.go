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
		name     string
		root     string
		allowEnv []string
		wantErr  bool
		assert   func(t *testing.T, sb *Sandbox)
	}{
		{
			name:    "rejects empty root",
			root:    "",
			wantErr: true,
		},
		{
			name:     "initializes sandbox",
			root:     t.TempDir(),
			allowEnv: []string{"FOO"},
			assert: func(t *testing.T, sb *Sandbox) {
				require.Equal(t, []string{"FOO"}, sb.AllowedEnv())
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			sb, err := NewSandbox(tc.root, false, tc.allowEnv)
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, sb)
				return
			}
			require.NoError(t, err)
			require.Equal(t, filepath.Clean(tc.root), sb.Root())
			if tc.assert != nil {
				tc.assert(t, sb)
			}
		})
	}
}

func TestSandboxResolve(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "templates")
	require.NoError(t, os.MkdirAll(nested, 0o750))
	target := filepath.Join(nested, "example.tmpl")
	require.NoError(t, os.WriteFile(target, []byte("hi"), 0o600))

	sb, err := NewSandbox(nested, false, nil)
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

	sb, err := NewSandbox(root, false, nil)
	require.NoError(t, err)

	_, err = sb.Resolve("link.tmpl")
	require.Error(t, err)
	require.Contains(t, err.Error(), "escapes")
}

func TestSandboxEnvironment(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ALLOWED", "value")
	t.Setenv("IGNORED", "drop-me")

	tests := []struct {
		name     string
		enabled  bool
		allowEnv []string
		wantLen  int
	}{
		{name: "returns configured environment", enabled: true, allowEnv: []string{"ALLOWED", "IGNORED"}, wantLen: 2},
		{name: "disabled environment", enabled: false, allowEnv: []string{"ALLOWED"}, wantLen: 0},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			sb, err := NewSandbox(dir, tc.enabled, tc.allowEnv)
			require.NoError(t, err)
			env := sb.Environment()
			require.Len(t, env, tc.wantLen)
			if tc.wantLen > 0 {
				require.Equal(t, "value", env["ALLOWED"])
				require.Equal(t, "drop-me", env["IGNORED"])
			}
		})
	}
}

func TestSandboxEnvironmentFiltersMissing(t *testing.T) {
	dir := t.TempDir()
	sb, err := NewSandbox(dir, true, []string{"SET", "MISSING"})
	require.NoError(t, err)
	t.Setenv("SET", "ok")

	env := sb.Environment()
	require.Len(t, env, 1)

	tests := []struct {
		name      string
		key       string
		wantValue string
		wantFound bool
	}{
		{name: "returns set value", key: "SET", wantValue: "ok", wantFound: true},
		{name: "omits missing value", key: "MISSING", wantFound: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			value, found := env[tc.key]
			require.Equal(t, tc.wantFound, found)
			if tc.wantFound {
				require.Equal(t, tc.wantValue, value)
			}
		})
	}
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
	sb, err := NewSandbox(dir, false, nil)
	require.NoError(t, err)
	t.Run("returns not exist error", func(t *testing.T) {
		_, err = sb.Resolve("does-not-exist.tmpl")
		require.Error(t, err)
		require.ErrorIs(t, err, os.ErrNotExist)
	})
}
