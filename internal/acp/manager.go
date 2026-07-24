package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ctxengine "metiq/internal/context"
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
	// FallbackBackends are attempted in order after the requested or persisted
	// primary backend when an early transient failure is safe to retry.
	FallbackBackends []string
	// RuntimeIdleTTL controls CleanupIdleRuntimeHandles. <=0 disables idle cleanup.
	RuntimeIdleTTL time.Duration
	// MaxSpawnDepth limits managed child session nesting. <=0 uses the default.
	MaxSpawnDepth int
	// MaxChildrenPerSession limits direct managed children per parent. <=0 uses the default.
	MaxChildrenPerSession int
	// TurnTimeoutGrace allows a backend to emit cleanup/terminal events after the
	// primary turn deadline before the manager gives up waiting.
	TurnTimeoutGrace time.Duration
	// TurnTimeoutCleanupGrace bounds the backend Cancel call made on timeout.
	TurnTimeoutCleanupGrace time.Duration
	// SessionRateLimitMax limits InitializeSession calls per fixed window. <=0 disables.
	SessionRateLimitMax int
	// SessionRateLimitWindow is the fixed window for SessionRateLimitMax.
	SessionRateLimitWindow time.Duration
	// EventLedger records runtime events for replay/audit.
	EventLedger EventLedger
	// ContextEngine is notified when managed child sessions are spawned so
	// subagent context can be forked or isolated before the backend starts.
	ContextEngine ctxengine.Engine
	// FlowRegistry exposes flow orchestration state in Manager.Status.
	FlowRegistry *FlowRegistry
	// ProcessLeases tracks backend process leases for observability/reaping.
	ProcessLeases *ProcessLeaseRegistry
	// ApprovalRouter forwards runtime approval requests to supervising sessions.
	ApprovalRouter ApprovalRouter
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
	ledger       EventLedger
	flows        *FlowRegistry
	processes    *ProcessLeaseRegistry
	rateLimiter  *FixedWindowRateLimiter
	approval     ApprovalRouter
	latencies    []int64
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
	Runtime    BackendRuntime
	Handle     RuntimeHandle
	Cancel     context.CancelCauseFunc
	RequestID  string
	StartedAt  time.Time
	TimedOut   bool
	Canceled   atomic.Bool
	cancelOnce sync.Once
	cancelDone chan struct{}
	cancelErr  error
}

// ManagerCounters captures cumulative manager activity.
type ManagerCounters struct {
	SessionsInitialized int64            `json:"sessions_initialized"`
	SessionsClosed      int64            `json:"sessions_closed"`
	RuntimeCacheHits    int64            `json:"runtime_cache_hits"`
	RuntimeCacheMisses  int64            `json:"runtime_cache_misses"`
	RuntimeEvicted      int64            `json:"runtime_evicted"`
	TurnsStarted        int64            `json:"turns_started"`
	TurnsCompleted      int64            `json:"turns_completed"`
	TurnsFailed         int64            `json:"turns_failed"`
	TurnsCanceled       int64            `json:"turns_canceled"`
	TurnsTimedOut       int64            `json:"turns_timed_out"`
	ControlsApplied     int64            `json:"controls_applied"`
	SessionsSpawned     int64            `json:"sessions_spawned"`
	TurnLatency         TurnLatencyStats `json:"turn_latency"`
}

// TurnLatencyStats tracks coarse turn duration metrics in milliseconds. P95MS
// is computed over a bounded rolling sample window; the other fields are
// lifetime aggregates.
type TurnLatencyStats struct {
	Count   int64 `json:"count"`
	TotalMS int64 `json:"total_ms"`
	MinMS   int64 `json:"min_ms"`
	MaxMS   int64 `json:"max_ms"`
	P95MS   int64 `json:"p95_ms"`
}

func (s TurnLatencyStats) MeanMS() int64 {
	if s.Count == 0 {
		return 0
	}
	return s.TotalMS / s.Count
}

// ApprovalRoute is the explicit supervisor routing envelope for a runtime
// approval request emitted by a worker/child session.
type ApprovalRoute struct {
	Request              ApprovalRequest `json:"request"`
	WorkerSessionKey     string          `json:"worker_session_key"`
	SupervisorSessionKey string          `json:"supervisor_session_key"`
	RequestID            string          `json:"request_id,omitempty"`
	ThreadID             string          `json:"thread_id,omitempty"`
	Event                RuntimeEvent    `json:"event"`
}

// ApprovalRouter forwards approval requests to the supervising agent/session.
type ApprovalRouter interface {
	RouteApprovalRequest(ctx context.Context, route ApprovalRoute) error
}

type ApprovalRouterFunc func(ctx context.Context, route ApprovalRoute) error

func (f ApprovalRouterFunc) RouteApprovalRequest(ctx context.Context, route ApprovalRoute) error {
	return f(ctx, route)
}

// PendingPrompt records a prompt that was interrupted by a client disconnect
// before a terminal event was observed. It can be replayed on reconnect.
type PendingPrompt struct {
	Text        string           `json:"text"`
	Mode        string           `json:"mode,omitempty"`
	RequestID   string           `json:"request_id,omitempty"`
	TimeoutMS   int64            `json:"timeout_ms,omitempty"`
	Attachments []TurnAttachment `json:"attachments,omitempty"`
	CreatedAt   int64            `json:"created_at"`
}

// ManagerStatus is an observability snapshot for the manager.
type ManagerStatus struct {
	RuntimeCacheSize int                `json:"runtime_cache_size"`
	ActiveTurns      int                `json:"active_turns"`
	QueueDepth       int                `json:"queue_depth"`
	Counters         ManagerCounters    `json:"counters"`
	ErrorsByCode     map[string]int     `json:"errors_by_code,omitempty"`
	Sessions         []SessionStatus    `json:"sessions,omitempty"`
	Backends         []BackendSnapshot  `json:"backends,omitempty"`
	Tasks            *TaskStoreStats    `json:"tasks,omitempty"`
	EventLedger      *EventLedgerStats  `json:"event_ledger,omitempty"`
	Flows            []FlowRecord       `json:"flows,omitempty"`
	ProcessLeases    *ProcessLeaseStats `json:"process_leases,omitempty"`
}

// BackendSnapshot is a redacted backend registry entry for status output.
type BackendSnapshot struct {
	ID      string `json:"id"`
	Healthy bool   `json:"healthy"`
}

// SessionRuntimeMeta is persisted in SessionRecord.State by Manager.
type SessionRuntimeMeta struct {
	Backend            string         `json:"backend,omitempty"`
	Agent              string         `json:"agent,omitempty"`
	Mode               SessionMode    `json:"mode,omitempty"`
	RuntimeSessionName string         `json:"runtime_session_name,omitempty"`
	CWD                string         `json:"cwd,omitempty"`
	AcpxRecordID       string         `json:"acpx_record_id,omitempty"`
	BackendSessionID   string         `json:"backend_session_id,omitempty"`
	AgentSessionID     string         `json:"agent_session_id,omitempty"`
	State              string         `json:"state,omitempty"`
	LastError          string         `json:"last_error,omitempty"`
	LastActivityAt     int64          `json:"last_activity_at,omitempty"`
	ParentSessionKey   string         `json:"parent_session_key,omitempty"`
	SpawnDepth         int            `json:"spawn_depth,omitempty"`
	ThreadID           string         `json:"thread_id,omitempty"`
	SpawnedBy          string         `json:"spawned_by,omitempty"`
	ChildSessionKeys   []string       `json:"child_session_keys,omitempty"`
	PendingPrompt      *PendingPrompt `json:"pending_prompt,omitempty"`
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
	SessionKey       string             `json:"session_key"`
	Backend          string             `json:"backend,omitempty"`
	FallbackBackends []string           `json:"fallback_backends,omitempty"`
	Agent            string             `json:"agent,omitempty"`
	Mode             string             `json:"mode,omitempty"`
	Text             string             `json:"text"`
	RequestID        string             `json:"request_id,omitempty"`
	TimeoutMS        int64              `json:"timeout_ms,omitempty"`
	Attachments      []TurnAttachment   `json:"attachments,omitempty"`
	Controls         []RuntimeControl   `json:"controls,omitempty"`
	OnEvent          func(RuntimeEvent) `json:"-"`
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
	opts.FallbackBackends = append([]string(nil), opts.FallbackBackends...)
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
	if opts.TurnTimeoutGrace == 0 {
		opts.TurnTimeoutGrace = 5 * time.Second
	}
	if opts.TurnTimeoutCleanupGrace <= 0 {
		opts.TurnTimeoutCleanupGrace = 2 * time.Second
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
		ledger:       opts.EventLedger,
		flows:        opts.FlowRegistry,
		processes:    opts.ProcessLeases,
		rateLimiter:  NewFixedWindowRateLimiter(opts.SessionRateLimitMax, opts.SessionRateLimitWindow, opts.Now),
		approval:     opts.ApprovalRouter,
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
	chargeRateLimit := true
	if cached := m.getCached(key); cached != nil {
		requestedBackendID := normalizeBackendID(input.Backend)
		if requestedBackendID == "" || requestedBackendID == cached.Backend {
			chargeRateLimit = false
		}
	}
	if chargeRateLimit {
		if rec, _ := m.loadRecord(ctx, key); rec != nil {
			chargeRateLimit = false
		}
	}
	if chargeRateLimit && !m.rateLimiter.Allow() {
		m.recordError("rate_limited")
		return RuntimeHandle{}, AcpError{Code: "rate_limited", Message: "ACP session creation rate limit exceeded", Retryable: true}
	}

	unlock := m.lockSession(key)
	defer unlock()
	backend, err := m.backends.Require(input.Backend)
	if err != nil {
		acpErr := ToAcpRuntimeError(err, AcpCodeBackendUnavailable, "ACP runtime backend unavailable")
		m.recordError(acpErr.Code)
		return RuntimeHandle{}, acpErr
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
		acpErr := ToAcpRuntimeError(fmt.Errorf("acp manager: ensure session: %w", err), AcpCodeSessionInitFailed, "ACP session initialization failed")
		m.recordError(acpErr.Code)
		return RuntimeHandle{}, acpErr
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
	if m.ledger != nil {
		_ = m.ledger.StartSession(ctx, key, handle.CWD)
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

	resolvedBackend := ""
	if cached := m.getCached(key); cached != nil {
		resolvedBackend = cached.Backend
	} else {
		record, err := m.loadRecord(ctx, key)
		if err != nil {
			return nil, err
		}
		if record != nil {
			resolvedBackend = decodeSessionRuntimeMeta(record).Backend
		}
	}
	fallbacks := input.FallbackBackends
	if len(fallbacks) == 0 {
		fallbacks = m.opts.FallbackBackends
	}
	candidates := resolveBackendCandidatePlan(input.Backend, resolvedBackend, fallbacks)
	timeout := time.Duration(input.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = m.opts.DefaultTurnTimeout
	}
	overallStartedAt := m.now()
	pending := &PendingPrompt{
		Text:        input.Text,
		Mode:        strings.TrimSpace(input.Mode),
		RequestID:   strings.TrimSpace(input.RequestID),
		TimeoutMS:   input.TimeoutMS,
		Attachments: append([]TurnAttachment(nil), input.Attachments...),
		CreatedAt:   overallStartedAt.Unix(),
	}
	var attempts []BackendAttempt
	var turnStarted bool

	for candidateIndex, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		state, err := m.ensureRuntimeState(ctx, key, candidate, input.Agent)
		if err != nil {
			attempt := backendAttempt(candidate, err, AcpCodeSessionInitFailed, "ACP session initialization failed", false)
			attempts = append(attempts, attempt)
			if isFailoverWorthyBackendAttempt(attempt) && candidateIndex < len(candidates)-1 {
				continue
			}
			failErr := BackendFailoverError{Attempts: attempts}
			m.recordError(errorCode(failErr))
			return nil, failErr
		}
		if err := m.applyRuntimeControls(ctx, state.Runtime, state.Handle, input.Controls); err != nil {
			attempt := backendAttempt(state.Backend, err, AcpCodeBackendUnsupportedControl, "ACP runtime backend control failed", false)
			attempts = append(attempts, attempt)
			failErr := BackendFailoverError{Attempts: attempts}
			m.recordError(errorCode(failErr))
			return nil, failErr
		}

		turnCtx, cancelTurn := context.WithCancelCause(ctx)
		active := &managerActiveTurn{Runtime: state.Runtime, Handle: state.Handle, Cancel: cancelTurn, RequestID: strings.TrimSpace(input.RequestID), StartedAt: overallStartedAt, cancelDone: make(chan struct{})}
		m.setActive(key, active)
		if !turnStarted {
			turnStarted = true
			m.mu.Lock()
			m.counters.TurnsStarted++
			m.mu.Unlock()
		}
		if err := m.saveMetaWithPending(ctx, key, state.Agent, state.Mode, state.Handle, "running", "", pending, false); err != nil {
			cancelTurn(nil)
			m.clearActive(key, active)
			return nil, err
		}

		events, runErr := m.consumeTurn(turnCtx, state, input, timeout, active)
		if errors.Is(runErr, context.DeadlineExceeded) {
			active.TimedOut = true
		}
		terminal := false
		for _, event := range events {
			if event.Kind.IsTerminal() {
				terminal = true
				break
			}
		}
		if runErr == nil && !terminal {
			runErr = fmt.Errorf("acp manager: turn ended without terminal event")
		}
		cancelTurn(nil)
		m.clearActive(key, active)

		if runErr == nil {
			state.LastUsedAt = m.now()
			m.recordTurnLatency(state.LastUsedAt.Sub(overallStartedAt))
			m.setCached(key, state)
			m.mu.Lock()
			m.counters.TurnsCompleted++
			m.mu.Unlock()
			if state.Mode == SessionModeOneshot {
				m.closeOneShot(key, state, "oneshot-complete")
			} else {
				_ = m.saveMetaWithPending(context.Background(), key, state.Agent, state.Mode, state.Handle, "idle", "", nil, true)
			}
			return events, nil
		}

		sawOutput := turnEventsSawOutput(events)
		attempt := backendAttempt(state.Backend, runErr, AcpCodeTurnFailed, "ACP turn failed before completion", sawOutput)
		attempts = append(attempts, attempt)
		if isFailoverWorthyBackendAttempt(attempt) && candidateIndex < len(candidates)-1 {
			continue
		}

		failErr := BackendFailoverError{Attempts: attempts}
		state.LastUsedAt = m.now()
		m.recordTurnLatency(state.LastUsedAt.Sub(overallStartedAt))
		m.setCached(key, state)
		m.recordTurnFailure(active, failErr)
		if state.Mode == SessionModeOneshot {
			m.closeOneShot(key, state, "oneshot-error")
		} else {
			clearPending := true
			if errors.Is(failErr, context.Canceled) && !active.Canceled.Load() {
				clearPending = false
			}
			_ = m.saveMetaWithPending(context.Background(), key, state.Agent, state.Mode, state.Handle, "error", failErr.Error(), nil, clearPending)
		}
		return events, failErr
	}

	failErr := BackendFailoverError{Attempts: attempts}
	m.recordError(errorCode(failErr))
	return nil, failErr
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
		active.Canceled.Store(true)
		return m.cancelActiveTurn(active, reason, fmt.Errorf("%s: %w", reason, context.Canceled))
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
		_ = m.saveMetaWithPending(ctx, key, state.Agent, state.Mode, state.Handle, "closed", "", nil, true)
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
	if m.dispatcher != nil && m.dispatcher.TaskStore() != nil {
		if stats, err := m.dispatcher.TaskStore().Stats(ctx); err == nil {
			status.Tasks = &stats
		}
	}
	if m.ledger != nil {
		stats := m.ledger.Stats(ctx)
		status.EventLedger = &stats
	}
	if m.flows != nil {
		if flows, err := m.flows.List(ctx, FlowFilter{Limit: 100}); err == nil {
			status.Flows = flows
		}
	}
	if m.processes != nil {
		stats := m.processes.Stats(ctx)
		status.ProcessLeases = &stats
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
		stateCopy := *state
		m.mu.Unlock()

		// Try to close the runtime before removing from cache
		if err := stateCopy.Runtime.Close(ctx, CloseInput{Handle: stateCopy.Handle, Reason: "idle-evicted"}); err != nil {
			unlock()
			// Only return error if it's a real error, not context cancellation
			if ctx.Err() == nil {
				return err
			}
			// Context was cancelled, ignore this Close error and continue cleanup
			continue
		}

		// Close succeeded, now remove from cache
		m.mu.Lock()
		delete(m.runtimeCache, key)
		m.counters.RuntimeEvicted++
		m.mu.Unlock()
		unlock()
	}
	return nil
}

func (m *Manager) consumeTurn(ctx context.Context, state *managerRuntimeState, input RunSessionTurnInput, timeout time.Duration, active *managerActiveTurn) ([]RuntimeEvent, error) {
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
		if m.ledger != nil {
			_ = m.ledger.RecordEvent(context.Background(), state.Handle.SessionKey, strings.TrimSpace(input.RequestID), ev)
		}
		if m.dispatcher != nil && strings.TrimSpace(input.RequestID) != "" && !ev.Kind.IsTerminal() {
			m.dispatcher.RecordProgress(context.Background(), input.RequestID, firstNonEmpty(ev.Text, ev.Title, string(ev.Kind)))
		}
		if ev.Kind == EventApprovalRequest && ev.ApprovalRequest != nil {
			if err := m.routeApprovalRequest(context.Background(), state.Handle.SessionKey, strings.TrimSpace(input.RequestID), ev); err != nil {
				m.recordError(AcpErrorApprovalRoute)
			}
		}
		if input.OnEvent != nil {
			input.OnEvent(ev)
		}
		return ev.Kind.IsTerminal()
	}
	closedTurn := func() ([]RuntimeEvent, error) {
		cause := context.Cause(ctx)
		if cause == nil {
			return events, nil
		}
		_ = m.cancelActiveTurn(active, "turn-disconnect", cause)
		return events, cause
	}
	var timeoutC <-chan time.Time
	var timeoutTimer *time.Timer
	if timeout > 0 {
		timeoutTimer = time.NewTimer(timeout)
		defer timeoutTimer.Stop()
		timeoutC = timeoutTimer.C
	}
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return closedTurn()
			}
			if recordEvent(ev) {
				return events, runtimeTerminalEventError(ev)
			}
		default:
		}
		select {
		case ev, ok := <-ch:
			if !ok {
				return closedTurn()
			}
			if recordEvent(ev) {
				return events, runtimeTerminalEventError(ev)
			}
		case <-timeoutC:
			timeoutErr := NewTurnTimeoutError(fmt.Sprintf("ACP turn timed out after %v", timeout))
			_ = m.cancelActiveTurn(active, "turn-timeout", timeoutErr)
			return m.drainTurnGrace(ch, &events, recordEvent, timeoutErr)
		case <-ctx.Done():
			select {
			case ev, ok := <-ch:
				if ok && recordEvent(ev) {
					return events, runtimeTerminalEventError(ev)
				}
			default:
			}
			cause := context.Cause(ctx)
			if cause == nil {
				cause = ctx.Err()
			}
			_ = m.cancelActiveTurn(active, "turn-disconnect", cause)
			return events, cause
		}
	}
}

func runtimeTerminalEventError(event RuntimeEvent) error {
	if event.Kind != EventError {
		return nil
	}
	message := strings.TrimSpace(event.Text)
	if message == "" {
		message = "ACP turn failed before completion"
	}
	return AcpError{
		Code:       AcpCodeTurnFailed,
		Message:    message,
		DetailCode: strings.TrimSpace(event.Code),
		Retryable:  event.Retryable,
	}
}

// ReconcilePendingPrompt resumes a prompt that was interrupted before a
// terminal event, typically because a gateway/client disconnected. Call this
// after InitializeSession reconnects the runtime session.
func (m *Manager) ReconcilePendingPrompt(ctx context.Context, sessionKey string) ([]RuntimeEvent, error) {
	key := canonicalSessionKey(sessionKey)
	if key == "" {
		return nil, ErrSessionKeyRequired
	}
	rec, err := m.loadRecord(ctx, key)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, ErrSessionNotFound
	}
	meta := decodeSessionRuntimeMeta(rec)
	if meta.PendingPrompt == nil {
		return nil, nil
	}
	pending := meta.PendingPrompt
	return m.RunTurn(ctx, RunSessionTurnInput{
		SessionKey:  key,
		Backend:     meta.Backend,
		Agent:       meta.Agent,
		Mode:        pending.Mode,
		Text:        pending.Text,
		RequestID:   pending.RequestID,
		TimeoutMS:   pending.TimeoutMS,
		Attachments: append([]TurnAttachment(nil), pending.Attachments...),
	})
}

func (m *Manager) routeApprovalRequest(ctx context.Context, workerSessionKey, requestID string, ev RuntimeEvent) error {
	if ev.ApprovalRequest == nil {
		return nil
	}
	workerSessionKey = canonicalSessionKey(workerSessionKey)
	supervisor := workerSessionKey
	threadID := ""
	if rec, _ := m.loadRecord(ctx, workerSessionKey); rec != nil {
		meta := decodeSessionRuntimeMeta(rec)
		if meta.ParentSessionKey != "" {
			supervisor = meta.ParentSessionKey
		}
		threadID = meta.ThreadID
	}
	route := ApprovalRoute{
		Request:              cloneApprovalRequest(*ev.ApprovalRequest),
		WorkerSessionKey:     workerSessionKey,
		SupervisorSessionKey: supervisor,
		RequestID:            strings.TrimSpace(requestID),
		ThreadID:             threadID,
		Event:                cloneRuntimeEvent(ev),
	}
	if m.ledger != nil && supervisor != "" && supervisor != workerSessionKey {
		_ = m.ledger.RecordEvent(ctx, supervisor, route.RequestID, route.Event)
	}
	if m.approval == nil {
		return nil
	}
	return m.approval.RouteApprovalRequest(ctx, route)
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

func (m *Manager) cancelActiveTurn(active *managerActiveTurn, reason string, cause error) error {
	if active == nil || active.Runtime == nil {
		return nil
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "cancelled"
	}
	if cause == nil {
		cause = context.Canceled
	}
	active.cancelOnce.Do(func() {
		active.Cancel(cause)
		go func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), m.opts.TurnTimeoutCleanupGrace)
			defer cleanupCancel()
			active.cancelErr = active.Runtime.Cancel(cleanupCtx, CancelInput{Handle: active.Handle, Reason: reason})
			close(active.cancelDone)
		}()
	})
	waitTimer := time.NewTimer(m.opts.TurnTimeoutCleanupGrace)
	defer waitTimer.Stop()
	select {
	case <-active.cancelDone:
		return active.cancelErr
	case <-waitTimer.C:
		return context.DeadlineExceeded
	}
}

func (m *Manager) drainTurnGrace(ch <-chan RuntimeEvent, events *[]RuntimeEvent, recordEvent func(RuntimeEvent) bool, timeoutErr error) ([]RuntimeEvent, error) {
	grace := m.opts.TurnTimeoutGrace
	if grace <= 0 {
		return *events, timeoutErr
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return *events, timeoutErr
			}
			if recordEvent(ev) {
				return *events, timeoutErr
			}
		case <-timer.C:
			return *events, timeoutErr
		}
	}
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
		h.BackendSessionID = firstNonEmpty(h.BackendSessionID, meta.BackendSessionID, meta.AcpxRecordID, h.AcpxRecordID, h.RuntimeSessionName, h.SessionKey)
		h.AgentSessionID = firstNonEmpty(h.AgentSessionID, meta.AgentSessionID, h.RuntimeSessionName, h.BackendSessionID)
		status.RuntimeHandle = &h
		status.Backend = firstNonEmpty(status.Backend, cached.Backend)
		status.Agent = firstNonEmpty(status.Agent, cached.Agent)
		status.Mode = cached.Mode
		if sp, ok := cached.Runtime.(StatusProvider); ok {
			if runtimeStatus, err := sp.GetStatus(ctx, cached.Handle); err == nil {
				runtimeStatus.AcpxRecordID = firstNonEmpty(runtimeStatus.AcpxRecordID, h.AcpxRecordID, meta.AcpxRecordID)
				runtimeStatus.BackendSessionID = firstNonEmpty(runtimeStatus.BackendSessionID, h.BackendSessionID, meta.BackendSessionID)
				runtimeStatus.AgentSessionID = firstNonEmpty(runtimeStatus.AgentSessionID, h.AgentSessionID, meta.AgentSessionID)
				status.RuntimeStatus = &runtimeStatus
			}
		}
		if cp, ok := cached.Runtime.(CapabilitiesProvider); ok {
			if caps, err := cp.GetCapabilities(ctx, &cached.Handle); err == nil {
				status.Capabilities = &caps
			}
		}
	} else if meta.BackendSessionID != "" || meta.AgentSessionID != "" || meta.AcpxRecordID != "" || meta.RuntimeSessionName != "" {
		h := normalizeHandle(RuntimeHandle{SessionKey: key, Backend: meta.Backend, RuntimeSessionName: meta.RuntimeSessionName, CWD: meta.CWD, AcpxRecordID: meta.AcpxRecordID, BackendSessionID: meta.BackendSessionID, AgentSessionID: meta.AgentSessionID}, key, meta.Backend, meta.CWD)
		status.RuntimeHandle = &h
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
	return m.saveMetaWithPending(ctx, key, agent, mode, handle, state, lastErr, nil, false)
}

func (m *Manager) saveMetaWithPending(ctx context.Context, key, agent string, mode SessionMode, handle RuntimeHandle, state, lastErr string, pending *PendingPrompt, clearPending bool) error {
	if m.sessions == nil {
		return nil
	}
	existing, _ := m.sessions.Load(ctx, key)
	existingMeta := decodeSessionRuntimeMeta(existing)
	meta := SessionRuntimeMeta{
		Backend:            handle.Backend,
		Agent:              agent,
		Mode:               mode,
		RuntimeSessionName: handle.RuntimeSessionName,
		CWD:                handle.CWD,
		AcpxRecordID:       handle.AcpxRecordID,
		BackendSessionID:   handle.BackendSessionID,
		AgentSessionID:     handle.AgentSessionID,
		State:              state,
		LastError:          strings.TrimSpace(lastErr),
		LastActivityAt:     m.now().Unix(),
		ParentSessionKey:   existingMeta.ParentSessionKey,
		SpawnDepth:         existingMeta.SpawnDepth,
		ThreadID:           existingMeta.ThreadID,
		SpawnedBy:          existingMeta.SpawnedBy,
		ChildSessionKeys:   append([]string(nil), existingMeta.ChildSessionKeys...),
	}
	if pending != nil {
		cp := *pending
		cp.Attachments = append([]TurnAttachment(nil), pending.Attachments...)
		meta.PendingPrompt = &cp
	} else if !clearPending {
		meta.PendingPrompt = existingMeta.PendingPrompt
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

func (m *Manager) recordTurnLatency(d time.Duration) {
	ms := d.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	m.mu.Lock()
	stats := &m.counters.TurnLatency
	stats.Count++
	stats.TotalMS += ms
	if stats.Count == 1 || ms < stats.MinMS {
		stats.MinMS = ms
	}
	if ms > stats.MaxMS {
		stats.MaxMS = ms
	}
	m.latencies = append(m.latencies, ms)
	const maxLatencySamples = 2048
	if len(m.latencies) > maxLatencySamples {
		copy(m.latencies, m.latencies[len(m.latencies)-maxLatencySamples:])
		m.latencies = m.latencies[:maxLatencySamples]
	}
	stats.P95MS = percentileNearestRank(m.latencies, 0.95)
	m.mu.Unlock()
}

func percentileNearestRank(samples []int64, p float64) int64 {
	if len(samples) == 0 {
		return 0
	}
	cp := append([]int64(nil), samples...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	rank := int(p * float64(len(cp)))
	if float64(rank) < p*float64(len(cp)) {
		rank++
	}
	if rank < 1 {
		rank = 1
	}
	if rank > len(cp) {
		rank = len(cp)
	}
	return cp[rank-1]
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

// lockSession acquires a per-session lock and returns an unlock function.
//
// Lock ordering invariant:
//   - Acquisition: m.mu → l.mu
//   - Release: l.mu → m.mu (LIFO)
//
// This two-level locking pattern allows:
//   - m.mu protects the map of per-session locks (short critical section)
//   - l.mu serializes operations on a specific session (can be held longer)
//   - pending counter tracks waiters to clean up unused locks
//
// Safety properties:
//   - LIFO ordering (acquire m.mu first, release it last) prevents deadlock
//   - The unlock function re-acquires m.mu AFTER releasing l.mu to update pending count
//   - No other code path acquires l.mu before m.mu, maintaining the invariant
//
// Callers must invoke the returned unlock function exactly once, typically via defer.
func (m *Manager) lockSession(key string) func() {
	key = canonicalSessionKey(key)
	// Acquire global lock to access the locks map
	m.mu.Lock()
	l := m.locks[key]
	if l == nil {
		l = &sessionActorLock{}
		m.locks[key] = l
	}
	l.pending++
	m.mu.Unlock()

	// Acquire per-session lock (can be held for duration of session operation)
	l.mu.Lock()

	return func() {
		// Release in LIFO order: l.mu first, then m.mu
		l.mu.Unlock()

		// Re-acquire global lock to update pending count and clean up if needed
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
	if handle.BackendSessionID == "" {
		handle.BackendSessionID = firstNonEmpty(handle.AcpxRecordID, handle.RuntimeSessionName, handle.SessionKey)
	}
	if handle.AgentSessionID == "" {
		handle.AgentSessionID = firstNonEmpty(handle.RuntimeSessionName, handle.BackendSessionID)
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

func cloneApprovalRequest(in ApprovalRequest) ApprovalRequest {
	out := in
	if len(in.Metadata) > 0 {
		out.Metadata = make(map[string]any, len(in.Metadata))
		for k, v := range in.Metadata {
			out.Metadata[k] = v
		}
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
	var acpErr AcpError
	if errors.As(err, &acpErr) {
		if acpErr.DetailCode != "" {
			return acpErr.DetailCode
		}
		if acpErr.Code != "" {
			return acpErr.Code
		}
	}
	var acpErrPtr *AcpError
	if errors.As(err, &acpErrPtr) && acpErrPtr != nil {
		if acpErrPtr.DetailCode != "" {
			return acpErrPtr.DetailCode
		}
		if acpErrPtr.Code != "" {
			return acpErrPtr.Code
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	return "error"
}
