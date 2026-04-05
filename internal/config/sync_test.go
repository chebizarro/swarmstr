package config

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"metiq/internal/store/state"
)

// ─── fakeRelaySync ────────────────────────────────────────────────────────────

type fakeRelaySync struct {
	doc state.ConfigDoc
}

func (f *fakeRelaySync) PutConfig(_ context.Context, doc state.ConfigDoc) (state.Event, error) {
	f.doc = doc
	return state.Event{}, nil
}

func (f *fakeRelaySync) GetConfig(_ context.Context) (state.ConfigDoc, error) {
	return f.doc, nil
}

// ─── SyncEngine construction ──────────────────────────────────────────────────

func TestNewSyncEngine_emptyPathError(t *testing.T) {
	_, err := NewSyncEngine("", &fakeRelaySync{})
	if err == nil {
		t.Error("expected error for empty configPath")
	}
}

func TestNewSyncEngine_nilRelayError(t *testing.T) {
	_, err := NewSyncEngine("/tmp/config.json", nil)
	if err == nil {
		t.Error("expected error for nil relay")
	}
}

func TestNewSyncEngine_ok(t *testing.T) {
	se, err := NewSyncEngine("/tmp/config.json", &fakeRelaySync{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if se == nil {
		t.Fatal("expected non-nil SyncEngine")
	}
}

func TestNewSyncEngine_invalidPathError(t *testing.T) {
	_, err := NewSyncEngine(t.TempDir(), &fakeRelaySync{})
	if err == nil {
		t.Fatal("expected error for directory config path")
	}
}

// ─── WithOnChange callback ────────────────────────────────────────────────────

func TestWithOnChange_calledOnFileWrite(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	initial := state.ConfigDoc{Version: 1, DM: state.DMPolicy{Policy: "open"}}
	if err := WriteConfigFile(configPath, initial); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	var callCount atomic.Int32
	var lastDocMu sync.Mutex
	var lastDoc state.ConfigDoc

	relay := &fakeRelaySync{doc: initial}
	se, err := NewSyncEngine(configPath, relay,
		WithDebounce(50*time.Millisecond),
		WithOnChange(func(doc state.ConfigDoc) {
			callCount.Add(1)
			lastDocMu.Lock()
			lastDoc = doc
			lastDocMu.Unlock()
		}),
	)
	if err != nil {
		t.Fatalf("NewSyncEngine: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := se.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer se.Stop()

	// Write an updated config to disk.
	updated := state.ConfigDoc{Version: 2, DM: state.DMPolicy{Policy: "disabled"}}
	if err := WriteConfigFile(configPath, updated); err != nil {
		t.Fatalf("write updated config: %v", err)
	}

	// Wait for the debounce + file event (up to 1s).
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if callCount.Load() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if callCount.Load() == 0 {
		t.Error("OnChange callback was not called after file write")
	}
	lastDocMu.Lock()
	gotLastDoc := lastDoc
	lastDocMu.Unlock()
	if gotLastDoc.DM.Policy != "disabled" {
		t.Errorf("OnChange received wrong doc: policy = %q, want \"disabled\"", gotLastDoc.DM.Policy)
	}
}

// ─── BootstrapFromRelay ───────────────────────────────────────────────────────

func TestBootstrapFromRelay_writesFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	relay := &fakeRelaySync{doc: state.ConfigDoc{Version: 1, DM: state.DMPolicy{Policy: "pairing"}}}
	se, err := NewSyncEngine(configPath, relay)
	if err != nil {
		t.Fatalf("NewSyncEngine: %v", err)
	}

	if err := se.BootstrapFromRelay(context.Background()); err != nil {
		t.Fatalf("BootstrapFromRelay: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	if len(data) == 0 {
		t.Error("config file is empty after bootstrap")
	}
}

func TestBootstrapFromRelay_emptyRelayReturnsErrNoRelayConfig(t *testing.T) {
	dir := t.TempDir()
	// Use a relay that returns ErrNotFound.
	relay := &notFoundRelaySync{}
	se, err := NewSyncEngine(filepath.Join(dir, "config.json"), relay)
	if err != nil {
		t.Fatalf("NewSyncEngine: %v", err)
	}
	err = se.BootstrapFromRelay(context.Background())
	if err != ErrNoRelayConfig {
		t.Errorf("expected ErrNoRelayConfig, got: %v", err)
	}
}

// notFoundRelaySync simulates a relay with no config stored yet.
type notFoundRelaySync struct{}

func (n *notFoundRelaySync) PutConfig(_ context.Context, doc state.ConfigDoc) (state.Event, error) {
	return state.Event{}, nil
}

func (n *notFoundRelaySync) GetConfig(_ context.Context) (state.ConfigDoc, error) {
	return state.ConfigDoc{}, state.ErrNotFound
}
