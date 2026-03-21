// Package config – bidirectional config sync: disk file ↔ Nostr relay.
//
// Design:
//   - Disk is the human-readable source of truth (JSON5/YAML).
//   - On startup, LoadFromFile → publish to relay as a replaceable event.
//   - On relay update (bootstrap / remote admin), fetch → WriteToFile.
//   - fsnotify watches the file; on change → push to relay.
//   - All relay I/O is done through the state.DocsRepository (Nostr).
package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"metiq/internal/store/state"
)

// RelayWriter publishes a ConfigDoc to Nostr.
// Implemented by state.DocsRepository (or any thin wrapper around it).
type RelayWriter interface {
	PutConfig(ctx context.Context, doc state.ConfigDoc) (state.Event, error)
}

// RelayReader fetches the current ConfigDoc from Nostr.
type RelayReader interface {
	GetConfig(ctx context.Context) (state.ConfigDoc, error)
}

// RelaySync is the interface satisfied by *state.DocsRepository.
type RelaySync interface {
	RelayWriter
	RelayReader
}

// SyncEngine watches a config file on disk and keeps it in sync with a Nostr
// relay. It also supports pulling the relay version to bootstrap from cold start.
type SyncEngine struct {
	mu       sync.Mutex
	path     string
	relay    RelaySync
	watcher  *fsnotify.Watcher
	debounce time.Duration
	log      *slog.Logger
	cancel   context.CancelFunc
	onChange func(state.ConfigDoc) // called after each successful reload from disk
}

// NewSyncEngine creates a SyncEngine. Call Start to activate file watching.
func NewSyncEngine(configPath string, relay RelaySync, opts ...SyncOption) (*SyncEngine, error) {
	if configPath == "" {
		return nil, fmt.Errorf("configPath must not be empty")
	}
	if relay == nil {
		return nil, fmt.Errorf("relay must not be nil")
	}
	se := &SyncEngine{
		path:     configPath,
		relay:    relay,
		debounce: 500 * time.Millisecond,
		log:      slog.Default().With("component", "config-sync"),
	}
	for _, o := range opts {
		o(se)
	}
	return se, nil
}

// SyncOption is a functional option for SyncEngine.
type SyncOption func(*SyncEngine)

// WithDebounce sets the debounce window for file-change events (default: 500ms).
func WithDebounce(d time.Duration) SyncOption {
	return func(se *SyncEngine) { se.debounce = d }
}

// WithLogger sets a custom slog.Logger.
func WithLogger(l *slog.Logger) SyncOption {
	return func(se *SyncEngine) { se.log = l }
}

// WithOnChange registers a callback that is invoked with the freshly loaded
// ConfigDoc each time the config file changes on disk and is successfully
// read.  This allows the runtime to apply changes without a daemon restart.
func WithOnChange(fn func(state.ConfigDoc)) SyncOption {
	return func(se *SyncEngine) { se.onChange = fn }
}

// BootstrapFromRelay pulls the config from the relay and writes it to disk.
// If no relay config exists yet, it returns ErrNoRelayConfig without touching
// the disk file.
func (se *SyncEngine) BootstrapFromRelay(ctx context.Context) error {
	doc, err := se.relay.GetConfig(ctx)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			se.log.Info("no config on relay yet; using local file")
			return ErrNoRelayConfig
		}
		return fmt.Errorf("fetch relay config: %w", err)
	}
	if err := se.writeFileLocked(doc); err != nil {
		return fmt.Errorf("write bootstrapped config to disk: %w", err)
	}
	se.log.Info("bootstrapped config from relay", "path", se.path)
	return nil
}

// PushToDisk fetches the relay config and overwrites the local file.
// Same as BootstrapFromRelay but does not return ErrNoRelayConfig on absence.
func (se *SyncEngine) PushToDisk(ctx context.Context) error {
	doc, err := se.relay.GetConfig(ctx)
	if err != nil {
		return fmt.Errorf("fetch relay config: %w", err)
	}
	return se.writeFileLocked(doc)
}

// PushToRelay reads the disk config file and publishes it to the relay.
func (se *SyncEngine) PushToRelay(ctx context.Context) error {
	doc, err := se.readFileLocked()
	if err != nil {
		return err
	}
	if _, err := se.relay.PutConfig(ctx, doc); err != nil {
		return fmt.Errorf("publish config to relay: %w", err)
	}
	se.log.Info("config pushed to relay", "path", se.path)
	return nil
}

// Start begins watching the config file for changes and pushes to relay on
// each change. The sync runs until ctx is cancelled or Stop is called.
func (se *SyncEngine) Start(ctx context.Context) error {
	se.mu.Lock()
	defer se.mu.Unlock()

	if se.watcher != nil {
		return fmt.Errorf("SyncEngine already started")
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create fsnotify watcher: %w", err)
	}
	// Watch the directory (not the file) so rename/atomic-write events work.
	dir := configDir(se.path)
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return fmt.Errorf("watch config directory %q: %w", dir, err)
	}
	se.watcher = w

	watchCtx, cancel := context.WithCancel(ctx)
	se.cancel = cancel

	go se.loop(watchCtx, w)
	se.log.Info("config file watcher started", "path", se.path, "dir", dir)
	return nil
}

// Stop halts file watching gracefully.
func (se *SyncEngine) Stop() {
	se.mu.Lock()
	defer se.mu.Unlock()
	if se.cancel != nil {
		se.cancel()
		se.cancel = nil
	}
	if se.watcher != nil {
		_ = se.watcher.Close()
		se.watcher = nil
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// internal loop
// ──────────────────────────────────────────────────────────────────────────────

func (se *SyncEngine) loop(ctx context.Context, watcher *fsnotify.Watcher) {
	var (
		timer   *time.Timer
		pending bool
	)
	resetTimer := func() {
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(se.debounce, func() {
			se.mu.Lock()
			pending = false
			se.mu.Unlock()
			pushCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			doc, readErr := se.readFileLocked()
			if readErr != nil {
				se.log.Error("reload config from disk failed", "err", readErr)
			} else {
				// Notify the runtime about the config change first.
				se.mu.Lock()
				onChange := se.onChange
				se.mu.Unlock()
				if onChange != nil {
					onChange(doc)
				}
				// Then push to relay.
				if _, err := se.relay.PutConfig(pushCtx, doc); err != nil {
					if !errors.Is(err, context.Canceled) {
						se.log.Error("auto push to relay failed", "err", err)
					}
				}
			}
		})
	}

	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}
			// Only act on the watched file.
			if ev.Name != se.path {
				continue
			}
			if ev.Has(fsnotify.Write) || ev.Has(fsnotify.Create) || ev.Has(fsnotify.Rename) {
				se.mu.Lock()
				if !pending {
					pending = true
					resetTimer()
				}
				se.mu.Unlock()
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			if err != nil {
				se.log.Error("fsnotify error", "err", err)
			}
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────────────

func (se *SyncEngine) readFileLocked() (state.ConfigDoc, error) {
	doc, err := LoadConfigFile(se.path)
	if err != nil {
		return state.ConfigDoc{}, fmt.Errorf("read config file for sync: %w", err)
	}
	return doc, nil
}

func (se *SyncEngine) writeFileLocked(doc state.ConfigDoc) error {
	return WriteConfigFile(se.path, doc)
}

func configDir(path string) string {
	return filepath.Dir(path)
}

// ErrNoRelayConfig is returned by BootstrapFromRelay when no config exists yet.
var ErrNoRelayConfig = errors.New("no config event found on relay")
