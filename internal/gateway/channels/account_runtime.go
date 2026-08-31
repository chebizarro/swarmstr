package channels

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"metiq/internal/plugins/sdk"
)

// AccountState is derived from the real extension handle lifecycle.
type AccountState string

const (
	AccountStopped  AccountState = "stopped"
	AccountStarting AccountState = "starting"
	AccountRunning  AccountState = "running"
	AccountStopping AccountState = "stopping"
	AccountFailed   AccountState = "failed"
)

// AccountSnapshot is the public, credential-free state of one account.
type AccountSnapshot struct {
	Channel            string       `json:"channel"`
	AccountID          string       `json:"account_id"`
	State              AccountState `json:"state"`
	Running            bool         `json:"running"`
	LastTransitionAtMS int64        `json:"last_transition_at_ms"`
	LastError          string       `json:"last_error,omitempty"`
}

// AccountConnection exposes an already-running account without transferring
// ownership of its handles.
type AccountConnection struct {
	Handle    Channel
	RawHandle sdk.ChannelHandle
}

type accountRuntimeEntry struct {
	mu               sync.Mutex
	snapshot         AccountSnapshot
	connection       AccountConnection
	cancel           context.CancelFunc
	generation       uint64
	activeGeneration atomic.Uint64
}

// AccountLifecyclePlugin is an optional extension hook for providers whose
// account lifecycle needs more than the standard Connect/Close contract.
type AccountLifecyclePlugin interface {
	sdk.ChannelPlugin
	StartAccount(context.Context, string, map[string]any, func(sdk.InboundChannelMessage)) (sdk.ChannelHandle, error)
	StopAccount(context.Context, string, sdk.ChannelHandle) error
}

// AccountRuntimeOptions configure the daemon-owned account runtime.
type AccountRuntimeOptions struct {
	Context   context.Context
	OnMessage func(sdk.InboundChannelMessage)
	OnStart   func(AccountSnapshot, AccountConnection)
	OnStop    func(AccountSnapshot)
}

// AccountRuntime owns real extension handles and serializes lifecycle changes
// per configured account. It does not persist synthetic desired-state flags.
type AccountRuntime struct {
	ctx       context.Context
	onMessage func(sdk.InboundChannelMessage)
	onStart   func(AccountSnapshot, AccountConnection)
	onStop    func(AccountSnapshot)

	mu      sync.RWMutex
	entries map[string]*accountRuntimeEntry
}

func NewAccountRuntime(opts AccountRuntimeOptions) *AccountRuntime {
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	r := &AccountRuntime{ctx: ctx, onMessage: opts.OnMessage, onStart: opts.OnStart, onStop: opts.OnStop, entries: map[string]*accountRuntimeEntry{}}
	for _, account := range ConfiguredChannelAccounts() {
		if _, ok := GetChannelPlugin(account.Provider); !ok {
			continue
		}
		key := accountRuntimeKey(account.Provider, account.ID)
		r.entries[key] = &accountRuntimeEntry{snapshot: AccountSnapshot{Channel: account.Provider, AccountID: account.ID, State: AccountStopped, LastTransitionAtMS: time.Now().UnixMilli()}}
	}
	return r
}

func accountRuntimeKey(provider, accountID string) string {
	return normalizeChannelProvider(provider) + "\x00" + strings.TrimSpace(accountID)
}

func (r *AccountRuntime) entry(provider, accountID string) (*accountRuntimeEntry, ResolvedChannelAccount, error) {
	account, err := ResolveConfiguredChannelAccount(provider, accountID)
	if err != nil {
		return nil, ResolvedChannelAccount{}, err
	}
	key := accountRuntimeKey(account.Provider, account.ID)
	r.mu.Lock()
	entry := r.entries[key]
	if entry == nil {
		entry = &accountRuntimeEntry{snapshot: AccountSnapshot{Channel: account.Provider, AccountID: account.ID, State: AccountStopped, LastTransitionAtMS: time.Now().UnixMilli()}}
		r.entries[key] = entry
	}
	r.mu.Unlock()
	return entry, account, nil
}

// Start connects a configured account once. Repeated calls return the same
// running state without invoking the backend again.
func (r *AccountRuntime) Start(ctx context.Context, provider, accountID string) (AccountSnapshot, error) {
	entry, account, err := r.entry(provider, accountID)
	if err != nil {
		return AccountSnapshot{}, err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.connection.Handle != nil {
		return entry.snapshot, nil
	}
	select {
	case <-r.ctx.Done():
		return entry.snapshot, fmt.Errorf("channel runtime is shutting down")
	default:
	}
	plugin, ok := GetChannelPlugin(account.Provider)
	if !ok {
		return entry.snapshot, fmt.Errorf("channel provider %q is not registered", account.Provider)
	}
	entry.snapshot.State = AccountStarting
	entry.snapshot.LastError = ""
	entry.snapshot.LastTransitionAtMS = time.Now().UnixMilli()
	entry.generation++
	generation := entry.generation
	accountCtx, cancel := context.WithCancel(r.ctx)
	onMessage := func(msg sdk.InboundChannelMessage) {
		if entry.activeGeneration.Load() == generation && r.onMessage != nil {
			r.onMessage(msg)
		}
	}
	var handle sdk.ChannelHandle
	var connectErr error
	if lifecycle, ok := plugin.(AccountLifecyclePlugin); ok {
		handle, connectErr = lifecycle.StartAccount(accountCtx, account.ID, cloneAccountParams(account.Config), onMessage)
	} else {
		handle, connectErr = plugin.Connect(accountCtx, account.ID, cloneAccountParams(account.Config), onMessage)
	}
	if connectErr != nil {
		cancel()
		entry.snapshot.State = AccountFailed
		entry.snapshot.Running = false
		entry.snapshot.LastError = connectErr.Error()
		entry.snapshot.LastTransitionAtMS = time.Now().UnixMilli()
		return entry.snapshot, connectErr
	}
	if handle == nil {
		cancel()
		entry.snapshot.State = AccountFailed
		entry.snapshot.LastError = "channel provider returned a nil handle"
		entry.snapshot.LastTransitionAtMS = time.Now().UnixMilli()
		return entry.snapshot, fmt.Errorf("channel provider %q returned a nil handle", account.Provider)
	}
	if cp, ok := plugin.(sdk.ChannelPluginWithCapabilities); ok {
		if err := sdk.ValidateChannelCapabilityContract(cp.Capabilities(), handle); err != nil {
			cancel()
			handle.Close()
			entry.snapshot.State = AccountFailed
			entry.snapshot.Running = false
			entry.snapshot.LastError = err.Error()
			entry.snapshot.LastTransitionAtMS = time.Now().UnixMilli()
			return entry.snapshot, err
		}
	}
	connection := AccountConnection{Handle: &ExtensionHandle{handle: handle}, RawHandle: handle}
	entry.connection = connection
	entry.cancel = cancel
	entry.activeGeneration.Store(generation)
	entry.snapshot.State = AccountRunning
	entry.snapshot.Running = true
	entry.snapshot.LastError = ""
	entry.snapshot.LastTransitionAtMS = time.Now().UnixMilli()
	if r.onStart != nil {
		r.onStart(entry.snapshot, connection)
	}
	return entry.snapshot, nil
}

// Stop closes the real extension handle. Repeated calls are idempotent.
func (r *AccountRuntime) Stop(ctx context.Context, provider, accountID string) (AccountSnapshot, error) {
	entry, _, err := r.entry(provider, accountID)
	if err != nil {
		return AccountSnapshot{}, err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.connection.Handle == nil {
		entry.snapshot.State = AccountStopped
		entry.snapshot.Running = false
		entry.snapshot.LastError = ""
		return entry.snapshot, nil
	}
	entry.snapshot.State = AccountStopping
	entry.snapshot.LastTransitionAtMS = time.Now().UnixMilli()
	if lifecycle, ok := func() (AccountLifecyclePlugin, bool) {
		plugin, exists := GetChannelPlugin(entry.snapshot.Channel)
		if !exists {
			return nil, false
		}
		lifecycle, exists := plugin.(AccountLifecyclePlugin)
		return lifecycle, exists
	}(); ok {
		if err := lifecycle.StopAccount(ctx, entry.snapshot.AccountID, entry.connection.RawHandle); err != nil {
			entry.snapshot.State = AccountRunning
			entry.snapshot.Running = true
			entry.snapshot.LastError = err.Error()
			return entry.snapshot, err
		}
	}
	entry.generation++
	entry.activeGeneration.Store(0)
	if entry.cancel != nil {
		entry.cancel()
	}
	entry.connection.Handle.Close()
	entry.connection = AccountConnection{}
	entry.cancel = nil
	entry.snapshot.State = AccountStopped
	entry.snapshot.Running = false
	entry.snapshot.LastError = ""
	entry.snapshot.LastTransitionAtMS = time.Now().UnixMilli()
	if r.onStop != nil {
		r.onStop(entry.snapshot)
	}
	return entry.snapshot, nil
}

func (r *AccountRuntime) Get(provider, accountID string) (AccountConnection, bool) {
	account, err := ResolveConfiguredChannelAccount(provider, accountID)
	if err != nil {
		return AccountConnection{}, false
	}
	r.mu.RLock()
	entry := r.entries[accountRuntimeKey(account.Provider, account.ID)]
	r.mu.RUnlock()
	if entry == nil {
		return AccountConnection{}, false
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.connection, entry.connection.Handle != nil
}

func (r *AccountRuntime) List() []AccountSnapshot {
	r.mu.RLock()
	entries := make([]*accountRuntimeEntry, 0, len(r.entries))
	for _, entry := range r.entries {
		entries = append(entries, entry)
	}
	r.mu.RUnlock()
	out := make([]AccountSnapshot, 0, len(entries))
	for _, entry := range entries {
		entry.mu.Lock()
		out = append(out, entry.snapshot)
		entry.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Channel == out[j].Channel {
			return out[i].AccountID < out[j].AccountID
		}
		return out[i].Channel < out[j].Channel
	})
	return out
}

func (r *AccountRuntime) StartAll(ctx context.Context) []error {
	accounts := r.List()
	var errs []error
	for _, account := range accounts {
		if _, err := r.Start(ctx, account.Channel, account.AccountID); err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", account.Channel, account.AccountID, err))
		}
	}
	return errs
}

func (r *AccountRuntime) CloseAll() {
	for _, account := range r.List() {
		_, _ = r.Stop(context.Background(), account.Channel, account.AccountID)
	}
}
