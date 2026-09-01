package secrets

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	maxSecretProviderBytes = 1 << 20
	defaultExecTimeout     = 5 * time.Second
	sentinelPrefix         = "metiq-sent-v1."
)

type SecretRefSource string

const (
	SecretRefEnv   SecretRefSource = "env"
	SecretRefFile  SecretRefSource = "file"
	SecretRefExec  SecretRefSource = "exec"
	SecretRefStore SecretRefSource = "store"
)

// SecretRef is the structured secret contract shared by configuration and
// lifecycle APIs. It is safe to serialize because it never contains a value.
type SecretRef struct {
	Source   SecretRefSource `json:"source"`
	Provider string          `json:"provider,omitempty"`
	ID       string          `json:"id"`
}

// SecretProviderConfig binds a named provider to a file or executable. File and
// exec output must be JSON; SecretRef.ID selects a string via JSON Pointer.
type SecretProviderConfig struct {
	FilePath string        `json:"file_path,omitempty" yaml:"file_path,omitempty"`
	Command  string        `json:"command,omitempty" yaml:"command,omitempty"`
	Args     []string      `json:"args,omitempty" yaml:"args,omitempty"`
	Timeout  time.Duration `json:"timeout,omitempty" yaml:"timeout,omitempty"`
}

type SecretSnapshotState string

const (
	SecretSnapshotCold  SecretSnapshotState = "cold"
	SecretSnapshotFresh SecretSnapshotState = "fresh"
	SecretSnapshotStale SecretSnapshotState = "stale"
)

// SecretSnapshot is an immutable owner-scoped view. Raw values and sentinel
// maps are deliberately unexported and therefore absent from JSON/log output.
type SecretSnapshot struct {
	Owner      string              `json:"owner"`
	Generation uint64              `json:"generation"`
	State      SecretSnapshotState `json:"state"`
	CreatedAt  time.Time           `json:"created_at"`
	LastError  string              `json:"last_error,omitempty"`
	values     map[string]string
	sentinels  map[string]string
}

// Lifecycle resolves structured refs and publishes generation-ordered snapshots.
type Lifecycle struct {
	store *Store

	mu        sync.RWMutex
	providers map[string]SecretProviderConfig
	snapshots map[string]*SecretSnapshot
	refreshes map[string]uint64
}

func NewLifecycle(store *Store) *Lifecycle {
	if store == nil {
		store = NewStore(nil)
	}
	return &Lifecycle{
		store:     store,
		providers: map[string]SecretProviderConfig{},
		snapshots: map[string]*SecretSnapshot{},
		refreshes: map[string]uint64{},
	}
}

func (l *Lifecycle) SetProvider(name string, cfg SecretProviderConfig) error {
	name = strings.TrimSpace(name)
	if !providerNamePattern.MatchString(name) {
		return errors.New("invalid secret provider name")
	}
	cfg.FilePath = strings.TrimSpace(cfg.FilePath)
	cfg.Command = strings.TrimSpace(cfg.Command)
	cfg.Args = append([]string(nil), cfg.Args...)
	if cfg.FilePath == "" && cfg.Command == "" {
		return errors.New("secret provider requires file_path or command")
	}
	if cfg.FilePath != "" && cfg.Command != "" {
		return errors.New("secret provider cannot configure both file and exec")
	}
	l.mu.Lock()
	l.providers[name] = cfg
	l.mu.Unlock()
	return nil
}

var (
	providerNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	envNamePattern      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	sentinelPattern     = regexp.MustCompile(`metiq-sent-v1\.[A-Za-z0-9]+\.end`)
)

func validateSecretRef(ref SecretRef) error {
	ref.Provider = strings.TrimSpace(ref.Provider)
	ref.ID = strings.TrimSpace(ref.ID)
	if ref.ID == "" {
		return errors.New("secret ref id is required")
	}
	if ref.Provider != "" && !providerNamePattern.MatchString(ref.Provider) {
		return errors.New("invalid secret provider name")
	}
	switch ref.Source {
	case SecretRefEnv:
		if ref.Provider != "" {
			return errors.New("env secret ref cannot specify provider")
		}
		if !envNamePattern.MatchString(ref.ID) {
			return errors.New("invalid environment secret id")
		}
	case SecretRefFile, SecretRefExec:
		if ref.Provider == "" {
			return errors.New("file and exec secret refs require provider")
		}
	case SecretRefStore:
	default:
		return errors.New("unsupported secret ref source")
	}
	return nil
}

func (l *Lifecycle) ResolveRef(ctx context.Context, ref SecretRef) (string, error) {
	if err := validateSecretRef(ref); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	switch ref.Source {
	case SecretRefEnv:
		if value, ok := os.LookupEnv(ref.ID); ok {
			return value, nil
		}
		l.store.mu.RLock()
		value, ok := l.store.values[ref.ID]
		l.store.mu.RUnlock()
		if !ok {
			return "", errSecretNotFound
		}
		return value, nil
	case SecretRefStore:
		key := ref.ID
		if ref.Provider != "" {
			key = ref.Provider + "/" + ref.ID
		}
		l.store.mu.RLock()
		primary, fallback := l.store.backend, l.store.fallback
		l.store.mu.RUnlock()
		// Gateway-managed values are written only to a backend that guarantees
		// protection at rest. Never consult the plaintext fallback for this
		// provider, even if the primary backend becomes unavailable.
		if ref.Provider == gatewayStoreProvider {
			protected, ok := primary.(ProtectedSecretBackend)
			if !ok || protected == nil || !protected.ProtectedAtRest() {
				return "", ErrProtectedBackendUnavailable
			}
			value, found, err := protected.Get(key)
			if err != nil {
				return "", errors.New("protected secret store read failed")
			}
			if !found {
				return "", errSecretNotFound
			}
			return value, nil
		}
		var primaryFailed bool
		if primary != nil {
			if value, ok, err := primary.Get(key); err != nil {
				primaryFailed = true
			} else if ok {
				return value, nil
			}
		}
		if fallback != nil {
			if value, ok, err := fallback.Get(key); err != nil {
				return "", errors.New("secret store fallback read failed")
			} else if ok {
				return value, nil
			}
		}
		if primaryFailed {
			return "", errors.New("secret store primary read failed")
		}
		return "", errSecretNotFound
	case SecretRefFile, SecretRefExec:
		l.mu.RLock()
		cfg, ok := l.providers[ref.Provider]
		l.mu.RUnlock()
		if !ok {
			return "", errors.New("secret provider is not configured")
		}
		var raw []byte
		var err error
		if ref.Source == SecretRefFile {
			raw, err = readSecretProviderFile(cfg)
		} else {
			raw, err = runSecretProvider(ctx, cfg)
		}
		if err != nil {
			return "", err
		}
		return selectJSONString(raw, ref.ID)
	default:
		return "", errors.New("unsupported secret ref source")
	}
}

func readSecretProviderFile(cfg SecretProviderConfig) ([]byte, error) {
	if cfg.FilePath == "" || cfg.Command != "" {
		return nil, errors.New("secret file provider is misconfigured")
	}
	path, err := filepath.Abs(cfg.FilePath)
	if err != nil {
		return nil, errors.New("secret file path is invalid")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, errors.New("secret file is unavailable")
	}
	if !info.Mode().IsRegular() || info.Size() > maxSecretProviderBytes {
		return nil, errors.New("secret file must be regular and at most 1 MiB")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("secret file permissions must not allow group or other access")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("secret file read failed")
	}
	return raw, nil
}

type boundedBuffer struct {
	buf      []byte
	overflow bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	remaining := maxSecretProviderBytes - len(b.buf)
	if remaining > 0 {
		take := len(p)
		if take > remaining {
			take = remaining
		}
		b.buf = append(b.buf, p[:take]...)
	}
	if len(p) > remaining {
		b.overflow = true
	}
	return len(p), nil
}

func runSecretProvider(ctx context.Context, cfg SecretProviderConfig) ([]byte, error) {
	if cfg.Command == "" || cfg.FilePath != "" {
		return nil, errors.New("secret exec provider is misconfigured")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultExecTimeout
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(execCtx, cfg.Command, cfg.Args...)
	var stdout, stderr boundedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			return nil, errors.New("secret exec provider timed out")
		}
		return nil, errors.New("secret exec provider failed")
	}
	if stdout.overflow || stderr.overflow {
		return nil, errors.New("secret exec provider output exceeded 1 MiB")
	}
	return stdout.buf, nil
}

func selectJSONString(raw []byte, pointer string) (string, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", errors.New("secret provider returned invalid JSON")
	}
	if pointer == "" {
		return "", errors.New("secret JSON pointer is required")
	}
	if !strings.HasPrefix(pointer, "/") {
		pointer = "/" + pointer
	}
	current := value
	for _, segment := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return "", errors.New("secret JSON pointer does not resolve to an object field")
		}
		current, ok = object[segment]
		if !ok {
			return "", errSecretNotFound
		}
	}
	result, ok := current.(string)
	if !ok {
		return "", errors.New("secret JSON pointer must resolve to a string")
	}
	return result, nil
}

// Refresh resolves every logical ref, then atomically publishes the newest
// completed refresh. A failed first refresh is cold; later failures are stale
// and retain the last known-good values and sentinels.
func (l *Lifecycle) Refresh(ctx context.Context, owner string, refs map[string]SecretRef) SecretSnapshot {
	owner = strings.TrimSpace(owner)
	l.mu.Lock()
	l.refreshes[owner]++
	requestGeneration := l.refreshes[owner]
	previous := l.snapshots[owner]
	l.mu.Unlock()

	values := make(map[string]string, len(refs))
	var refreshErr error
	for logical, ref := range refs {
		logical = strings.TrimSpace(logical)
		if logical == "" {
			refreshErr = errors.New("secret snapshot logical name is required")
			break
		}
		value, err := l.ResolveRef(ctx, ref)
		if err != nil {
			refreshErr = err
			break
		}
		values[logical] = value
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.refreshes[owner] != requestGeneration {
		if current := l.snapshots[owner]; current != nil {
			return cloneSnapshot(current)
		}
		return SecretSnapshot{Owner: owner, Generation: requestGeneration, State: SecretSnapshotCold, CreatedAt: time.Now(), LastError: "superseded secret refresh"}
	}
	if refreshErr != nil {
		if previous == nil || previous.State == SecretSnapshotCold {
			snapshot := &SecretSnapshot{Owner: owner, Generation: requestGeneration, State: SecretSnapshotCold, CreatedAt: time.Now(), LastError: sanitizeRefreshError(refreshErr)}
			l.snapshots[owner] = snapshot
			return cloneSnapshot(snapshot)
		}
		snapshot := cloneSnapshotPtr(previous)
		snapshot.Generation = requestGeneration
		snapshot.State = SecretSnapshotStale
		snapshot.CreatedAt = time.Now()
		snapshot.LastError = sanitizeRefreshError(refreshErr)
		l.snapshots[owner] = snapshot
		return cloneSnapshot(snapshot)
	}
	sentinels := make(map[string]string, len(values))
	for logical := range values {
		sentinels[logical] = newSentinel()
	}
	snapshot := &SecretSnapshot{Owner: owner, Generation: requestGeneration, State: SecretSnapshotFresh, CreatedAt: time.Now(), values: values, sentinels: sentinels}
	l.snapshots[owner] = snapshot
	return cloneSnapshot(snapshot)
}

func sanitizeRefreshError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "secret refresh canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "secret refresh timed out"
	case errors.Is(err, errSecretNotFound):
		return "secret reference not found"
	default:
		return err.Error()
	}
}

func newSentinel() string {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Sprintf("%s%d.end", sentinelPrefix, time.Now().UnixNano())
	}
	return sentinelPrefix + hex.EncodeToString(random[:]) + ".end"
}

func cloneSnapshotPtr(in *SecretSnapshot) *SecretSnapshot {
	copy := cloneSnapshot(in)
	return &copy
}

func cloneSnapshot(in *SecretSnapshot) SecretSnapshot {
	if in == nil {
		return SecretSnapshot{}
	}
	out := *in
	out.values = make(map[string]string, len(in.values))
	for key, value := range in.values {
		out.values[key] = value
	}
	out.sentinels = make(map[string]string, len(in.sentinels))
	for key, sentinel := range in.sentinels {
		out.sentinels[key] = sentinel
	}
	return out
}

func (l *Lifecycle) Snapshot(owner string) (SecretSnapshot, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	snapshot, ok := l.snapshots[strings.TrimSpace(owner)]
	if !ok {
		return SecretSnapshot{}, false
	}
	return cloneSnapshot(snapshot), true
}

// Sentinel returns the egress placeholder for a logical name. It never returns
// the underlying value.
func (s SecretSnapshot) Sentinel(logical string) (string, bool) {
	value, ok := s.sentinels[strings.TrimSpace(logical)]
	return value, ok
}

// ExpandForEgress replaces only this snapshot's sentinels. Any sentinel-shaped
// token left in the payload belongs to another/unknown snapshot and fails closed.
func (s SecretSnapshot) ExpandForEgress(payload []byte) ([]byte, error) {
	out := string(payload)
	allowed := make(map[string]string, len(s.sentinels))
	for logical, sentinel := range s.sentinels {
		value, ok := s.values[logical]
		if !ok {
			return nil, errors.New("secret snapshot is internally inconsistent")
		}
		allowed[sentinel] = value
	}
	for _, token := range sentinelPattern.FindAllString(out, -1) {
		if _, ok := allowed[token]; !ok {
			return nil, errors.New("payload contains unknown secret sentinel")
		}
	}
	for sentinel, value := range allowed {
		out = strings.ReplaceAll(out, sentinel, value)
	}
	return []byte(out), nil
}
