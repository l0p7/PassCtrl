package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWatchRulesFileReloads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	rulesFile := filepath.Join(dir, "rules.yaml")
	require.NoError(t, os.WriteFile(rulesFile, []byte("endpoints:\n  file-endpoint:\n    description: v1\n    rules:\n      - name: file-rule\nrules:\n  file-rule:\n    description: v1\n"), 0o600))

	serverCfg := filepath.Join(dir, "server.yaml")
	configContents := "server:\n  rules:\n    rulesFolder: \"\"\n    rulesFile: %s\nendpoints:\n  inline-endpoint:\n    description: inline\n    rules:\n      - name: inline-rule\nrules:\n  inline-rule:\n    description: inline\n"
	require.NoError(t, os.WriteFile(serverCfg, []byte(fmt.Sprintf(configContents, rulesFile)), 0o600))

	loader := NewLoader("PASSCTRL", serverCfg)
	cfg, err := loader.Load(ctx)
	require.NoError(t, err)

	changeCh := make(chan RuleBundle, 4)
	errCh := make(chan error, 1)

	watcher, err := loader.WatchRules(ctx, cfg, func(bundle RuleBundle) {
		changeCh <- bundle
	}, func(err error) {
		errCh <- err
	})
	require.NoError(t, err)
	defer watcher.Stop()

	select {
	case bundle := <-changeCh:
		require.Contains(t, bundle.Endpoints, "inline-endpoint", "inline endpoint missing on initial load")
		endpoint, ok := bundle.Endpoints["file-endpoint"]
		require.True(t, ok, "file endpoint missing on initial load")
		require.Equal(t, "v1", endpoint.Description)
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "timeout waiting for initial change event")
	}

	require.NoError(t, os.WriteFile(rulesFile, []byte("endpoints:\n  file-endpoint:\n    description: v2\n    rules:\n      - name: file-rule\nrules:\n  file-rule:\n    description: v2\n"), 0o600))

	select {
	case bundle := <-changeCh:
		endpoint, ok := bundle.Endpoints["file-endpoint"]
		require.True(t, ok, "file endpoint missing after reload")
		require.Equal(t, "v2", endpoint.Description)
		require.Contains(t, bundle.Endpoints, "inline-endpoint")
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "timeout waiting for reload event")
	}
}

func TestWatchRulesFolderReloads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	rulesDir := filepath.Join(dir, "rules")
	require.NoError(t, os.MkdirAll(rulesDir, 0o750))

	serverCfg := filepath.Join(dir, "server.yaml")
	configContents := "server:\n  rules:\n    rulesFolder: %s\nendpoints:\n  inline-endpoint:\n    description: inline\n    rules:\n      - name: inline-rule\nrules:\n  inline-rule:\n    description: inline\n"
	require.NoError(t, os.WriteFile(serverCfg, []byte(fmt.Sprintf(configContents, rulesDir)), 0o600))

	loader := NewLoader("PASSCTRL", serverCfg)
	cfg, err := loader.Load(ctx)
	require.NoError(t, err)

	changeCh := make(chan RuleBundle, 4)
	errCh := make(chan error, 1)

	watcher, err := loader.WatchRules(ctx, cfg, func(bundle RuleBundle) {
		changeCh <- bundle
	}, func(err error) {
		errCh <- err
	})
	require.NoError(t, err)
	defer watcher.Stop()

	select {
	case bundle := <-changeCh:
		require.Len(t, bundle.Endpoints, 1, "expected only inline endpoint initially")
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "timeout waiting for initial event")
	}

	rulePath := filepath.Join(rulesDir, "file.yaml")
	require.NoError(t, os.WriteFile(rulePath, []byte("endpoints:\n  folder-endpoint:\n    description: folder\n    rules:\n      - name: folder-rule\nrules:\n  folder-rule:\n    description: folder\n"), 0o600))

	select {
	case bundle := <-changeCh:
		require.Contains(t, bundle.Endpoints, "folder-endpoint")
		require.Contains(t, bundle.Endpoints, "inline-endpoint")
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		require.FailNow(t, "timeout waiting for folder reload event")
	}
}
