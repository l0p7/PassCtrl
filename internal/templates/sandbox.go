package templates

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Sandbox enforces the template security constraints described in the design
// documents by constraining filesystem lookups to a configured root.
type Sandbox struct {
	root string
}

// NewSandbox initializes a sandbox rooted at the provided directory. The root
// must exist and be a directory so path validation can reliably guard against
// escape attempts via ".." or symlinks.
func NewSandbox(root string) (*Sandbox, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("templates: sandbox root required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("templates: resolve root: %w", err)
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("templates: eval root symlinks: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("templates: stat root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("templates: root %q is not a directory", abs)
	}
	return &Sandbox{root: abs}, nil
}

// Root returns the canonical sandbox directory, primarily for observability and
// testing.
func (s *Sandbox) Root() string { return s.root }

// Resolve normalizes the provided template path ensuring it is contained within
// the sandbox root. Both relative and absolute paths are supported as long as
// the resulting location does not escape the sandbox.
func (s *Sandbox) Resolve(path string) (string, error) {
	if s == nil {
		return "", errors.New("templates: sandbox is nil")
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." || cleaned == "" {
		return s.root, nil
	}
	if !filepath.IsAbs(cleaned) {
		cleaned = filepath.Join(s.root, cleaned)
	}
	cleaned = filepath.Clean(cleaned)
	evaluated, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Even when the target does not exist we still want to guard against
			// traversal. Use the cleaned path for the rel check and surface the
			// original error to callers.
			if !s.contains(cleaned) {
				return "", fmt.Errorf("templates: path %q escapes sandbox", path)
			}
			return "", fmt.Errorf("templates: resolve %q: %w", path, err)
		}
		return "", fmt.Errorf("templates: resolve %q: %w", path, err)
	}
	if !s.contains(evaluated) {
		return "", fmt.Errorf("templates: path %q escapes sandbox", path)
	}
	return evaluated, nil
}

// contains reports whether the provided absolute path is inside the sandbox.
func (s *Sandbox) contains(candidate string) bool {
	sandbox := s.root
	if runtime.GOOS == "windows" {
		sandbox = strings.ToLower(sandbox)
		candidate = strings.ToLower(candidate)
	}
	if sandbox == candidate {
		return true
	}
	if !strings.HasSuffix(sandbox, string(os.PathSeparator)) {
		sandbox += string(os.PathSeparator)
	}
	return strings.HasPrefix(candidate, sandbox)
}
