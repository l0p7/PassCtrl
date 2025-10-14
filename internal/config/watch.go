package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// RulesWatcher monitors the configured rules source (file or folder) and invokes
// the supplied callback whenever definitions change. Stop must be called to
// release filesystem resources.
type RulesWatcher struct {
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// Stop halts the watcher and waits for the underlying goroutine to exit.
func (w *RulesWatcher) Stop() {
	if w == nil {
		return
	}
	w.once.Do(func() {
		w.cancel()
		<-w.done
	})
}

// WatchRules wires fsnotify around the configured rules source and reloads the
// bundle on any relevant change. The provided config should come from Loader.Load
// so InlineEndpoints and InlineRules are already captured.
func (l *Loader) WatchRules(ctx context.Context, cfg Config, onChange func(RuleBundle), onError func(error)) (*RulesWatcher, error) {
	if onChange == nil {
		return nil, fmt.Errorf("config: watch rules requires a change callback")
	}
	if cfg.Server.Rules.RulesFile == "" && cfg.Server.Rules.RulesFolder == "" {
		return nil, fmt.Errorf("config: no rules source configured for watching")
	}

	watchCtx, cancel := context.WithCancel(ctx)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("config: watch rules: %w", err)
	}

	inlineEndpoints := cloneEndpointMap(cfg.InlineEndpoints)
	inlineRules := cloneRuleMap(cfg.InlineRules)

	bundle, err := buildRuleBundle(watchCtx, inlineEndpoints, inlineRules, cfg.Server.Rules)
	if err != nil {
		if closeErr := watcher.Close(); closeErr != nil && onError != nil {
			onError(fmt.Errorf("config: watch rules close: %w", closeErr))
		}
		cancel()
		return nil, err
	}
	onChange(bundle)

	done := make(chan struct{})
	watch := &RulesWatcher{cancel: cancel, done: done}

	ready := make(chan struct{})
	var readyOnce sync.Once
	signalReady := func() { readyOnce.Do(func() { close(ready) }) }

	go func() {
		defer close(done)
		defer func() {
			if err := watcher.Close(); err != nil && onError != nil {
				onError(fmt.Errorf("config: watch rules close: %w", err))
			}
		}()
		defer signalReady()

		var reloadMu sync.Mutex
		reload := func() {
			reloadMu.Lock()
			defer reloadMu.Unlock()
			bundle, err := buildRuleBundle(watchCtx, inlineEndpoints, inlineRules, cfg.Server.Rules)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				if onError != nil {
					onError(err)
				}
				return
			}
			onChange(bundle)
		}

		dirs := map[string]struct{}{}
		addDir := func(dir string) {
			dir = filepath.Clean(dir)
			if _, ok := dirs[dir]; ok {
				return
			}
			if err := watcher.Add(dir); err != nil {
				if onError != nil {
					onError(fmt.Errorf("config: watch add %s: %w", dir, err))
				}
				return
			}
			dirs[dir] = struct{}{}
		}

		targetFile := ""
		if cfg.Server.Rules.RulesFile != "" {
			resolved := cfg.Server.Rules.RulesFile
			if path, err := filepath.Abs(cfg.Server.Rules.RulesFile); err == nil {
				resolved = path
			} else if onError != nil {
				onError(fmt.Errorf("config: resolve rules file: %w", err))
			}
			targetFile = filepath.Clean(resolved)
			addDir(filepath.Dir(targetFile))
		} else {
			root, err := filepath.Abs(cfg.Server.Rules.RulesFolder)
			if err != nil {
				if onError != nil {
					onError(fmt.Errorf("config: resolve rules folder: %w", err))
				}
				root = cfg.Server.Rules.RulesFolder
			}
			if err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
				if walkErr != nil {
					if onError != nil {
						onError(fmt.Errorf("config: walk watcher %s: %w", path, walkErr))
					}
					return nil
				}
				if d.IsDir() {
					addDir(path)
				}
				return nil
			}); err != nil {
				if onError != nil {
					onError(fmt.Errorf("config: traverse watcher %s: %w", root, err))
				}
			}
		}

		signalReady()

		const debounce = 25 * time.Millisecond
		var reloadTimer *time.Timer
		var reloadSignal <-chan time.Time
		scheduleReload := func() {
			if reloadTimer == nil {
				reloadTimer = time.NewTimer(debounce)
			} else {
				if !reloadTimer.Stop() {
					select {
					case <-reloadTimer.C:
					default:
					}
				}
				reloadTimer.Reset(debounce)
			}
			reloadSignal = reloadTimer.C
		}
		flushTimer := func() {
			if reloadTimer == nil {
				return
			}
			if !reloadTimer.Stop() {
				select {
				case <-reloadTimer.C:
				default:
				}
			}
			reloadSignal = nil
		}
		defer flushTimer()

		for {
			select {
			case <-watchCtx.Done():
				return
			case <-reloadSignal:
				flushTimer()
				reload()
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				name := filepath.Clean(event.Name)
				if targetFile != "" {
					if name != targetFile {
						continue
					}
					if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
						if onError != nil {
							onError(fmt.Errorf("config: rules file %s removed", targetFile))
						}
					}
					if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove|fsnotify.Chmod) != 0 {
						scheduleReload()
					}
					continue
				}
				if event.Op&fsnotify.Create != 0 {
					info, err := os.Stat(name)
					if err == nil && info.IsDir() {
						addDir(name)
						continue
					}
				}
				if !isSupportedRulesFile(name) {
					continue
				}
				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove|fsnotify.Chmod) == 0 {
					continue
				}
				scheduleReload()
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				if onError != nil {
					onError(fmt.Errorf("config: watch error: %w", err))
				}
			}
		}
	}()

	<-ready

	return watch, nil
}
