package templates

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewSandboxValidatesRoot(t *testing.T) {
	sb, err := NewSandbox("", false, nil)
	if err == nil {
		t.Fatalf("expected error when root is empty")
	}
	if sb != nil {
		t.Fatalf("expected nil sandbox when creation fails")
	}

	dir := t.TempDir()
	sb, err = NewSandbox(dir, true, []string{"FOO"})
	if err != nil {
		t.Fatalf("unexpected error creating sandbox: %v", err)
	}
	if sb.Root() != filepath.Clean(dir) {
		t.Fatalf("expected sandbox root %q, got %q", filepath.Clean(dir), sb.Root())
	}
	if got := sb.AllowedEnv(); len(got) != 1 || got[0] != "FOO" {
		t.Fatalf("expected allowlist [FOO], got %v", got)
	}
}

func TestSandboxResolve(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "templates")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	target := filepath.Join(nested, "example.tmpl")
	if err := os.WriteFile(target, []byte("hi"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	sb, err := NewSandbox(nested, false, nil)
	if err != nil {
		t.Fatalf("sandbox create: %v", err)
	}

	resolved, err := sb.Resolve("example.tmpl")
	if err != nil {
		t.Fatalf("resolve relative: %v", err)
	}
	if resolved != target {
		t.Fatalf("expected %q, got %q", target, resolved)
	}

	resolved, err = sb.Resolve("./sub/../example.tmpl")
	if err != nil {
		t.Fatalf("resolve cleaned path: %v", err)
	}
	if resolved != target {
		t.Fatalf("expected cleaned path to resolve to %q, got %q", target, resolved)
	}

	if _, err = sb.Resolve("../outside"); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected escape detection, got %v", err)
	}
}

func TestSandboxResolveSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require admin on Windows CI")
	}
	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "data.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	link := filepath.Join(root, "link.tmpl")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	sb, err := NewSandbox(root, false, nil)
	if err != nil {
		t.Fatalf("sandbox create: %v", err)
	}

	if _, err := sb.Resolve("link.tmpl"); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected symlink escape error, got %v", err)
	}
}

func TestSandboxEnvironment(t *testing.T) {
	dir := t.TempDir()
	sb, err := NewSandbox(dir, true, []string{"ALLOWED", "IGNORED"})
	if err != nil {
		t.Fatalf("sandbox create: %v", err)
	}
	t.Setenv("ALLOWED", "value")
	t.Setenv("IGNORED", "drop-me")

	env := sb.Environment()
	if len(env) != 2 {
		t.Fatalf("expected 2 allowed entries, got %d", len(env))
	}
	if env["ALLOWED"] != "value" {
		t.Fatalf("expected ALLOWED to be set, got %q", env["ALLOWED"])
	}
	if env["IGNORED"] != "drop-me" {
		t.Fatalf("expected IGNORED to reflect process env even if unused")
	}

	disabled, err := NewSandbox(dir, false, []string{"ALLOWED"})
	if err != nil {
		t.Fatalf("sandbox create: %v", err)
	}
	if env := disabled.Environment(); len(env) != 0 {
		t.Fatalf("expected disabled sandbox to hide env, got %v", env)
	}
}

func TestSandboxEnvironmentFiltersMissing(t *testing.T) {
	dir := t.TempDir()
	sb, err := NewSandbox(dir, true, []string{"SET", "MISSING"})
	if err != nil {
		t.Fatalf("sandbox create: %v", err)
	}
	t.Setenv("SET", "ok")

	env := sb.Environment()
	if len(env) != 1 {
		t.Fatalf("expected only set variables to surface, got %v", env)
	}
	if env["SET"] != "ok" {
		t.Fatalf("expected SET to equal \"ok\", got %q", env["SET"])
	}
	if _, exists := env["MISSING"]; exists {
		t.Fatalf("expected missing env not to be exposed")
	}
}

func TestSandboxResolveNilReceiver(t *testing.T) {
	var sb *Sandbox
	if _, err := sb.Resolve("anything"); err == nil {
		t.Fatalf("expected nil sandbox to error")
	}
}

func TestSandboxResolveMissingFile(t *testing.T) {
	dir := t.TempDir()
	sb, err := NewSandbox(dir, false, nil)
	if err != nil {
		t.Fatalf("sandbox create: %v", err)
	}
	_, err = sb.Resolve("does-not-exist.tmpl")
	if err == nil {
		t.Fatalf("expected error for missing file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}
