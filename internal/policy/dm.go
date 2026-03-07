package policy

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"

	nostruntime "swarmstr/internal/nostr/runtime"
	"swarmstr/internal/store/state"
)

const (
	DMPolicyPairing   = "pairing"
	DMPolicyAllowlist = "allowlist"
	DMPolicyOpen      = "open"
	DMPolicyDisabled  = "disabled"
)

type DMDecision struct {
	Allowed         bool
	RequiresPairing bool
	Reason          string
}

func NormalizeDMPolicy(policy string) string {
	policy = strings.ToLower(strings.TrimSpace(policy))
	if policy == "" {
		return DMPolicyPairing
	}
	return policy
}

func ValidateConfig(cfg state.ConfigDoc) error {
	policy := NormalizeDMPolicy(cfg.DM.Policy)
	switch policy {
	case DMPolicyPairing, DMPolicyAllowlist, DMPolicyOpen, DMPolicyDisabled:
	default:
		return fmt.Errorf("invalid dm policy %q", cfg.DM.Policy)
	}
	if err := validateControlPolicy(cfg.Control); err != nil {
		return err
	}
	if len(cfg.Relays.Read) == 0 {
		return fmt.Errorf("relays.read must include at least one relay")
	}
	if len(cfg.Relays.Write) == 0 {
		return fmt.Errorf("relays.write must include at least one relay")
	}
	for i, relay := range cfg.Relays.Read {
		if _, err := normalizeRelayURL(relay); err != nil {
			return fmt.Errorf("relays.read[%d] invalid: %w", i, err)
		}
	}
	for i, relay := range cfg.Relays.Write {
		if _, err := normalizeRelayURL(relay); err != nil {
			return fmt.Errorf("relays.write[%d] invalid: %w", i, err)
		}
	}
	// Validate typed config sections (agents, providers, session, heartbeat).
	if err := validateAgents(cfg.Agents); err != nil {
		return err
	}
	if err := validateProviders(cfg.Providers); err != nil {
		return err
	}
	if err := validateSession(cfg.Session); err != nil {
		return err
	}
	if err := validateHeartbeat(cfg.Heartbeat); err != nil {
		return err
	}
	return nil
}

// ─── Restart detection ────────────────────────────────────────────────────────

// ConfigChangedNeedsRestart reports whether the change from old to next requires
// a daemon restart to take effect.
//
// Changes that are hot-applied (no restart needed):
//   - DM policy
//   - Read/write relay lists (applied via applyRuntimeRelayPolicy)
//   - Session / heartbeat / TTS / secrets tunables
//
// Changes that require restart:
//   - Agent default model (must rebuild the live agent runtime)
//   - Providers map (API key / base URL changes affect the HTTP provider)
//   - Extensions / plugins (require Go runtime reload)
func ConfigChangedNeedsRestart(old, next state.ConfigDoc) bool {
	if old.Agent.DefaultModel != next.Agent.DefaultModel {
		return true
	}
	if !providersEqual(old.Providers, next.Providers) {
		return true
	}
	// extensions live in doc.Extra["extensions"]
	oldExt, _ := old.Extra["extensions"]
	newExt, _ := next.Extra["extensions"]
	if !reflect.DeepEqual(oldExt, newExt) {
		return true
	}
	return false
}

func providersEqual(a, b state.ProvidersConfig) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		if av.Enabled != bv.Enabled || av.APIKey != bv.APIKey || av.BaseURL != bv.BaseURL || av.Model != bv.Model {
			return false
		}
	}
	return true
}

var validToolProfiles = map[string]bool{
	"":          true, // unset = use default
	"minimal":   true,
	"coding":    true,
	"messaging": true,
	"full":      true,
}

func validateAgents(agents state.AgentsConfig) error {
	seen := map[string]bool{}
	for i, a := range agents {
		id := strings.TrimSpace(a.ID)
		if id == "" {
			return fmt.Errorf("agents[%d]: id is required", i)
		}
		if seen[id] {
			return fmt.Errorf("agents[%d]: duplicate id %q", i, id)
		}
		seen[id] = true
		if !validToolProfiles[a.ToolProfile] {
			return fmt.Errorf("agents[%d] (%s): tool_profile %q is not valid (valid: minimal, coding, messaging, full)", i, id, a.ToolProfile)
		}
		if a.HeartbeatMS < 0 {
			return fmt.Errorf("agents[%d] (%s): heartbeat_ms must be >= 0", i, id)
		}
		if a.HistoryLimit < 0 {
			return fmt.Errorf("agents[%d] (%s): history_limit must be >= 0", i, id)
		}
	}
	return nil
}

func validateProviders(p state.ProvidersConfig) error {
	for name, entry := range p {
		if entry.BaseURL == "" {
			continue
		}
		u, err := url.Parse(strings.TrimSpace(entry.BaseURL))
		if err != nil {
			return fmt.Errorf("providers.%s.base_url: malformed URL: %w", name, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("providers.%s.base_url: scheme must be http or https (got %q)", name, u.Scheme)
		}
		if u.Host == "" {
			return fmt.Errorf("providers.%s.base_url: missing host", name)
		}
	}
	return nil
}

func validateSession(s state.SessionConfig) error {
	if s.TTLSeconds < 0 {
		return fmt.Errorf("session.ttl_seconds must be >= 0 (got %d)", s.TTLSeconds)
	}
	if s.MaxSessions < 0 {
		return fmt.Errorf("session.max_sessions must be >= 0 (got %d)", s.MaxSessions)
	}
	if s.HistoryLimit < 0 {
		return fmt.Errorf("session.history_limit must be >= 0 (got %d)", s.HistoryLimit)
	}
	return nil
}

func validateHeartbeat(h state.HeartbeatConfig) error {
	if h.Enabled && h.IntervalMS < 0 {
		return fmt.Errorf("heartbeat.interval_ms must be >= 0 (got %d)", h.IntervalMS)
	}
	return nil
}

func NormalizeConfig(cfg state.ConfigDoc) state.ConfigDoc {
	cfg.DM.Policy = NormalizeDMPolicy(cfg.DM.Policy)
	cfg.Relays.Read = normalizeRelaySet(cfg.Relays.Read)
	cfg.Relays.Write = normalizeRelaySet(cfg.Relays.Write)
	return cfg
}

func validateControlPolicy(control state.ControlPolicy) error {
	for i, admin := range control.Admins {
		if strings.TrimSpace(admin.PubKey) == "" {
			return fmt.Errorf("control admins[%d].pubkey is required", i)
		}
		if _, err := nostruntime.ParsePubKey(admin.PubKey); err != nil {
			return fmt.Errorf("control admins[%d].pubkey invalid: %w", i, err)
		}
	}
	return nil
}

func EvaluateIncomingDM(sender string, cfg state.ConfigDoc) DMDecision {
	normalizedSender := normalizePubKey(sender)
	policy := NormalizeDMPolicy(cfg.DM.Policy)

	switch policy {
	case DMPolicyDisabled:
		return DMDecision{Allowed: false, Reason: "dm policy is disabled"}
	case DMPolicyOpen:
		return DMDecision{Allowed: true}
	case DMPolicyAllowlist:
		if isAllowedSender(normalizedSender, cfg.DM.AllowFrom) {
			return DMDecision{Allowed: true}
		}
		return DMDecision{Allowed: false, Reason: "sender not in allowlist"}
	case DMPolicyPairing:
		if isAllowedSender(normalizedSender, cfg.DM.AllowFrom) {
			return DMDecision{Allowed: true}
		}
		return DMDecision{Allowed: false, RequiresPairing: true, Reason: "sender requires pairing approval"}
	default:
		return DMDecision{Allowed: false, Reason: "unknown dm policy"}
	}
}

func isAllowedSender(sender string, allow []string) bool {
	if sender == "" {
		return false
	}
	for _, entry := range allow {
		value := strings.TrimSpace(entry)
		if value == "*" {
			return true
		}
		if normalizePubKey(value) == sender {
			return true
		}
	}
	return false
}

func normalizePubKey(raw string) string {
	pk, err := nostruntime.ParsePubKey(raw)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	return pk.Hex()
}

func normalizeRelaySet(relays []string) []string {
	out := make([]string, 0, len(relays))
	seen := map[string]struct{}{}
	for _, relay := range relays {
		norm, err := normalizeRelayURL(relay)
		if err != nil {
			continue
		}
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, norm)
	}
	return out
}

func normalizeRelayURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty relay url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u.Scheme = strings.ToLower(strings.TrimSpace(u.Scheme))
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return "", fmt.Errorf("relay scheme must be ws or wss")
	}
	if strings.TrimSpace(u.Host) == "" {
		return "", fmt.Errorf("relay host is required")
	}
	u.Fragment = ""
	return u.String(), nil
}
