package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"metiq/internal/agent"
	"metiq/internal/config"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/policy"
	securitypkg "metiq/internal/security"
	"metiq/internal/store/state"
)

type configPutFailStore struct {
	*testStore
	mu         sync.Mutex
	failConfig bool
	err        error
}

func (s *configPutFailStore) setFailConfig(fail bool) {
	s.mu.Lock()
	s.failConfig = fail
	s.mu.Unlock()
}

func (s *configPutFailStore) PutReplaceable(ctx context.Context, addr state.Address, content string, extraTags [][]string) (state.Event, error) {
	s.mu.Lock()
	fail := s.failConfig && addr.DTag == "metiq:config"
	err := s.err
	s.mu.Unlock()
	if fail {
		if err == nil {
			err = errors.New("injected config repo failure")
		}
		return state.Event{}, err
	}
	return s.testStore.PutReplaceable(ctx, addr, content, extraTags)
}

func baseConfigMutationDoc() state.ConfigDoc {
	return policy.NormalizeConfig(state.ConfigDoc{
		Version: 1,
		DM:      state.DMPolicy{Policy: "open"},
		Relays: state.RelayPolicy{
			Read:  []string{"wss://relay.example"},
			Write: []string{"wss://relay.example"},
		},
		Agent: state.AgentPolicy{DefaultModel: "echo"},
	})
}

func configMutationHandler(docsRepo *state.DocsRepository, cfgState *runtimeConfigStore) controlRPCHandler {
	return newControlRPCHandler(controlRPCDeps{
		docsRepo:    docsRepo,
		configState: cfgState,
		tools:       agent.NewToolRegistry(),
		startedAt:   time.Now(),
	})
}

func withRuntimeConfigFile(t *testing.T, path string) {
	t.Helper()
	controlServicesMu.Lock()
	prev := controlServices
	controlServices = &daemonServices{
		emitterMu:      &sync.RWMutex{},
		handlers:       handlerServices{configFilePath: path},
		restartCh:      make(chan int, 8),
		lifecycleCtx:   context.Background(),
		agentRunWG:     &sync.WaitGroup{},
		agentRunMu:     &sync.Mutex{},
		agentRunClosed: new(bool),
	}
	controlServicesMu.Unlock()
	t.Cleanup(func() {
		controlServicesMu.Lock()
		controlServices = prev
		controlServicesMu.Unlock()
	})
}

func readConfigFileDoc(t *testing.T, path string) state.ConfigDoc {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	doc, err := config.ParseConfigBytes(raw, path)
	if err != nil {
		t.Fatalf("parse config file: %v", err)
	}
	return policy.NormalizeConfig(doc)
}

func TestHandleConfigRPCSecurityAuditUsesConfiguredBootstrapPath(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".metiq"), 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	t.Setenv("HOME", home)
	defaultBootstrap := filepath.Join(home, ".metiq", "bootstrap.json")
	if err := os.WriteFile(defaultBootstrap, []byte("{\"admin_listen_addr\":\"127.0.0.1:9999\",\"admin_token\":\"present\"}"), 0o600); err != nil {
		t.Fatalf("write default bootstrap: %v", err)
	}

	customBootstrap := filepath.Join(dir, "custom-bootstrap.json")
	if err := os.WriteFile(customBootstrap, []byte("{\"admin_listen_addr\":\"127.0.0.1:8787\"}"), 0o600); err != nil {
		t.Fatalf("write custom bootstrap: %v", err)
	}

	h := newControlRPCHandler(controlRPCDeps{
		bootstrapPath: customBootstrap,
		configState:   newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}}),
		startedAt:     time.Now(),
	})
	res, handled, err := h.handleConfigRPC(context.Background(), nostruntime.ControlRPCInbound{Method: methods.MethodSecurityAudit}, methods.MethodSecurityAudit, state.ConfigDoc{})
	if err != nil {
		t.Fatalf("security audit rpc: %v", err)
	}
	if !handled {
		t.Fatal("security audit was not handled")
	}
	out, ok := res.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %#v", res.Result)
	}
	findings, ok := out["findings"].([]securitypkg.Finding)
	if !ok {
		t.Fatalf("unexpected findings type: %#v", out["findings"])
	}
	for _, finding := range findings {
		if finding.CheckID == "admin-no-token" {
			return
		}
	}
	t.Fatalf("expected admin-no-token from custom bootstrap, got %#v", findings)
}

func configMutationParams(t *testing.T, method string, current, next state.ConfigDoc) json.RawMessage {
	t.Helper()
	var payload any
	switch method {
	case methods.MethodConfigPut:
		payload = map[string]any{"config": next, "base_hash": current.Hash()}
	case methods.MethodConfigSet:
		payload = map[string]any{"key": "dm.policy", "value": next.DM.Policy, "base_hash": current.Hash()}
	case methods.MethodConfigApply:
		payload = map[string]any{"config": next, "base_hash": current.Hash()}
	case methods.MethodConfigPatch:
		payload = map[string]any{"patch": map[string]any{"dm": map[string]any{"policy": next.DM.Policy}}, "base_hash": current.Hash()}
	default:
		t.Fatalf("unsupported method %s", method)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return raw
}

func TestConfigMutationRepoFailureRollsBackFileForMutatingMethods(t *testing.T) {
	methodsUnderTest := []string{
		methods.MethodConfigPut,
		methods.MethodConfigSet,
		methods.MethodConfigApply,
		methods.MethodConfigPatch,
	}
	for _, method := range methodsUnderTest {
		method := method
		t.Run(method, func(t *testing.T) {
			initial := baseConfigMutationDoc()
			next := initial
			next.DM.Policy = "disabled"
			path := filepath.Join(t.TempDir(), "config.json")
			if err := config.WriteConfigFile(path, initial); err != nil {
				t.Fatalf("seed config file: %v", err)
			}
			withRuntimeConfigFile(t, path)

			store := &configPutFailStore{testStore: newTestStore(), err: errors.New("repo unavailable")}
			docsRepo := state.NewDocsRepository(store, "author")
			if _, err := docsRepo.PutConfig(context.Background(), initial); err != nil {
				t.Fatalf("seed repo: %v", err)
			}
			store.setFailConfig(true)
			cfgState := newRuntimeConfigStore(initial)
			var onChangeCalls atomic.Int32
			cfgState.SetOnChange(func(state.ConfigDoc) { onChangeCalls.Add(1) })
			h := configMutationHandler(docsRepo, cfgState)

			_, err := h.Handle(context.Background(), nostruntime.ControlRPCInbound{
				Method:   method,
				Params:   configMutationParams(t, method, initial, next),
				Internal: true,
			})
			if err == nil {
				t.Fatal("expected repo failure")
			}
			var durabilityErr *configMutationDurabilityError
			if !errors.As(err, &durabilityErr) {
				t.Fatalf("expected configMutationDurabilityError, got %T: %v", err, err)
			}
			if durabilityErr.Partial {
				t.Fatalf("rollback should have succeeded, got partial error: %v", durabilityErr)
			}
			if got := readConfigFileDoc(t, path); got.Hash() != initial.Hash() {
				t.Fatalf("file hash = %s, want initial %s", got.Hash(), initial.Hash())
			}
			if got := cfgState.Get(); got.Hash() != initial.Hash() {
				t.Fatalf("runtime hash = %s, want initial %s", got.Hash(), initial.Hash())
			}
			if onChangeCalls.Load() != 0 {
				t.Fatalf("configState.Set called %d times on failed commit", onChangeCalls.Load())
			}
		})
	}
}

func TestConfigMutationRollbackFailureReportsPartialDurability(t *testing.T) {
	initial := baseConfigMutationDoc()
	next := initial
	next.DM.Policy = "disabled"
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.WriteConfigFile(path, initial); err != nil {
		t.Fatalf("seed config file: %v", err)
	}
	withRuntimeConfigFile(t, path)

	store := &configPutFailStore{testStore: newTestStore(), err: errors.New("repo unavailable")}
	docsRepo := state.NewDocsRepository(store, "author")
	if _, err := docsRepo.PutConfig(context.Background(), initial); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	store.setFailConfig(true)
	cfgState := newRuntimeConfigStore(initial)
	h := configMutationHandler(docsRepo, cfgState)

	prevWrite := writeRuntimeConfigFile
	var writes atomic.Int32
	writeRuntimeConfigFile = func(path string, doc state.ConfigDoc) error {
		if writes.Add(1) == 2 {
			return errors.New("rollback write failed")
		}
		return prevWrite(path, doc)
	}
	t.Cleanup(func() { writeRuntimeConfigFile = prevWrite })

	_, err := h.Handle(context.Background(), nostruntime.ControlRPCInbound{
		Method:   methods.MethodConfigSet,
		Params:   configMutationParams(t, methods.MethodConfigSet, initial, next),
		Internal: true,
	})
	if err == nil {
		t.Fatal("expected rollback failure")
	}
	var durabilityErr *configMutationDurabilityError
	if !errors.As(err, &durabilityErr) {
		t.Fatalf("expected configMutationDurabilityError, got %T: %v", err, err)
	}
	if !durabilityErr.Partial || durabilityErr.RollbackErr == nil {
		t.Fatalf("expected partial durability error with rollback cause, got %#v", durabilityErr)
	}
	if got := cfgState.Get(); got.Hash() != initial.Hash() {
		t.Fatalf("runtime hash = %s, want initial %s", got.Hash(), initial.Hash())
	}
}

func TestConfigMutationSkipsDuplicateRuntimeApplyWhenWatcherAlreadyApplied(t *testing.T) {
	initial := baseConfigMutationDoc()
	next := initial
	next.DM.Policy = "disabled"
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.WriteConfigFile(path, initial); err != nil {
		t.Fatalf("seed config file: %v", err)
	}
	withRuntimeConfigFile(t, path)

	store := &configPutFailStore{testStore: newTestStore()}
	docsRepo := state.NewDocsRepository(store, "author")
	if _, err := docsRepo.PutConfig(context.Background(), initial); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	cfgState := newRuntimeConfigStore(initial)
	var onChangeCalls atomic.Int32
	cfgState.SetOnChange(func(state.ConfigDoc) { onChangeCalls.Add(1) })
	h := configMutationHandler(docsRepo, cfgState)

	prevWrite := writeRuntimeConfigFile
	var injected atomic.Bool
	writeRuntimeConfigFile = func(path string, doc state.ConfigDoc) error {
		if err := prevWrite(path, doc); err != nil {
			return err
		}
		if doc.Hash() == next.Hash() && injected.CompareAndSwap(false, true) {
			cfgState.Set(doc)
		}
		return nil
	}
	t.Cleanup(func() { writeRuntimeConfigFile = prevWrite })

	res, err := h.Handle(context.Background(), nostruntime.ControlRPCInbound{
		Method:   methods.MethodConfigApply,
		Params:   configMutationParams(t, methods.MethodConfigApply, initial, next),
		Internal: true,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.Result == nil {
		t.Fatal("expected result")
	}
	if onChangeCalls.Load() != 1 {
		t.Fatalf("configState.Set side effects = %d, want exactly 1", onChangeCalls.Load())
	}
	if got := cfgState.Get(); got.Hash() != next.Hash() {
		t.Fatalf("runtime hash = %s, want next %s", got.Hash(), next.Hash())
	}
}

func TestApplyRuntimeConfigReloadIfChangedSuppressesNoop(t *testing.T) {
	initial := baseConfigMutationDoc()
	cfgState := newRuntimeConfigStore(initial)
	var calls atomic.Int32
	applied := applyRuntimeConfigReloadIfChanged(cfgState, initial, func(doc state.ConfigDoc) {
		calls.Add(1)
		cfgState.Set(doc)
	})
	if applied {
		t.Fatal("identical reload should be suppressed")
	}
	if calls.Load() != 0 {
		t.Fatalf("apply called %d times for identical reload", calls.Load())
	}

	next := initial
	next.DM.Policy = "disabled"
	applied = applyRuntimeConfigReloadIfChanged(cfgState, next, func(doc state.ConfigDoc) {
		calls.Add(1)
		cfgState.Set(doc)
	})
	if !applied {
		t.Fatal("changed reload should apply")
	}
	if calls.Load() != 1 {
		t.Fatalf("apply called %d times, want 1", calls.Load())
	}
	if got := cfgState.Get(); got.Hash() != policy.NormalizeConfig(next).Hash() {
		t.Fatalf("runtime hash = %s, want next %s", got.Hash(), next.Hash())
	}
}

func TestConfigMutationCommitsAreSerialized(t *testing.T) {
	initial := baseConfigMutationDoc()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.WriteConfigFile(path, initial); err != nil {
		t.Fatalf("seed config file: %v", err)
	}
	withRuntimeConfigFile(t, path)

	store := &configPutFailStore{testStore: newTestStore()}
	docsRepo := state.NewDocsRepository(store, "author")
	if _, err := docsRepo.PutConfig(context.Background(), initial); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	cfgState := newRuntimeConfigStore(initial)
	h := configMutationHandler(docsRepo, cfgState)

	prevWrite := writeRuntimeConfigFile
	enteredFirst := make(chan struct{})
	releaseFirst := make(chan struct{})
	enteredSecondWhileFirstBlocked := make(chan struct{}, 1)
	var active atomic.Int32
	var first atomic.Bool
	writeRuntimeConfigFile = func(path string, doc state.ConfigDoc) error {
		if active.Add(1) > 1 {
			enteredSecondWhileFirstBlocked <- struct{}{}
		}
		if first.CompareAndSwap(false, true) {
			close(enteredFirst)
			<-releaseFirst
		}
		err := prevWrite(path, doc)
		active.Add(-1)
		return err
	}
	t.Cleanup(func() { writeRuntimeConfigFile = prevWrite })

	ctx := context.Background()
	firstDone := make(chan error, 1)
	go func() {
		next := initial
		next.DM.Policy = "disabled"
		_, err := h.Handle(ctx, nostruntime.ControlRPCInbound{
			Method:   methods.MethodConfigSet,
			Params:   configMutationParams(t, methods.MethodConfigSet, initial, next),
			Internal: true,
		})
		firstDone <- err
	}()
	<-enteredFirst

	secondDone := make(chan error, 1)
	go func() {
		next := initial
		next.Relays.Read = []string{"wss://relay-two.example"}
		_, err := h.Handle(ctx, nostruntime.ControlRPCInbound{
			Method:   methods.MethodConfigSet,
			Params:   configMutationParams(t, methods.MethodConfigSet, cfgState.Get(), next),
			Internal: true,
		})
		secondDone <- err
	}()

	select {
	case <-enteredSecondWhileFirstBlocked:
		t.Fatal("second config mutation entered durable write while first commit was active")
	default:
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first mutation: %v", err)
	}
	if err := <-secondDone; err != nil {
		// The second request may legitimately see a stale base_hash if it was
		// constructed before the first commit completed; the serialization property
		// under test is that durable writes never overlap.
		if !strings.Contains(fmt.Sprint(err), "base_hash") && !strings.Contains(fmt.Sprint(err), "precondition") {
			t.Fatalf("second mutation: %v", err)
		}
	}
}
