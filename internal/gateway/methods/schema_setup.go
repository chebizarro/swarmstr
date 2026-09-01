package methods

import (
	"encoding/json"
	"fmt"
	"strings"

	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

// SetupTokenRequest is shared by setup.detect, setup.verify, and setup.activate.
type SetupTokenRequest struct {
	SetupToken string `json:"setup_token"`
}

func DecodeSetupTokenParams(params json.RawMessage) (SetupTokenRequest, error) {
	return decodeMethodParams[SetupTokenRequest](params)
}

// SetupAuthStartRequest provisions one Metiq Nostr identity. Mode is one of
// generate, import_nsec, or nip46. NIP-46 accepts bunker:// URLs only.
type SetupAuthStartRequest struct {
	SetupToken     string `json:"setup_token"`
	Mode           string `json:"mode"`
	Nsec           string `json:"nsec,omitempty"`
	SignerURL      string `json:"signer_url,omitempty"`
	NIP46ClientKey string `json:"nip46_client_key,omitempty"`
}

func (r SetupAuthStartRequest) Normalize() (SetupAuthStartRequest, error) {
	r.SetupToken = strings.TrimSpace(r.SetupToken)
	r.Mode = strings.ToLower(strings.TrimSpace(r.Mode))
	r.Nsec = strings.TrimSpace(r.Nsec)
	r.SignerURL = strings.TrimSpace(r.SignerURL)
	r.NIP46ClientKey = strings.TrimSpace(r.NIP46ClientKey)
	switch r.Mode {
	case "generate":
		if r.Nsec != "" || r.SignerURL != "" || r.NIP46ClientKey != "" {
			return r, fmt.Errorf("setup.auth.start: generate does not accept imported signer material")
		}
	case "import_nsec":
		if !strings.HasPrefix(r.Nsec, "nsec1") {
			return r, fmt.Errorf("setup.auth.start: import_nsec requires nsec")
		}
		if r.SignerURL != "" || r.NIP46ClientKey != "" {
			return r, fmt.Errorf("setup.auth.start: import_nsec does not accept signer_url")
		}
	case "nip46":
		if !strings.HasPrefix(strings.ToLower(r.SignerURL), "bunker://") {
			return r, fmt.Errorf("setup.auth.start: nip46 requires bunker:// signer_url")
		}
		if r.Nsec != "" {
			return r, fmt.Errorf("setup.auth.start: nip46 does not accept nsec")
		}
	default:
		return r, fmt.Errorf("setup.auth.start: mode must be generate, import_nsec, or nip46")
	}
	return r, nil
}

func DecodeSetupAuthStartParams(params json.RawMessage) (SetupAuthStartRequest, error) {
	return decodeMethodParams[SetupAuthStartRequest](params)
}

// SetupPrepareStartRequest stages all configuration required by verification.
// The shape is intentionally Metiq-native rather than OpenClaw-compatible.
type SetupPrepareStartRequest struct {
	SetupToken       string                        `json:"setup_token"`
	ReadRelays       []string                      `json:"read_relays"`
	WriteRelays      []string                      `json:"write_relays"`
	WorkspaceDir     string                        `json:"workspace_dir"`
	Providers        state.ProvidersConfig         `json:"providers"`
	CapabilityAdvert nostruntime.FIPSOverlayAdvert `json:"capability_advert"`
}

func (r SetupPrepareStartRequest) Normalize() (SetupPrepareStartRequest, error) {
	r.SetupToken = strings.TrimSpace(r.SetupToken)
	r.WorkspaceDir = strings.TrimSpace(r.WorkspaceDir)
	r.ReadRelays = normalizeSetupStrings(r.ReadRelays)
	r.WriteRelays = normalizeSetupStrings(r.WriteRelays)
	if len(r.ReadRelays) == 0 || len(r.WriteRelays) == 0 {
		return r, fmt.Errorf("setup.prepare.start: read_relays and write_relays are required")
	}
	if r.WorkspaceDir == "" {
		return r, fmt.Errorf("setup.prepare.start: workspace_dir is required")
	}
	if len(r.Providers) == 0 {
		return r, fmt.Errorf("setup.prepare.start: at least one provider is required")
	}
	providers := make(state.ProvidersConfig, len(r.Providers))
	for rawID, entry := range r.Providers {
		id := strings.ToLower(strings.TrimSpace(rawID))
		if id == "" {
			return r, fmt.Errorf("setup.prepare.start: provider id is required")
		}
		if strings.TrimSpace(entry.APIKey) == "" && len(entry.APIKeys) == 0 {
			return r, fmt.Errorf("setup.prepare.start: provider %q credentials are required", id)
		}
		entry.Enabled = true
		providers[id] = entry
	}
	r.Providers = providers
	advert, err := nostruntime.ValidateFIPSOverlayAdvert(r.CapabilityAdvert)
	if err != nil {
		return r, fmt.Errorf("setup.prepare.start: capability_advert: %w", err)
	}
	r.CapabilityAdvert = advert
	return r, nil
}

func DecodeSetupPrepareStartParams(params json.RawMessage) (SetupPrepareStartRequest, error) {
	return decodeMethodParams[SetupPrepareStartRequest](params)
}

func normalizeSetupStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
