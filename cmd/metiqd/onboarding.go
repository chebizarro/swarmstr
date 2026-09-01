package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"

	"metiq/internal/config"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

const onboardingStateVersion = 1

type onboardingIdentity struct {
	Mode           string `json:"mode"`
	PublicKey      string `json:"public_key"`
	PrivateKey     string `json:"private_key,omitempty"`
	SignerURL      string `json:"signer_url,omitempty"`
	NIP46ClientKey string `json:"nip46_client_key,omitempty"`
}

type onboardingPrepared struct {
	Revision         string                        `json:"revision"`
	ReadRelays       []string                      `json:"read_relays"`
	WriteRelays      []string                      `json:"write_relays"`
	WorkspaceDir     string                        `json:"workspace_dir"`
	Providers        state.ProvidersConfig         `json:"providers"`
	CapabilityAdvert nostruntime.FIPSOverlayAdvert `json:"capability_advert"`
}

type onboardingCheck struct {
	Name    string         `json:"name"`
	OK      bool           `json:"ok"`
	Details map[string]any `json:"details,omitempty"`
}

type onboardingVerification struct {
	Revision          string            `json:"revision"`
	Ready             bool              `json:"ready"`
	Checks            []onboardingCheck `json:"checks"`
	NIP65EventID      string            `json:"nip65_event_id,omitempty"`
	CapabilityEventID string            `json:"capability_event_id,omitempty"`
	VerifiedAt        int64             `json:"verified_at,omitempty"`
}

type onboardingState struct {
	Version      int                     `json:"version"`
	Phase        string                  `json:"phase"`
	Sealed       bool                    `json:"sealed"`
	TokenSalt    string                  `json:"token_salt,omitempty"`
	TokenHash    string                  `json:"token_hash,omitempty"`
	Identity     *onboardingIdentity     `json:"identity,omitempty"`
	Prepared     *onboardingPrepared     `json:"prepared,omitempty"`
	Verification *onboardingVerification `json:"verification,omitempty"`
	ActivatedAt  int64                   `json:"activated_at,omitempty"`
	UpdatedAt    int64                   `json:"updated_at"`
}

type onboardingServiceOptions struct {
	BootstrapPath     string
	ConfigPath        string
	StatePath         string
	Bootstrap         config.BootstrapConfig
	Config            state.ConfigDoc
	IdentityCommitted bool
	TokenLogger       func(string)
	ProbeRelay        func(context.Context, string) nostruntime.RelayHealthResult
	ResolveSigner     func(context.Context, config.BootstrapConfig) (nostr.Keyer, error)
	PublishNIP65      func(context.Context, *nostr.Pool, nostr.Keyer, []string, []string, []string) (string, error)
	PublishAdvert     func(context.Context, *nostr.Pool, nostr.Keyer, []string, nostruntime.FIPSOverlayAdvert) (string, error)
}

type onboardingService struct {
	mu        sync.Mutex
	opts      onboardingServiceOptions
	state     onboardingState
	bootstrap config.BootstrapConfig
	config    state.ConfigDoc
}

func defaultOnboardingStatePath(bootstrapPath string) string {
	return bootstrapPath + ".onboarding.json"
}

func openOnboardingService(opts onboardingServiceOptions) (*onboardingService, string, error) {
	if strings.TrimSpace(opts.BootstrapPath) == "" || strings.TrimSpace(opts.ConfigPath) == "" {
		return nil, "", fmt.Errorf("onboarding requires bootstrap and config paths")
	}
	if opts.StatePath == "" {
		opts.StatePath = defaultOnboardingStatePath(opts.BootstrapPath)
	}
	if opts.TokenLogger == nil {
		opts.TokenLogger = func(token string) { log.Printf("METIQ FIRST-RUN SETUP TOKEN: %s", token) }
	}
	if opts.ProbeRelay == nil {
		opts.ProbeRelay = nostruntime.ProbeRelayREQ
	}
	if opts.ResolveSigner == nil {
		opts.ResolveSigner = func(ctx context.Context, bootstrap config.BootstrapConfig) (nostr.Keyer, error) {
			return config.ResolveSigner(ctx, bootstrap, nil)
		}
	}
	if opts.PublishNIP65 == nil {
		opts.PublishNIP65 = func(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, publish, read, write []string) (string, error) {
			return nostruntime.PublishNIP65(ctx, pool, keyer, publish, read, write, nil)
		}
	}
	if opts.PublishAdvert == nil {
		opts.PublishAdvert = func(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, advert nostruntime.FIPSOverlayAdvert) (string, error) {
			return nostruntime.PublishFIPSAdvert(ctx, pool, keyer, relays, nostruntime.FIPSOverlayAdvertIdentifier, nostruntime.DefaultFIPSOverlayAdvertTTL, advert)
		}
	}

	svc := &onboardingService{opts: opts, bootstrap: opts.Bootstrap, config: opts.Config}
	created := false
	raw, err := os.ReadFile(opts.StatePath)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &svc.state); err != nil {
			return nil, "", fmt.Errorf("parse onboarding state: %w", err)
		}
		if svc.state.Version != onboardingStateVersion {
			return nil, "", fmt.Errorf("unsupported onboarding state version %d", svc.state.Version)
		}
	case errors.Is(err, os.ErrNotExist):
		created = true
		svc.state = onboardingState{Version: onboardingStateVersion, Phase: "identity", UpdatedAt: time.Now().Unix()}
	default:
		return nil, "", fmt.Errorf("read onboarding state: %w", err)
	}

	if opts.IdentityCommitted {
		if !svc.state.Sealed {
			svc.sealLocked()
			// A committed bootstrap identity is itself a permanent seal. Legacy
			// installations with no onboarding file need not gain a new write
			// requirement; an existing pending record is persisted and scrubbed.
			if !created {
				if err := svc.persistLocked(); err != nil {
					return nil, "", err
				}
			}
		}
		return svc, "", nil
	}
	if svc.state.Sealed {
		return svc, "", nil
	}
	if svc.state.TokenHash != "" || svc.state.TokenSalt != "" {
		if svc.state.TokenHash == "" || svc.state.TokenSalt == "" {
			return nil, "", fmt.Errorf("incomplete onboarding token verifier")
		}
		return svc, "", nil
	}
	if !created {
		return nil, "", fmt.Errorf("existing unsealed onboarding state has no token verifier")
	}
	tokenBytes := make([]byte, 32)
	saltBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, "", fmt.Errorf("generate setup token: %w", err)
	}
	if _, err := rand.Read(saltBytes); err != nil {
		return nil, "", fmt.Errorf("generate setup token salt: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	svc.state.TokenSalt = hex.EncodeToString(saltBytes)
	svc.state.TokenHash = hashSetupToken(saltBytes, token)
	if err := svc.persistLocked(); err != nil {
		return nil, "", err
	}
	opts.TokenLogger(token)
	return svc, token, nil
}

func hashSetupToken(salt []byte, token string) string {
	h := sha256.New()
	_, _ = h.Write(salt)
	_, _ = h.Write([]byte(token))
	return hex.EncodeToString(h.Sum(nil))
}

func (s *onboardingService) authorizeLocked(token string) error {
	if s == nil {
		return fmt.Errorf("setup unavailable")
	}
	if s.state.Sealed {
		return fmt.Errorf("setup sealed")
	}
	salt, err := hex.DecodeString(s.state.TokenSalt)
	if err != nil || len(salt) != 16 || s.state.TokenHash == "" {
		return fmt.Errorf("setup authorization unavailable")
	}
	got := hashSetupToken(salt, strings.TrimSpace(token))
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.state.TokenHash)) != 1 {
		return fmt.Errorf("setup token invalid")
	}
	return nil
}

func (s *onboardingService) Detect(token string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.authorizeLocked(token); err != nil {
		return nil, err
	}
	return s.detectLocked(), nil
}

func (s *onboardingService) detectLocked() map[string]any {
	out := map[string]any{
		"phase":            s.state.Phase,
		"sealed":           s.state.Sealed,
		"identity_present": s.state.Identity != nil,
		"prepared":         s.state.Prepared != nil,
		"verified":         s.state.Verification != nil && s.state.Verification.Ready,
		"can_activate":     s.state.Verification != nil && s.state.Verification.Ready && s.state.Prepared != nil && s.state.Verification.Revision == s.state.Prepared.Revision,
	}
	if s.state.Identity != nil {
		out["identity"] = map[string]any{"mode": s.state.Identity.Mode, "public_key": s.state.Identity.PublicKey}
	}
	if s.state.Prepared != nil {
		out["revision"] = s.state.Prepared.Revision
	}
	return out
}

func (s *onboardingService) AuthStart(ctx context.Context, req methods.SetupAuthStartRequest) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.authorizeLocked(req.SetupToken); err != nil {
		return nil, err
	}
	if s.state.Identity != nil {
		if s.state.Identity.Mode != req.Mode {
			return nil, fmt.Errorf("setup identity already provisioned with mode %q", s.state.Identity.Mode)
		}
		switch req.Mode {
		case "import_nsec":
			hexKey, err := config.ResolvePrivateKey(config.BootstrapConfig{PrivateKey: req.Nsec})
			if err != nil || nostruntime.MustPublicKeyHex(hexKey) != s.state.Identity.PublicKey {
				return nil, fmt.Errorf("setup identity import conflicts with durable state")
			}
		case "nip46":
			if req.SignerURL != s.state.Identity.SignerURL || (req.NIP46ClientKey != "" && req.NIP46ClientKey != s.state.Identity.NIP46ClientKey) {
				return nil, fmt.Errorf("setup NIP-46 signer conflicts with durable state")
			}
		}
		return map[string]any{"ok": true, "mode": s.state.Identity.Mode, "public_key": s.state.Identity.PublicKey, "resumed": true}, nil
	}

	identity := &onboardingIdentity{Mode: req.Mode}
	result := map[string]any{"ok": true, "mode": req.Mode}
	switch req.Mode {
	case "generate":
		sk := nostr.Generate()
		identity.PrivateKey = hex.EncodeToString(sk[:])
		identity.PublicKey = sk.Public().Hex()
		result["nsec"] = nip19.EncodeNsec([32]byte(sk))
		result["backup_required"] = true
	case "import_nsec":
		hexKey, err := config.ResolvePrivateKey(config.BootstrapConfig{PrivateKey: req.Nsec})
		if err != nil {
			return nil, fmt.Errorf("setup.auth.start: import nsec: %w", err)
		}
		identity.PrivateKey = hexKey
		identity.PublicKey = nostruntime.MustPublicKeyHex(hexKey)
	case "nip46":
		clientKey := req.NIP46ClientKey
		if clientKey == "" {
			generated := nostr.Generate()
			clientKey = hex.EncodeToString(generated[:])
		}
		bootstrap := config.BootstrapConfig{SignerURL: req.SignerURL, NIP46ClientKey: clientKey}
		keyer, err := s.opts.ResolveSigner(ctx, bootstrap)
		if err != nil {
			return nil, fmt.Errorf("setup.auth.start: pair NIP-46 signer: %w", err)
		}
		pk, err := keyer.GetPublicKey(ctx)
		if err != nil {
			return nil, fmt.Errorf("setup.auth.start: NIP-46 public key: %w", err)
		}
		identity.SignerURL = req.SignerURL
		identity.NIP46ClientKey = clientKey
		identity.PublicKey = pk.Hex()
	default:
		return nil, fmt.Errorf("setup.auth.start: unsupported mode")
	}
	s.state.Identity = identity
	s.state.Phase = "identity"
	s.state.UpdatedAt = time.Now().Unix()
	if err := s.persistLocked(); err != nil {
		return nil, err
	}
	result["public_key"] = identity.PublicKey
	return result, nil
}

func (s *onboardingService) Prepare(req methods.SetupPrepareStartRequest) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.authorizeLocked(req.SetupToken); err != nil {
		return nil, err
	}
	if s.state.Identity == nil {
		return nil, fmt.Errorf("setup.prepare.start: identity must be provisioned first")
	}
	prepared := &onboardingPrepared{
		ReadRelays:       append([]string{}, req.ReadRelays...),
		WriteRelays:      append([]string{}, req.WriteRelays...),
		WorkspaceDir:     req.WorkspaceDir,
		Providers:        cloneProviders(req.Providers),
		CapabilityAdvert: req.CapabilityAdvert,
	}
	prepared.Revision = onboardingRevision(prepared)
	resumed := s.state.Prepared != nil && s.state.Prepared.Revision == prepared.Revision
	if !resumed {
		s.state.Prepared = prepared
		s.state.Verification = nil
		s.state.Phase = "prepared"
		s.state.UpdatedAt = time.Now().Unix()
		if err := s.persistLocked(); err != nil {
			return nil, err
		}
	}
	return map[string]any{"ok": true, "revision": prepared.Revision, "resumed": resumed}, nil
}

func (s *onboardingService) Verify(ctx context.Context, token string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.authorizeLocked(token); err != nil {
		return nil, err
	}
	if s.state.Identity == nil || s.state.Prepared == nil {
		return nil, fmt.Errorf("setup.verify: identity and preparation are required")
	}
	if v := s.state.Verification; v != nil && v.Ready && v.Revision == s.state.Prepared.Revision {
		return verificationPayload(v, true), nil
	}

	prepared := s.state.Prepared
	checks := make([]onboardingCheck, 0, 4)
	allRelays := mergeOnboardingRelays(prepared.ReadRelays, prepared.WriteRelays)
	relayDetails := make([]map[string]any, len(allRelays))
	relayOK := true
	type relayResult struct {
		i int
		r nostruntime.RelayHealthResult
	}
	results := make(chan relayResult, len(allRelays))
	for i, relay := range allRelays {
		go func(i int, relay string) { results <- relayResult{i: i, r: s.opts.ProbeRelay(ctx, relay)} }(i, relay)
	}
	for range allRelays {
		item := <-results
		detail := map[string]any{"relay": allRelays[item.i], "reachable": item.r.Reachable}
		if item.r.Latency > 0 {
			detail["latency_ms"] = item.r.Latency.Milliseconds()
		}
		if item.r.Err != nil {
			detail["error"] = item.r.Err.Error()
		}
		relayDetails[item.i] = detail
		relayOK = relayOK && item.r.Reachable
	}
	checks = append(checks, onboardingCheck{Name: "relays_reachable", OK: relayOK, Details: map[string]any{"relays": relayDetails}})

	bootstrap := s.pendingBootstrapLocked()
	keyer, signerErr := s.opts.ResolveSigner(ctx, bootstrap)
	signerOK := signerErr == nil
	if signerOK {
		event := nostr.Event{Kind: 1, CreatedAt: nostr.Now(), Content: "metiq onboarding readiness"}
		signerErr = keyer.SignEvent(ctx, &event)
		signerOK = signerErr == nil && event.CheckID() && event.VerifySignature()
	}
	signerDetails := map[string]any{"public_key": s.state.Identity.PublicKey}
	if signerErr != nil {
		signerDetails["error"] = signerErr.Error()
	}
	checks = append(checks, onboardingCheck{Name: "key_can_sign", OK: signerOK, Details: signerDetails})

	var nip65ID, advertID string
	publishOK := relayOK && signerOK
	publishDetails := map[string]any{}
	if publishOK {
		pool := nostruntime.NewPoolNIP42(keyer)
		defer pool.Close("onboarding verification complete")
		nip65ID, signerErr = s.opts.PublishNIP65(ctx, pool, keyer, allRelays, prepared.ReadRelays, prepared.WriteRelays)
		if signerErr == nil {
			advertID, signerErr = s.opts.PublishAdvert(ctx, pool, keyer, allRelays, prepared.CapabilityAdvert)
		}
		publishOK = signerErr == nil && nip65ID != "" && advertID != ""
	}
	if nip65ID != "" {
		publishDetails["nip65_event_id"] = nip65ID
	}
	if advertID != "" {
		publishDetails["capability_event_id"] = advertID
	}
	if signerErr != nil {
		publishDetails["error"] = signerErr.Error()
	}
	checks = append(checks, onboardingCheck{Name: "metadata_published", OK: publishOK, Details: publishDetails})

	providerDetails := make([]map[string]any, 0, len(prepared.Providers))
	providerOK := len(prepared.Providers) > 0
	providerIDs := make([]string, 0, len(prepared.Providers))
	for id := range prepared.Providers {
		providerIDs = append(providerIDs, id)
	}
	sort.Strings(providerIDs)
	for _, id := range providerIDs {
		entry := prepared.Providers[id]
		configured := strings.TrimSpace(entry.APIKey) != "" || len(entry.APIKeys) > 0
		providerDetails = append(providerDetails, map[string]any{"provider": id, "authenticated": configured, "enabled": entry.Enabled})
		providerOK = providerOK && configured && entry.Enabled
	}
	checks = append(checks, onboardingCheck{Name: "provider_auth", OK: providerOK, Details: map[string]any{"providers": providerDetails}})

	ready := relayOK && signerOK && publishOK && providerOK
	verification := &onboardingVerification{Revision: prepared.Revision, Ready: ready, Checks: checks, NIP65EventID: nip65ID, CapabilityEventID: advertID}
	if ready {
		verification.VerifiedAt = time.Now().Unix()
		s.state.Phase = "verified"
	}
	s.state.Verification = verification
	s.state.UpdatedAt = time.Now().Unix()
	if err := s.persistLocked(); err != nil {
		return nil, err
	}
	return verificationPayload(verification, false), nil
}

func verificationPayload(v *onboardingVerification, resumed bool) map[string]any {
	return map[string]any{
		"ready":               v.Ready,
		"revision":            v.Revision,
		"checks":              v.Checks,
		"nip65_event_id":      v.NIP65EventID,
		"capability_event_id": v.CapabilityEventID,
		"resumed":             resumed,
	}
}

func (s *onboardingService) Activate(token string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.authorizeLocked(token); err != nil {
		return nil, err
	}
	if s.state.Identity == nil || s.state.Prepared == nil || s.state.Verification == nil || !s.state.Verification.Ready || s.state.Verification.Revision != s.state.Prepared.Revision {
		return nil, fmt.Errorf("setup.activate: current preparation has not passed verification")
	}
	bootstrap := s.pendingBootstrapLocked()
	live := s.pendingConfigLocked()
	if err := config.WriteConfigFile(s.opts.ConfigPath, live); err != nil {
		return nil, fmt.Errorf("setup.activate: commit live config: %w", err)
	}
	if err := writeJSONAtomic0600(s.opts.BootstrapPath, bootstrap); err != nil {
		return nil, fmt.Errorf("setup.activate: commit bootstrap config: %w", err)
	}
	s.bootstrap = bootstrap
	s.config = live
	s.sealLocked()
	if err := s.persistLocked(); err != nil {
		return nil, fmt.Errorf("setup.activate: seal onboarding: %w", err)
	}
	return map[string]any{"ok": true, "sealed": true, "activated": true, "public_key": bootstrapIdentityPublicKey(bootstrap)}, nil
}

func (s *onboardingService) pendingBootstrapLocked() config.BootstrapConfig {
	out := s.bootstrap
	out.PrivateKey = s.state.Identity.PrivateKey
	out.SignerURL = s.state.Identity.SignerURL
	out.NIP46ClientKey = s.state.Identity.NIP46ClientKey
	out.Relays = mergeOnboardingRelays(s.state.Prepared.ReadRelays, s.state.Prepared.WriteRelays)
	return out
}

func (s *onboardingService) pendingConfigLocked() state.ConfigDoc {
	out := s.config
	out.Relays = state.RelayPolicy{Read: append([]string{}, s.state.Prepared.ReadRelays...), Write: append([]string{}, s.state.Prepared.WriteRelays...)}
	out.Providers = cloneProviders(s.state.Prepared.Providers)
	agents := append(state.AgentsConfig{}, out.Agents...)
	updated := false
	for i := range agents {
		if strings.EqualFold(strings.TrimSpace(agents[i].ID), "main") {
			agents[i].WorkspaceDir = s.state.Prepared.WorkspaceDir
			updated = true
			break
		}
	}
	if !updated {
		agents = append(agents, state.AgentConfig{ID: "main", WorkspaceDir: s.state.Prepared.WorkspaceDir})
	}
	out.Agents = agents
	if out.Extra == nil {
		out.Extra = map[string]any{}
	}
	out.Extra["onboarding_capability_advert"] = s.state.Prepared.CapabilityAdvert
	return out
}

func (s *onboardingService) sealLocked() {
	s.state.Sealed = true
	s.state.Phase = "sealed"
	s.state.TokenSalt = ""
	s.state.TokenHash = ""
	s.state.Identity = nil
	s.state.Prepared = nil
	s.state.Verification = nil
	s.state.ActivatedAt = time.Now().Unix()
	s.state.UpdatedAt = s.state.ActivatedAt
}

func (s *onboardingService) persistLocked() error {
	return writeJSONAtomic0600(s.opts.StatePath, s.state)
}

func writeJSONAtomic0600(path string, value any) error {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" {
		return fmt.Errorf("write path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err == nil {
		err = dir.Sync()
		_ = dir.Close()
	}
	return err
}

func onboardingRevision(prepared *onboardingPrepared) string {
	clone := *prepared
	clone.Revision = ""
	raw, _ := json.Marshal(clone)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func cloneProviders(in state.ProvidersConfig) state.ProvidersConfig {
	out := make(state.ProvidersConfig, len(in))
	for id, entry := range in {
		entry.APIKeys = append([]string{}, entry.APIKeys...)
		if entry.Extra != nil {
			extra := make(map[string]any, len(entry.Extra))
			for key, value := range entry.Extra {
				extra[key] = value
			}
			entry.Extra = extra
		}
		out[id] = entry
	}
	return out
}

func mergeOnboardingRelays(groups ...[]string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, group := range groups {
		for _, relay := range group {
			relay = strings.TrimSpace(relay)
			if relay == "" {
				continue
			}
			if _, ok := seen[relay]; ok {
				continue
			}
			seen[relay] = struct{}{}
			out = append(out, relay)
		}
	}
	return out
}

func bootstrapIdentityPublicKey(bootstrap config.BootstrapConfig) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	keyer, err := config.ResolveSigner(ctx, bootstrap, nil)
	if err != nil {
		return ""
	}
	pk, err := keyer.GetPublicKey(ctx)
	if err != nil {
		return ""
	}
	return pk.Hex()
}
