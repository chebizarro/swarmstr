// Package config – ConfigDoc semantic validation.
//
// ValidateConfigDoc checks the logical consistency of a ConfigDoc without
// mutating it.  Errors are collected and returned so callers can decide
// whether to reject (hard error) or warn.

package config

import (
	"fmt"
	"net/url"
	"strings"

	"metiq/internal/store/state"
)

// ValidateConfigDoc validates the semantic constraints of a ConfigDoc.
// It does not enforce schema shape (that is handled by JSON unmarshalling);
// instead it checks values that are structurally valid JSON but logically wrong,
// such as malformed relay URLs or unknown DM policy strings.
//
// All errors are collected; an empty slice means the document is valid.
func ValidateConfigDoc(doc state.ConfigDoc) []error {
	var errs []error

	errs = append(errs, validateDMPolicy(doc.DM)...)
	errs = append(errs, validateRelays(doc.Relays)...)
	errs = append(errs, validateAgentPolicy(doc.Agent)...)
	errs = append(errs, validateAgents(doc.Agents)...)
	errs = append(errs, validateProviders(doc.Providers)...)
	errs = append(errs, validateSession(doc.Session)...)
	errs = append(errs, validateHeartbeat(doc.Heartbeat)...)
	errs = append(errs, validateTTS(doc.TTS)...)
	errs = append(errs, validateFIPS(doc.FIPS)...)

	return errs
}

// ── DM Policy ─────────────────────────────────────────────────────────────────

var validDMPolicies = map[string]bool{
	"pairing":   true,
	"allowlist": true,
	"open":      true,
	"disabled":  true,
}

func validateDMPolicy(p state.DMPolicy) []error {
	if p.Policy == "" {
		return nil // unset is allowed (defaults applied at runtime)
	}
	if !validDMPolicies[p.Policy] {
		return []error{fmt.Errorf("dm.policy: unknown value %q (valid: pairing, allowlist, open, disabled)", p.Policy)}
	}
	return nil
}

// ── Relays ────────────────────────────────────────────────────────────────────

func validateRelays(r state.RelayPolicy) []error {
	var errs []error
	for i, u := range r.Read {
		if err := validateRelayURL(u); err != nil {
			errs = append(errs, fmt.Errorf("relays.read[%d] %q: %w", i, u, err))
		}
	}
	for i, u := range r.Write {
		if err := validateRelayURL(u); err != nil {
			errs = append(errs, fmt.Errorf("relays.write[%d] %q: %w", i, u, err))
		}
	}
	return errs
}

func validateRelayURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("empty URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("malformed URL: %w", err)
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return fmt.Errorf("scheme must be ws or wss (got %q)", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("missing host")
	}
	return nil
}

// ── Agent Policy ──────────────────────────────────────────────────────────────

// knownModels is a lightweight set of known model identifiers.
// The empty string is deliberately excluded (means "use default").
var knownModels = map[string]bool{
	"echo":         true,
	"http":         true,
	"http-default": true,
}

func validateAgentPolicy(a state.AgentPolicy) []error {
	var errs []error
	if a.DefaultModel != "" && !knownModels[a.DefaultModel] {
		// Warn only – third-party model strings like "gpt-4o" are valid too.
		// We record the error so callers can emit a warning without blocking.
		errs = append(errs, fmt.Errorf("agent.default_model: unrecognised model %q (may still work at runtime)", a.DefaultModel))
	}
	return errs
}

func validateAgents(agents state.AgentsConfig) []error {
	var errs []error
	for i, ag := range agents {
		if raw := strings.TrimSpace(string(ag.MemoryScope)); raw != "" && !ag.MemoryScope.Valid() {
			errs = append(errs, fmt.Errorf("agents[%d].memory_scope: unknown value %q (valid: user, project, local)", i, raw))
		}
		if ag.LightModelThreshold < 0 || ag.LightModelThreshold > 1 {
			errs = append(errs, fmt.Errorf("agents[%d].light_model_threshold must be between 0 and 1 (got %v)", i, ag.LightModelThreshold))
		}
		if ag.LightModelThreshold != 0 && strings.TrimSpace(ag.LightModel) == "" {
			errs = append(errs, fmt.Errorf("agents[%d].light_model_threshold requires light_model", i))
		}
	}
	return errs
}

// ── Providers ─────────────────────────────────────────────────────────────────

func validateProviders(p state.ProvidersConfig) []error {
	var errs []error
	for name, entry := range p {
		if entry.BaseURL != "" {
			if err := validateHTTPURL(entry.BaseURL); err != nil {
				errs = append(errs, fmt.Errorf("providers.%s.base_url: %w", name, err))
			}
		}
	}
	return errs
}

func validateHTTPURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("empty URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("malformed URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https (got %q)", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("missing host")
	}
	return nil
}

// ── Session ───────────────────────────────────────────────────────────────────

func validateSession(s state.SessionConfig) []error {
	var errs []error
	if s.TTLSeconds < 0 {
		errs = append(errs, fmt.Errorf("session.ttl_seconds must be >= 0 (got %d)", s.TTLSeconds))
	}
	return errs
}

// ── Heartbeat ─────────────────────────────────────────────────────────────────

func validateHeartbeat(h state.HeartbeatConfig) []error {
	if h.Enabled && h.IntervalMS < 0 {
		return []error{fmt.Errorf("heartbeat.interval_ms must be >= 0 (got %d)", h.IntervalMS)}
	}
	return nil
}

// ── TTS ───────────────────────────────────────────────────────────────────────

func validateTTS(t state.TTSConfig) []error {
	// No hard constraints beyond structure; provider names are open-ended.
	return nil
}

// ── FIPS ──────────────────────────────────────────────────────────────────────

func validateFIPS(f state.FIPSConfig) []error {
	if !f.Enabled {
		return nil // disabled — skip all validation
	}
	var errs []error

	// Transport preference must be a known value.
	if f.TransportPref != "" {
		if _, ok := state.ParseFIPSTransportPref(f.TransportPref); !ok {
			errs = append(errs, fmt.Errorf("fips.transport_pref: unknown value %q (valid: fips-first, relay-first, fips-only)", f.TransportPref))
		}
	}

	// Port numbers must be in valid range.
	if f.AgentPort < 0 || f.AgentPort > 65535 {
		errs = append(errs, fmt.Errorf("fips.agent_port: must be 0–65535 (got %d)", f.AgentPort))
	}
	if f.ControlPort < 0 || f.ControlPort > 65535 {
		errs = append(errs, fmt.Errorf("fips.control_port: must be 0–65535 (got %d)", f.ControlPort))
	}

	// Peer npubs should be parseable.
	for i, peer := range f.Peers {
		peer = strings.TrimSpace(peer)
		if peer == "" {
			errs = append(errs, fmt.Errorf("fips.peers[%d]: empty peer", i))
			continue
		}
		if !strings.HasPrefix(peer, "npub1") && len(peer) != 64 {
			errs = append(errs, fmt.Errorf("fips.peers[%d] %q: expected npub or 64-char hex pubkey", i, peer))
		}
	}

	return errs
}
