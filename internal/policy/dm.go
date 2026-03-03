package policy

import (
	"fmt"
	"net/url"
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
