package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchRulesFileReloads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	rulesFile := filepath.Join(dir, "rules.yaml")
	if err := os.WriteFile(rulesFile, []byte("endpoints:\n  file-endpoint:\n    description: v1\n    rules:\n      - name: file-rule\nrules:\n  file-rule:\n    description: v1\n"), 0o600); err != nil {
		t.Fatalf("failed to write rules file: %v", err)
	}

	serverCfg := filepath.Join(dir, "server.yaml")
	configContents := "server:\n  rules:\n    rulesFolder: \"\"\n    rulesFile: %s\nendpoints:\n  inline-endpoint:\n    description: inline\n    rules:\n      - name: inline-rule\nrules:\n  inline-rule:\n    description: inline\n"
	if err := os.WriteFile(serverCfg, []byte(fmt.Sprintf(configContents, rulesFile)), 0o600); err != nil {
		t.Fatalf("failed to write server config: %v", err)
	}

	loader := NewLoader("PASSCTRL", serverCfg)
	cfg, err := loader.Load(ctx)
	if err != nil {
		t.Fatalf("loader failed: %v", err)
	}

	changeCh := make(chan RuleBundle, 4)
	errCh := make(chan error, 1)

	watcher, err := loader.WatchRules(ctx, cfg, func(bundle RuleBundle) {
		changeCh <- bundle
	}, func(err error) {
		errCh <- err
	})
	if err != nil {
		t.Fatalf("watcher failed: %v", err)
	}
	defer watcher.Stop()

	select {
	case bundle := <-changeCh:
		if _, ok := bundle.Endpoints["inline-endpoint"]; !ok {
			t.Fatalf("inline endpoint missing on initial load: %v", bundle.Endpoints)
		}
		endpoint, ok := bundle.Endpoints["file-endpoint"]
		if !ok {
			t.Fatalf("file endpoint missing on initial load: %v", bundle.Endpoints)
		}
		if endpoint.Description != "v1" {
			t.Fatalf("expected file endpoint v1, got %v", endpoint.Description)
		}
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for initial change event")
	}

	if err := os.WriteFile(rulesFile, []byte("endpoints:\n  file-endpoint:\n    description: v2\n    rules:\n      - name: file-rule\nrules:\n  file-rule:\n    description: v2\n"), 0o600); err != nil {
		t.Fatalf("failed to update rules file: %v", err)
	}

	select {
	case bundle := <-changeCh:
		endpoint, ok := bundle.Endpoints["file-endpoint"]
		if !ok {
			t.Fatalf("file endpoint missing after reload")
		}
		if endpoint.Description != "v2" {
			t.Fatalf("expected updated description, got %v", endpoint.Description)
		}
		if _, ok := bundle.Endpoints["inline-endpoint"]; !ok {
			t.Fatalf("inline endpoint missing after reload")
		}
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for reload event")
	}
}

func TestWatchRulesFolderReloads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := t.TempDir()
	rulesDir := filepath.Join(dir, "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatalf("failed to create rules folder: %v", err)
	}

	serverCfg := filepath.Join(dir, "server.yaml")
	configContents := "server:\n  rules:\n    rulesFolder: %s\nendpoints:\n  inline-endpoint:\n    description: inline\n    rules:\n      - name: inline-rule\nrules:\n  inline-rule:\n    description: inline\n"
	if err := os.WriteFile(serverCfg, []byte(fmt.Sprintf(configContents, rulesDir)), 0o600); err != nil {
		t.Fatalf("failed to write server config: %v", err)
	}

	loader := NewLoader("PASSCTRL", serverCfg)
	cfg, err := loader.Load(ctx)
	if err != nil {
		t.Fatalf("loader failed: %v", err)
	}

	changeCh := make(chan RuleBundle, 4)
	errCh := make(chan error, 1)

	watcher, err := loader.WatchRules(ctx, cfg, func(bundle RuleBundle) {
		changeCh <- bundle
	}, func(err error) {
		errCh <- err
	})
	if err != nil {
		t.Fatalf("watcher failed: %v", err)
	}
	defer watcher.Stop()

	select {
	case bundle := <-changeCh:
		if len(bundle.Endpoints) != 1 {
			t.Fatalf("expected only inline endpoint initially, got %v", bundle.Endpoints)
		}
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for initial event")
	}

	rulePath := filepath.Join(rulesDir, "file.yaml")
	if err := os.WriteFile(rulePath, []byte("endpoints:\n  folder-endpoint:\n    description: folder\n    rules:\n      - name: folder-rule\nrules:\n  folder-rule:\n    description: folder\n"), 0o600); err != nil {
		t.Fatalf("failed to create rules document: %v", err)
	}

	select {
	case bundle := <-changeCh:
		if _, ok := bundle.Endpoints["folder-endpoint"]; !ok {
			t.Fatalf("expected folder endpoint after reload: %v", bundle.Endpoints)
		}
		if _, ok := bundle.Endpoints["inline-endpoint"]; !ok {
			t.Fatalf("inline endpoint missing after reload")
		}
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for folder reload event")
	}
}
