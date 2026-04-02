package policy

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"

	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

const (
	DMPolicyPairing   = "pairing"
	DMPolicyAllowlist = "allowlist"
	DMPolicyOpen      = "open"
	DMPolicyDisabled  = "disabled"
)

// AuthLevel defines the permission tier of an inbound message sender.
type AuthLevel int

const (
	// AuthDenied — sender is not allowed.
	AuthDenied AuthLevel = iota
	// AuthPublic — sender is on the allowlist (general access).
	AuthPublic
	// AuthTrusted — sender is tagged with the "trusted:" prefix in AllowFrom.
	AuthTrusted
	// AuthOwner — sender is the first entry in AllowFrom (highest privilege).
	AuthOwner
)

// String returns a human-readable description of the auth level.
func (a AuthLevel) String() string {
	switch a {
	case AuthOwner:
		return "owner"
	case AuthTrusted:
		return "trusted"
	case AuthPublic:
		return "public"
	default:
		return "denied"
	}
}

type DMDecision struct {
	Allowed         bool
	RequiresPairing bool
	Reason          string
	// Level is the permission tier of the sender. AuthDenied when !Allowed.
	Level AuthLevel
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
	if strings.TrimSpace(cfg.DM.ReplyScheme) != "" {
		if _, ok := state.ParseDMReplyScheme(cfg.DM.ReplyScheme); !ok {
			return fmt.Errorf("dm.reply_scheme must be one of auto, nip17, nip04")
		}
	}
	if strings.TrimSpace(cfg.ACP.Transport) != "" {
		if _, ok := state.ParseACPTransportMode(cfg.ACP.Transport); !ok {
			return fmt.Errorf("acp.transport must be one of auto, nip17, nip04")
		}
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
	// Validate typed config sections (agents, nostr_channels, providers, session, heartbeat).
	if err := validateAgents(cfg.Agents); err != nil {
		return err
	}
	if err := validateNostrChannels(cfg.NostrChannels); err != nil {
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

// ─── Nostr channel config validation ─────────────────────────────────────────

var validNostrChannelKinds = map[string]bool{
	state.NostrChannelKindDM:          true,
	state.NostrChannelKindNIP28:       true,
	state.NostrChannelKindNIP29:       true,
	state.NostrChannelKindChat:        true,
	state.NostrChannelKindRelayFilter: true,
	state.NostrChannelKindNIP34Inbox:  true,
}

func validateNostrChannels(channels state.NostrChannelsConfig) error {
	for name, ch := range channels {
		if ch.Kind == "" {
			return fmt.Errorf("nostr_channels.%s: kind is required", name)
		}
		if !validNostrChannelKinds[ch.Kind] {
			return fmt.Errorf("nostr_channels.%s: unknown kind %q (valid: dm, nip28, nip29, chat, relay-filter, nip34-inbox)", name, ch.Kind)
		}
		switch ch.Kind {
		case state.NostrChannelKindNIP29:
			if ch.GroupAddress == "" {
				return fmt.Errorf("nostr_channels.%s: group_address is required for nip29 channels", name)
			}
		case state.NostrChannelKindNIP28:
			if ch.ChannelID == "" {
				return fmt.Errorf("nostr_channels.%s: channel_id is required for nip28 channels", name)
			}
		case state.NostrChannelKindNIP34Inbox:
			if len(ch.Tags["a"]) == 0 {
				return fmt.Errorf("nostr_channels.%s: tags.a is required for nip34-inbox channels", name)
			}
		case state.NostrChannelKindRelayFilter:
			if relayFilterUsesNIP34Mode(ch) && len(ch.Tags["a"]) == 0 {
				return fmt.Errorf("nostr_channels.%s: tags.a is required when relay-filter config.mode is nip34", name)
			}
		}
		if err := validateNIP34AutoReviewConfig(name, ch); err != nil {
			return err
		}
		for i, relay := range ch.Relays {
			if _, err := normalizeRelayURL(relay); err != nil {
				return fmt.Errorf("nostr_channels.%s.relays[%d]: %w", name, i, err)
			}
		}
	}
	return nil
}

func relayFilterUsesNIP34Mode(ch state.NostrChannelConfig) bool {
	if ch.Config == nil {
		return false
	}
	for _, key := range []string{"mode", "type"} {
		if raw, ok := ch.Config[key].(string); ok {
			switch strings.TrimSpace(strings.ToLower(raw)) {
			case "nip34", string(state.NostrChannelKindNIP34Inbox):
				return true
			}
		}
	}
	return false
}

func validateNIP34AutoReviewConfig(name string, ch state.NostrChannelConfig) error {
	if ch.Config == nil {
		return nil
	}
	raw, ok := ch.Config["auto_review"]
	if !ok {
		return nil
	}
	if _, ok := raw.(bool); ok {
		return nil
	}
	cfg, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("nostr_channels.%s.config.auto_review must be a boolean or object", name)
	}
	if rawProfile, exists := cfg["tool_profile"]; exists {
		profile, ok := rawProfile.(string)
		if !ok {
			return fmt.Errorf("nostr_channels.%s.config.auto_review.tool_profile must be a string", name)
		}
		profile = strings.TrimSpace(strings.ToLower(profile))
		if !validToolProfiles[profile] {
			return fmt.Errorf("nostr_channels.%s.config.auto_review.tool_profile %q is not valid (valid: minimal, coding, messaging, full)", name, profile)
		}
	}
	for _, key := range []string{"agent_id", "instructions"} {
		if rawValue, exists := cfg[key]; exists {
			if _, ok := rawValue.(string); !ok {
				return fmt.Errorf("nostr_channels.%s.config.auto_review.%s must be a string", name, key)
			}
		}
	}
	for _, key := range []string{"enabled", "followed_only"} {
		if rawValue, exists := cfg[key]; exists {
			if _, ok := rawValue.(bool); !ok {
				return fmt.Errorf("nostr_channels.%s.config.auto_review.%s must be a boolean", name, key)
			}
		}
	}
	if rawTools, exists := cfg["enabled_tools"]; exists {
		switch values := rawTools.(type) {
		case []string:
			for _, value := range values {
				if strings.TrimSpace(value) == "" {
					return fmt.Errorf("nostr_channels.%s.config.auto_review.enabled_tools must not contain empty tool names", name)
				}
			}
		case []any:
			for _, value := range values {
				toolName, ok := value.(string)
				if !ok || strings.TrimSpace(toolName) == "" {
					return fmt.Errorf("nostr_channels.%s.config.auto_review.enabled_tools must be an array of non-empty strings", name)
				}
			}
		default:
			return fmt.Errorf("nostr_channels.%s.config.auto_review.enabled_tools must be an array of strings", name)
		}
	}
	if rawTriggerTypes, exists := cfg["trigger_types"]; exists {
		var values []string
		switch v := rawTriggerTypes.(type) {
		case []string:
			values = v
		case []any:
			values = make([]string, 0, len(v))
			for _, value := range v {
				s, ok := value.(string)
				if !ok {
					return fmt.Errorf("nostr_channels.%s.config.auto_review.trigger_types must be an array of strings", name)
				}
				values = append(values, s)
			}
		default:
			return fmt.Errorf("nostr_channels.%s.config.auto_review.trigger_types must be an array of strings", name)
		}
		validTriggerTypes := map[string]bool{
			"patch":               true,
			"pull_request":        true,
			"pull_request_update": true,
			"issue":               true,
			"status":              true,
		}
		for _, value := range values {
			if !validTriggerTypes[strings.TrimSpace(strings.ToLower(value))] {
				return fmt.Errorf("nostr_channels.%s.config.auto_review.trigger_types contains unsupported value %q", name, value)
			}
		}
	}
	return nil
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
	if mode, ok := state.ParseDMReplyScheme(cfg.DM.ReplyScheme); ok {
		cfg.DM.ReplyScheme = mode
	}
	cfg.Relays.Read = normalizeRelaySet(cfg.Relays.Read)
	cfg.Relays.Write = normalizeRelaySet(cfg.Relays.Write)
	if mode, ok := state.ParseACPTransportMode(cfg.ACP.Transport); ok {
		cfg.ACP.Transport = mode
	}
	if cfg.Storage.Encrypt == nil {
		cfg.Storage.Encrypt = state.BoolPtr(true)
	}
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

// EvaluateGroupMessage evaluates whether a sender is allowed in a group/channel
// context.  channelAllowFrom is the per-channel allow list (from
// NostrChannelConfig.AllowFrom).  When channelAllowFrom is empty, it falls back
// to the global DM allowlist.  A "*" wildcard in either list allows all senders.
func EvaluateGroupMessage(sender string, channelAllowFrom []string, cfg state.ConfigDoc) DMDecision {
	normalizedSender := normalizePubKey(sender)

	// If the channel has its own allowlist, use it exclusively.
	if len(channelAllowFrom) > 0 {
		if isAllowedSender(normalizedSender, channelAllowFrom) {
			return DMDecision{Allowed: true}
		}
		return DMDecision{Allowed: false, Reason: "sender not in channel allowlist"}
	}

	// Fall back to the global DM allowlist (policy-aware).
	dmPolicy := NormalizeDMPolicy(cfg.DM.Policy)
	switch dmPolicy {
	case DMPolicyDisabled:
		return DMDecision{Allowed: false, Reason: "channel messages disabled (dm policy is disabled)"}
	case DMPolicyOpen:
		return DMDecision{Allowed: true}
	default: // allowlist + pairing
		if isAllowedSender(normalizedSender, cfg.DM.AllowFrom) {
			return DMDecision{Allowed: true}
		}
		return DMDecision{Allowed: false, Reason: "sender not in allowlist"}
	}
}

func EvaluateIncomingDM(sender string, cfg state.ConfigDoc) DMDecision {
	normalizedSender := normalizePubKey(sender)
	policy := NormalizeDMPolicy(cfg.DM.Policy)

	switch policy {
	case DMPolicyDisabled:
		return DMDecision{Allowed: false, Reason: "dm policy is disabled", Level: AuthDenied}
	case DMPolicyOpen:
		level := authLevelOf(normalizedSender, cfg.DM.AllowFrom)
		if level == AuthDenied {
			level = AuthPublic // open policy allows all at public level
		}
		return DMDecision{Allowed: true, Level: level}
	case DMPolicyAllowlist:
		level := authLevelOf(normalizedSender, cfg.DM.AllowFrom)
		if level == AuthDenied {
			return DMDecision{Allowed: false, Reason: "sender not in allowlist", Level: AuthDenied}
		}
		return DMDecision{Allowed: true, Level: level}
	case DMPolicyPairing:
		level := authLevelOf(normalizedSender, cfg.DM.AllowFrom)
		if level == AuthDenied {
			return DMDecision{Allowed: false, RequiresPairing: true, Reason: "sender requires pairing approval", Level: AuthDenied}
		}
		return DMDecision{Allowed: true, Level: level}
	default:
		return DMDecision{Allowed: false, Reason: "unknown dm policy", Level: AuthDenied}
	}
}

// authLevelOf returns the AuthLevel of sender within the allowFrom list.
// The first entry is the owner; entries prefixed with "trusted:" are trusted;
// other explicit allowlist entries are treated as trusted for backward
// compatibility (so existing allowlists keep command access).  AuthDenied is
// returned if not found (unless wildcard *).
func authLevelOf(normalizedSender string, allowFrom []string) AuthLevel {
	if normalizedSender == "" {
		return AuthDenied
	}
	for i, entry := range allowFrom {
		raw := strings.TrimSpace(entry)
		// Strip trusted: prefix for comparison.
		trusted := false
		if strings.HasPrefix(strings.ToLower(raw), "trusted:") {
			raw = strings.TrimSpace(raw[len("trusted:"):])
			trusted = true
		}
		if raw == "*" {
			if i == 0 {
				return AuthOwner
			}
			if trusted {
				return AuthTrusted
			}
			return AuthPublic
		}
		if normalizePubKey(raw) == normalizedSender {
			if i == 0 && !trusted {
				return AuthOwner
			}
			return AuthTrusted
		}
	}
	return AuthDenied
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
