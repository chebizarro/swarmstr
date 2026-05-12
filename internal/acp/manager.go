package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultManagerTurnTimeout       = 10 * time.Minute
	defaultManagerIdleTTL           = 30 * time.Minute
	defaultManagerMaxSpawnDepth     = 5
	defaultManagerMaxChildrenPerKey = 8
)

var (
	// ErrSessionKeyRequired is returned when a manager operation has no session key.
	ErrSessionKeyRequired = errors.New("acp manager: session key required")
	// ErrSessionNotFound is returned when an operation targets an unknown session.
	ErrSessionNotFound = errors.New("acp manager: session not found")
	// ErrTurnActive is returned when an operation cannot proceed during an active turn.
	ErrTurnActive = errors.New("acp manager: turn active")
)

// ManagerOptions controls manager runtime behavior.
type ManagerOptions struct {
	// DefaultTurnTimeout applies to RunTurn when no request timeout is supplied.
	DefaultTurnTimeout time.Duration
	// RuntimeIdleTTL controls CleanupIdleRuntimeHandles. <=0 disables idle cleanup.
	RuntimeIdleTTL time.Duration
	// MaxSpawnDepth limits managed child session nesting. <=0 uses the default.
	MaxSpawnDepth int
	// MaxChildrenPerSession limits direct managed children per parent. <=0 uses the default.
	MaxChildrenPerSession int
	// Now supplies the current time. Defaults to time.Now.
	Now func() time.Time
}

// Manager coordinates ACP runtime session lifecycle and turns.
type Manager struct {
	backends   *BackendRegistry
	sessions   SessionStore
	agents     *AgentRegistry
	dispatcher *Dispatcher
	opts       ManagerOptions

	mu           sync.Mutex
	locks        map[string]*sessionActorLock
	runtimeCache map[string]*managerRuntimeState
	activeTurns  map[string]*managerActiveTurn
	counters     ManagerCounters
	errorsByCode map[string]int
}

type sessionActorLock struct {
	mu      sync.Mutex
	pending int
}

type managerRuntimeState struct {
	Runtime    BackendRuntime
	Handle     RuntimeHandle
	Backend    string
	Agent      string
	Mode       SessionMode
	CWD        string
	LastUsedAt time.Time
}

type managerActiveTurn struct {
	Runtime   BackendRuntime
	Handle    RuntimeHandle
	Cancel    context.CancelFunc
	RequestID string
	StartedAt time.Time
	TimedOut  bool
}

// ManagerCounters captures cumulative manager activity.
type ManagerCounters struct {
	SessionsInitialized int64 `json:"sessions_initialized"`
	SessionsClosed      int64 `json:"sessions_closed"`
	RuntimeCacheHits    int64 `json:"runtime_cache_hits"`
	RuntimeCacheMisses  int64 `json:"runtime_cache_misses"`
	RuntimeEvicted      int64 `json:"runtime_evicted"`
	TurnsStarted        int64 `json:"turns_started"`
	TurnsCompleted      int64 `json:"turns_completed"`
	TurnsFailed         int64 `json:"turns_failed"`
	TurnsCanceled       int64 `json:"turns_canceled"`
	TurnsTimedOut       int64 `json:"turns_timed_out"`
	ControlsApplied     int64 `json:"controls_applied"`
	SessionsSpawned     int64 `json:"sessions_spawned"`
}

// ManagerStatus is an observability snapshot for the manager.
type ManagerStatus struct {
	RuntimeCacheSize int               `json:"runtime_cache_size"`
	ActiveTurns      int               `json:"active_turns"`
	QueueDepth       int               `json:"queue_depth"`
	Counters         ManagerCounters   `json:"counters"`
	ErrorsByCode     map[string]int    `json:"errors_by_code,omitempty"`
	Sessions         []SessionStatus   `json:"sessions,omitempty"`
	Backends         []BackendSnapshot `json:"backends,omitempty"`
}

// BackendSnapshot is a redacted backend registry entry for status output.
type BackendSnapshot struct {
	ID      string `json:"id"`
	Healthy bool   `json:"healthy"`
}

// SessionRuntimeMeta is persisted in SessionRecord.State by Manager.
type SessionRuntimeMeta struct {
	Backend            string      `json:"backend,omitempty"`
	Agent              string      `json:"agent,omitempty"`
	Mode               SessionMode `json:"mode,omitempty"`
	RuntimeSessionName string      `json:"runtime_session_name,omitempty"`
	CWD                string      `json:"cwd,omitempty"`
	AcpxRecordID       string      `json:"acpx_record_id,omitempty"`
	State              string      `json:"state,omitempty"`
	LastError          string      `json:"last_error,omitempty"`
	LastActivityAt     int64       `json:"last_activity_at,omitempty"`
	ParentSessionKey   string      `json:"parent_session_key,omitempty"`
	SpawnDepth         int         `json:"spawn_depth,omitempty"`
	ThreadID           string      `json:"thread_id,omitempty"`
	SpawnedBy          string      `json:"spawned_by,omitempty"`
	ChildSessionKeys   []string    `json:"child_session_keys,omitempty"`
}

// InitializeSessionInput creates or resumes an ACP runtime session.
type InitializeSessionInput struct {
	SessionKey      string            `json:"session_key"`
	Agent           string            `json:"agent,omitempty"`
	Backend         string            `json:"backend,omitempty"`
	Mode            SessionMode       `json:"mode,omitempty"`
	ResumeSessionID string            `json:"resume_session_id,omitempty"`
	CWD             string            `json:"cwd,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	Controls        []RuntimeControl  `json:"controls,omitempty"`
}

// RunSessionTurnInput runs one turn in a managed ACP session.
type RunSessionTurnInput struct {
	SessionKey  string             `json:"session_key"`
	Backend     string             `json:"backend,omitempty"`
	Agent       string             `json:"agent,omitempty"`
	Mode        string             `json:"mode,omitempty"`
	Text        string             `json:"text"`
	RequestID   string             `json:"request_id,omitempty"`
	TimeoutMS   int64              `json:"timeout_ms,omitempty"`
	Attachments []TurnAttachment   `json:"attachments,omitempty"`
	Controls    []RuntimeControl   `json:"controls,omitempty"`
	OnEvent     func(RuntimeEvent) `json:"-"`
}

// CancelSessionInput cancels an active or backend-known turn for a session.
type CancelSessionInput struct {
	SessionKey string `json:"session_key"`
	Reason     string `json:"reason,omitempty"`
}

// CloseSessionInput closes a managed ACP session.
type CloseSessionInput struct {
	SessionKey             string `json:"session_key"`
	Reason                 string `json:"reason,omitempty"`
	DiscardPersistentState bool   `json:"discard_persistent_state,omitempty"`
	DeleteRecord           bool   `json:"delete_record,omitempty"`
}

// SessionStatus describes a managed ACP session.
type SessionStatus struct {
	SessionKey     string               `json:"session_key"`
	Backend        string               `json:"backend,omitempty"`
	Agent          string               `json:"agent,omitempty"`
	Mode           SessionMode          `json:"mode,omitempty"`
	State          string               `json:"state,omitempty"`
	RuntimeHandle  *RuntimeHandle       `json:"runtime_handle,omitempty"`
	RuntimeStatus  *RuntimeStatus       `json:"runtime_status,omitempty"`
	Capabilities   *RuntimeCapabilities `json:"capabilities,omitempty"`
	Cached         bool                 `json:"cached"`
	ActiveTurn     bool                 `json:"active_turn"`
	LastError      string               `json:"last_error,omitempty"`
	LastActivityAt int64                `json:"last_activity_at,omitempty"`
	Details        map[string]any       `json:"details,omitempty"`
}

// NewManager creates a manager around existing ACP registries and stores.
func NewManager(backends *BackendRegistry, sessions SessionStore, agents *AgentRegistry, dispatcher *Dispatcher, opts ManagerOptions) *Manager {
	if backends == nil {
		backends = defaultBackendRegistry
	}
	if agents == nil {
		agents = NewAgentRegistry()
	}
	if dispatcher == nil {
		dispatcher = NewDispatcher()
	}
	if opts.DefaultTurnTimeout <= 0 {
		opts.DefaultTurnTimeout = defaultManagerTurnTimeout
	}
	if opts.RuntimeIdleTTL == 0 {
		opts.RuntimeIdleTTL = defaultManagerIdleTTL
	}
	if opts.MaxSpawnDepth <= 0 {
		opts.MaxSpawnDepth = defaultManagerMaxSpawnDepth
	}
	if opts.MaxChildrenPerSession <= 0 {
		opts.MaxChildrenPerSession = defaultManagerMaxChildrenPerKey
	}
	return &Manager{
		backends:     backends,
		sessions:     sessions,
		agents:       agents,
		dispatcher:   dispatcher,
		opts:         opts,
		locks:        make(map[string]*sessionActorLock),
		runtimeCache: make(map[string]*managerRuntimeState),
		activeTurns:  make(map[string]*managerActiveTurn),
		errorsByCode: make(map[string]int),
	}
}

// InitializeSession creates or resumes a session and caches its runtime handle.
func (m *Manager) InitializeSession(ctx context.Context, input InitializeSessionInput) (RuntimeHandle, error) {
	key := canonicalSessionKey(input.SessionKey)
	if key == "" {
		return RuntimeHandle{}, ErrSessionKeyRequired
	}
	mode := input.Mode
	if mode == "" {
		mode = SessionModePersistent
	}
	if mode != SessionModePersistent && mode != SessionModeOneshot {
		return RuntimeHandle{}, fmt.Errorf("acp manager: unsupported session mode %q", mode)
	}

	unlock := m.lockSession(key)
	defer unlock()
	backend, err := m.backends.Require(input.Backend)
	if err != nil {
		m.recordError("backend")
		return RuntimeHandle{}, err
	}
	agentID, env := m.resolveAgent(input.Agent, input.Env)
	resumeID := strings.TrimSpace(input.ResumeSessionID)
	if cached := m.getCached(key); cached != nil && cached.Backend != backend.ID {
		_ = cached.Runtime.Close(ctx, CloseInput{Handle: cached.Handle, Reason: "backend-switch"})
		m.clearCached(key)
	}
	if resumeID == "" {
		if rec, _ := m.loadRecord(ctx, key); rec != nil {
			meta := decodeSessionRuntimeMeta(rec)
			if normalizeBackendID(meta.Backend) == backend.ID {
				resumeID = firstNonEmpty(meta.AcpxRecordID, meta.RuntimeSessionName)
			}
		}
	}
	handle, err := backend.Runtime.EnsureSession(ctx, EnsureInput{
		SessionKey:      key,
		Agent:           agentID,
		Mode:            mode,
		ResumeSessionID: resumeID,
		CWD:             strings.TrimSpace(input.CWD),
		Env:             env,
	})
	if err != nil {
		m.recordError("init")
		return RuntimeHandle{}, fmt.Errorf("acp manager: ensure session: %w", err)
	}
	handle = normalizeHandle(handle, key, backend.ID, input.CWD)
	if err := m.applyRuntimeControls(ctx, backend.Runtime, handle, input.Controls); err != nil {
		m.recordError("control")
		return RuntimeHandle{}, err
	}
	now := m.now()
	m.setCached(key, &managerRuntimeState{Runtime: backend.Runtime, Handle: handle, Backend: backend.ID, Agent: agentID, Mode: mode, CWD: handle.CWD, LastUsedAt: now})
	if err := m.saveMeta(ctx, key, agentID, mode, handle, "idle", ""); err != nil {
		m.recordError("store")
		return RuntimeHandle{}, err
	}
	m.mu.Lock()
	m.counters.SessionsInitialized++
	m.mu.Unlock()
	return handle, nil
}

// RunTurn runs one serialized turn and returns all emitted runtime events.
func (m *Manager) RunTurn(ctx context.Context, input RunSessionTurnInput) ([]RuntimeEvent, error) {
	key := canonicalSessionKey(input.SessionKey)
	if key == "" {
		return nil, ErrSessionKeyRequired
	}
	unlock := m.lockSession(key)
	defer unlock()

	state, err := m.ensureRuntimeState(ctx, key, input.Backend, input.Agent)
	if err != nil {
		m.recordError("init")
		return nil, err
	}
	if err := m.applyRuntimeControls(ctx, state.Runtime, state.Handle, input.Controls); err != nil {
		m.recordError("control")
		return nil, err
	}

	turnCtx := ctx
	var cancel context.CancelFunc
	timeout := time.Duration(input.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = m.opts.DefaultTurnTimeout
	}
	if timeout > 0 {
		turnCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		turnCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	active := &managerActiveTurn{Runtime: state.Runtime, Handle: state.Handle, Cancel: cancel, RequestID: strings.TrimSpace(input.RequestID), StartedAt: m.now()}
	m.setActive(key, active)
	m.mu.Lock()
	m.counters.TurnsStarted++
	m.mu.Unlock()
	if err := m.saveMeta(ctx, key, state.Agent, state.Mode, state.Handle, "running", ""); err != nil {
		m.clearActive(key, active)
		return nil, err
	}

	events, runErr := m.consumeTurn(turnCtx, state, input)
	if errors.Is(turnCtx.Err(), context.DeadlineExceeded) {
		active.TimedOut = true
		_ = state.Runtime.Cancel(context.Background(), CancelInput{Handle: state.Handle, Reason: "turn-timeout"})
	}
	m.clearActive(key, active)

	terminal := false
	for _, ev := range events {
		if ev.Kind.IsTerminal() {
			terminal = true
			break
		}
	}
	if runErr == nil && !terminal {
		runErr = fmt.Errorf("acp manager: turn ended without terminal event")
	}

	state.LastUsedAt = m.now()
	m.setCached(key, state)
	if runErr != nil {
		m.recordTurnFailure(active, runErr)
		if state.Mode == SessionModeOneshot {
			m.closeOneShot(key, state, "oneshot-error")
		} else {
			_ = m.saveMeta(context.Background(), key, state.Agent, state.Mode, state.Handle, "error", runErr.Error())
		}
		return events, runErr
	}
	m.mu.Lock()
	m.counters.TurnsCompleted++
	m.mu.Unlock()
	if state.Mode == SessionModeOneshot {
		m.closeOneShot(key, state, "oneshot-complete")
	} else {
		_ = m.saveMeta(context.Background(), key, state.Agent, state.Mode, state.Handle, "idle", "")
	}
	return events, nil
}

// CancelSession cancels an active turn or forwards cancellation to the backend.
func (m *Manager) CancelSession(ctx context.Context, input CancelSessionInput) error {
	key := canonicalSessionKey(input.SessionKey)
	if key == "" {
		return ErrSessionKeyRequired
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "cancelled"
	}
	if active := m.getActive(key); active != nil {
		active.Cancel()
		return active.Runtime.Cancel(ctx, CancelInput{Handle: active.Handle, Reason: reason})
	}

	unlock := m.lockSession(key)
	defer unlock()
	state, err := m.ensureRuntimeState(ctx, key, "", "")
	if err != nil {
		return err
	}
	if err := state.Runtime.Cancel(ctx, CancelInput{Handle: state.Handle, Reason: reason}); err != nil {
		m.recordError("cancel")
		return err
	}
	_ = m.saveMeta(ctx, key, state.Agent, state.Mode, state.Handle, "idle", "")
	m.mu.Lock()
	m.counters.TurnsCanceled++
	m.mu.Unlock()
	return nil
}

// CloseSession closes a runtime session, clears cached handles, and optionally deletes its record.
func (m *Manager) CloseSession(ctx context.Context, input CloseSessionInput) error {
	key := canonicalSessionKey(input.SessionKey)
	if key == "" {
		return ErrSessionKeyRequired
	}
	if m.getActive(key) != nil {
		return fmt.Errorf("%w: %s", ErrTurnActive, key)
	}
	unlock := m.lockSession(key)
	defer unlock()
	state, err := m.ensureRuntimeState(ctx, key, "", "")
	if err != nil && !errors.Is(err, ErrSessionNotFound) {
		return err
	}
	if state != nil {
		if err := state.Runtime.Close(ctx, CloseInput{Handle: state.Handle, Reason: input.Reason, DiscardPersistentState: input.DiscardPersistentState}); err != nil {
			m.recordError("close")
			return err
		}
	}
	m.clearCached(key)
	if input.DeleteRecord || input.DiscardPersistentState {
		if m.sessions != nil {
			if err := m.sessions.Delete(ctx, key); err != nil {
				return err
			}
		}
	} else if state != nil {
		_ = m.saveMeta(ctx, key, state.Agent, state.Mode, state.Handle, "closed", "")
	}
	m.mu.Lock()
	m.counters.SessionsClosed++
	m.mu.Unlock()
	return nil
}

// GetSessionStatus returns status for a session, probing backend status when available.
func (m *Manager) GetSessionStatus(ctx context.Context, sessionKey string) (SessionStatus, error) {
	key := canonicalSessionKey(sessionKey)
	if key == "" {
		return SessionStatus{}, ErrSessionKeyRequired
	}
	unlock := m.lockSession(key)
	defer unlock()
	rec, err := m.loadRecord(ctx, key)
	if err != nil {
		return SessionStatus{}, err
	}
	if rec == nil && m.getCached(key) == nil {
		return SessionStatus{}, ErrSessionNotFound
	}
	return m.sessionStatusFromRecord(ctx, key, rec), nil
}

// Status returns an observability snapshot for the manager and known sessions.
func (m *Manager) Status(ctx context.Context) ManagerStatus {
	_ = m.CleanupIdleRuntimeHandles(ctx)
	m.mu.Lock()
	status := ManagerStatus{
		RuntimeCacheSize: len(m.runtimeCache),
		ActiveTurns:      len(m.activeTurns),
		QueueDepth:       m.queueDepthLocked(),
		Counters:         m.counters,
		ErrorsByCode:     cloneIntMap(m.errorsByCode),
	}
	m.mu.Unlock()
	for _, be := range m.backends.List() {
		status.Backends = append(status.Backends, BackendSnapshot{ID: be.ID, Healthy: be.isHealthy()})
	}
	sort.Slice(status.Backends, func(i, j int) bool { return status.Backends[i].ID < status.Backends[j].ID })
	if m.sessions != nil {
		if records, err := m.sessions.List(ctx); err == nil {
			for _, rec := range records {
				if rec != nil {
					status.Sessions = append(status.Sessions, m.sessionStatusFromRecord(ctx, rec.SessionKey, rec))
				}
			}
			sort.Slice(status.Sessions, func(i, j int) bool { return status.Sessions[i].SessionKey < status.Sessions[j].SessionKey })
		}
	}
	return status
}

// CleanupIdleRuntimeHandles closes cached runtime handles that have been idle longer than RuntimeIdleTTL.
func (m *Manager) CleanupIdleRuntimeHandles(ctx context.Context) error {
	ttl := m.opts.RuntimeIdleTTL
	if ttl <= 0 {
		return nil
	}
	now := m.now()
	var candidates []string
	m.mu.Lock()
	for key, state := range m.runtimeCache {
		if _, active := m.activeTurns[key]; active {
			continue
		}
		if now.Sub(state.LastUsedAt) >= ttl {
			candidates = append(candidates, key)
		}
	}
	m.mu.Unlock()
	for _, key := range candidates {
		unlock := m.lockSession(key)
		m.mu.Lock()
		state := m.runtimeCache[key]
		_, active := m.activeTurns[key]
		if state == nil || active || now.Sub(state.LastUsedAt) < ttl {
			m.mu.Unlock()
			unlock()
			continue
		}
		delete(m.runtimeCache, key)
		m.counters.RuntimeEvicted++
		stateCopy := *state
		m.mu.Unlock()
		if err := stateCopy.Runtime.Close(ctx, CloseInput{Handle: stateCopy.Handle, Reason: "idle-evicted"}); err != nil && ctx.Err() != nil {
			unlock()
			return err
		}
		unlock()
	}
	return nil
}

func (m *Manager) consumeTurn(ctx context.Context, state *managerRuntimeState, input RunSessionTurnInput) ([]RuntimeEvent, error) {
	mode := strings.TrimSpace(input.Mode)
	if mode == "" {
		mode = "prompt"
	}
	ch, err := state.Runtime.RunTurn(ctx, TurnInput{
		Handle:      state.Handle,
		Text:        input.Text,
		Mode:        mode,
		RequestID:   strings.TrimSpace(input.RequestID),
		Attachments: append([]TurnAttachment(nil), input.Attachments...),
	})
	if err != nil {
		return nil, err
	}
	var events []RuntimeEvent
	recordEvent := func(ev RuntimeEvent) bool {
		events = append(events, ev)
		if input.OnEvent != nil {
			input.OnEvent(ev)
		}
		return ev.Kind.IsTerminal()
	}
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events, nil
			}
			if recordEvent(ev) {
				return events, nil
			}
		default:
		}
		select {
		case ev, ok := <-ch:
			if !ok {
				return events, nil
			}
			if recordEvent(ev) {
				return events, nil
			}
		case <-ctx.Done():
			select {
			case ev, ok := <-ch:
				if ok && recordEvent(ev) {
					return events, nil
				}
			default:
			}
			return events, ctx.Err()
		}
	}
}

func (m *Manager) ensureRuntimeState(ctx context.Context, key, requestedBackend, requestedAgent string) (*managerRuntimeState, error) {
	if cached := m.getCached(key); cached != nil {
		requestedBackendID := normalizeBackendID(requestedBackend)
		if requestedBackendID == "" || requestedBackendID == cached.Backend {
			m.mu.Lock()
			m.counters.RuntimeCacheHits++
			m.mu.Unlock()
			cached.LastUsedAt = m.now()
			return cached, nil
		}
		_ = cached.Runtime.Close(ctx, CloseInput{Handle: cached.Handle, Reason: "backend-switch"})
		m.clearCached(key)
	}
	m.mu.Lock()
	m.counters.RuntimeCacheMisses++
	m.mu.Unlock()
	rec, err := m.loadRecord(ctx, key)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		if requestedBackend == "" && requestedAgent == "" {
			return nil, ErrSessionNotFound
		}
		backend, err := m.backends.Require(requestedBackend)
		if err != nil {
			return nil, err
		}
		agentID, env := m.resolveAgent(requestedAgent, nil)
		handle, err := backend.Runtime.EnsureSession(ctx, EnsureInput{
			SessionKey: key,
			Agent:      agentID,
			Mode:       SessionModePersistent,
			Env:        env,
		})
		if err != nil {
			return nil, err
		}
		handle = normalizeHandle(handle, key, backend.ID, "")
		state := &managerRuntimeState{Runtime: backend.Runtime, Handle: handle, Backend: backend.ID, Agent: agentID, Mode: SessionModePersistent, CWD: handle.CWD, LastUsedAt: m.now()}
		m.setCached(key, state)
		_ = m.saveMeta(ctx, key, agentID, SessionModePersistent, handle, "idle", "")
		return state, nil
	}
	meta := decodeSessionRuntimeMeta(rec)
	backendID := firstNonEmpty(requestedBackend, meta.Backend)
	backend, err := m.backends.Require(backendID)
	if err != nil {
		return nil, err
	}
	agentID, env := m.resolveAgent(firstNonEmpty(requestedAgent, meta.Agent), nil)
	mode := meta.Mode
	if mode == "" {
		mode = SessionModePersistent
	}
	resumeID := ""
	if normalizeBackendID(meta.Backend) == backend.ID {
		resumeID = firstNonEmpty(meta.AcpxRecordID, meta.RuntimeSessionName)
	}
	handle, err := backend.Runtime.EnsureSession(ctx, EnsureInput{
		SessionKey:      key,
		Agent:           agentID,
		Mode:            mode,
		ResumeSessionID: resumeID,
		CWD:             meta.CWD,
		Env:             env,
	})
	if err != nil {
		return nil, err
	}
	handle = normalizeHandle(handle, key, backend.ID, meta.CWD)
	state := &managerRuntimeState{Runtime: backend.Runtime, Handle: handle, Backend: backend.ID, Agent: agentID, Mode: mode, CWD: handle.CWD, LastUsedAt: m.now()}
	m.setCached(key, state)
	_ = m.saveMeta(ctx, key, agentID, mode, handle, firstNonEmpty(meta.State, "idle"), meta.LastError)
	return state, nil
}

func (m *Manager) applyRuntimeControls(ctx context.Context, runtime BackendRuntime, handle RuntimeHandle, controls []RuntimeControl) error {
	if len(controls) == 0 {
		return nil
	}
	controller, ok := runtime.(RuntimeControlApplier)
	if !ok {
		return fmt.Errorf("acp manager: runtime backend does not support controls")
	}
	if err := controller.ApplyRuntimeControls(ctx, RuntimeControlInput{Handle: handle, Controls: append([]RuntimeControl(nil), controls...)}); err != nil {
		return err
	}
	m.mu.Lock()
	m.counters.ControlsApplied += int64(len(controls))
	m.mu.Unlock()
	return nil
}

func (m *Manager) sessionStatusFromRecord(ctx context.Context, key string, rec *SessionRecord) SessionStatus {
	meta := SessionRuntimeMeta{}
	if rec != nil {
		meta = decodeSessionRuntimeMeta(rec)
	}
	status := SessionStatus{SessionKey: key, Backend: meta.Backend, Agent: meta.Agent, Mode: meta.Mode, State: meta.State, LastError: meta.LastError, LastActivityAt: meta.LastActivityAt}
	if active := m.getActive(key); active != nil {
		status.ActiveTurn = true
	}
	if cached := m.getCached(key); cached != nil {
		status.Cached = true
		h := cached.Handle
		status.RuntimeHandle = &h
		status.Backend = firstNonEmpty(status.Backend, cached.Backend)
		status.Agent = firstNonEmpty(status.Agent, cached.Agent)
		status.Mode = cached.Mode
		if sp, ok := cached.Runtime.(StatusProvider); ok {
			if runtimeStatus, err := sp.GetStatus(ctx, cached.Handle); err == nil {
				status.RuntimeStatus = &runtimeStatus
			}
		}
		if cp, ok := cached.Runtime.(CapabilitiesProvider); ok {
			if caps, err := cp.GetCapabilities(ctx, &cached.Handle); err == nil {
				status.Capabilities = &caps
			}
		}
	}
	if status.State == "" {
		if status.ActiveTurn {
			status.State = "running"
		} else {
			status.State = "idle"
		}
	}
	return status
}

func (m *Manager) resolveAgent(agent string, env map[string]string) (string, map[string]string) {
	agent = normalizeAgentID(agent)
	if agent == "" {
		agent = "main"
	}
	merged := cloneStringMap(env)
	if m.agents != nil {
		if entry, ok := m.agents.Resolve(agent); ok {
			merged = mergeStringMaps(entry.Env, merged)
		}
	}
	return agent, merged
}

func (m *Manager) closeOneShot(key string, state *managerRuntimeState, reason string) {
	_ = state.Runtime.Close(context.Background(), CloseInput{Handle: state.Handle, Reason: reason, DiscardPersistentState: true})
	m.clearCached(key)
	if m.sessions != nil {
		_ = m.sessions.Delete(context.Background(), key)
	}
}

func (m *Manager) saveMeta(ctx context.Context, key, agent string, mode SessionMode, handle RuntimeHandle, state, lastErr string) error {
	if m.sessions == nil {
		return nil
	}
	existing, _ := m.sessions.Load(ctx, key)
	meta := SessionRuntimeMeta{
		Backend:            handle.Backend,
		Agent:              agent,
		Mode:               mode,
		RuntimeSessionName: handle.RuntimeSessionName,
		CWD:                handle.CWD,
		AcpxRecordID:       handle.AcpxRecordID,
		State:              state,
		LastError:          strings.TrimSpace(lastErr),
		LastActivityAt:     m.now().Unix(),
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	rec := &SessionRecord{SessionKey: key, AgentID: agent, State: raw}
	if existing != nil {
		rec.ID = existing.ID
		rec.CreatedAt = existing.CreatedAt
	}
	return m.sessions.Save(ctx, rec)
}

func (m *Manager) loadRecord(ctx context.Context, key string) (*SessionRecord, error) {
	if m.sessions == nil {
		return nil, nil
	}
	return m.sessions.Load(ctx, key)
}

func decodeSessionRuntimeMeta(rec *SessionRecord) SessionRuntimeMeta {
	if rec == nil || len(rec.State) == 0 {
		return SessionRuntimeMeta{}
	}
	var meta SessionRuntimeMeta
	_ = json.Unmarshal(rec.State, &meta)
	return meta
}

func (m *Manager) recordTurnFailure(active *managerActiveTurn, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if active != nil && active.TimedOut {
		m.counters.TurnsTimedOut++
	} else if errors.Is(err, context.Canceled) {
		m.counters.TurnsCanceled++
	} else {
		m.counters.TurnsFailed++
	}
	m.errorsByCode[errorCode(err)]++
}

func (m *Manager) recordError(code string) {
	m.mu.Lock()
	m.errorsByCode[code]++
	m.mu.Unlock()
}

func (m *Manager) lockSession(key string) func() {
	key = canonicalSessionKey(key)
	m.mu.Lock()
	l := m.locks[key]
	if l == nil {
		l = &sessionActorLock{}
		m.locks[key] = l
	}
	l.pending++
	m.mu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		m.mu.Lock()
		l.pending--
		if l.pending <= 0 {
			delete(m.locks, key)
		}
		m.mu.Unlock()
	}
}

func (m *Manager) queueDepthLocked() int {
	depth := 0
	for _, l := range m.locks {
		if l.pending > 1 {
			depth += l.pending - 1
		}
	}
	return depth
}

func (m *Manager) getCached(key string) *managerRuntimeState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if state := m.runtimeCache[canonicalSessionKey(key)]; state != nil {
		cp := *state
		return &cp
	}
	return nil
}

func (m *Manager) setCached(key string, state *managerRuntimeState) {
	m.mu.Lock()
	m.runtimeCache[canonicalSessionKey(key)] = state
	m.mu.Unlock()
}

func (m *Manager) clearCached(key string) {
	m.mu.Lock()
	delete(m.runtimeCache, canonicalSessionKey(key))
	m.mu.Unlock()
}

func (m *Manager) setActive(key string, active *managerActiveTurn) {
	m.mu.Lock()
	m.activeTurns[canonicalSessionKey(key)] = active
	m.mu.Unlock()
}

func (m *Manager) getActive(key string) *managerActiveTurn {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeTurns[canonicalSessionKey(key)]
}

func (m *Manager) clearActive(key string, active *managerActiveTurn) {
	m.mu.Lock()
	if m.activeTurns[canonicalSessionKey(key)] == active {
		delete(m.activeTurns, canonicalSessionKey(key))
	}
	m.mu.Unlock()
}

func (m *Manager) now() time.Time {
	if m.opts.Now != nil {
		return m.opts.Now()
	}
	return time.Now()
}

func canonicalSessionKey(key string) string { return strings.TrimSpace(key) }

func normalizeHandle(handle RuntimeHandle, sessionKey, backend, cwd string) RuntimeHandle {
	if handle.SessionKey == "" {
		handle.SessionKey = sessionKey
	}
	if handle.Backend == "" {
		handle.Backend = backend
	} else {
		handle.Backend = normalizeBackendID(handle.Backend)
	}
	if handle.CWD == "" {
		handle.CWD = strings.TrimSpace(cwd)
	}
	return handle
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func mergeStringMaps(base, override map[string]string) map[string]string {
	out := cloneStringMap(base)
	if out == nil && len(override) > 0 {
		out = make(map[string]string, len(override))
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

func cloneIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func errorCode(err error) string {
	if err == nil {
		return "unknown"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	return "error"
}
