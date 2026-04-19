// Package nodeconnect provides node connection reconciliation logic.
//
// When a node (mobile app, desktop client, CLI, etc.) connects to the gateway,
// its declared command set must be reconciled against the server's allowlist and
// any previously paired command set. This mirrors openclaw's
// node-connect-reconcile.ts.
package nodeconnect

import (
	"sort"
	"strings"
)

// ConnectClient describes the connecting client's identity.
type ConnectClient struct {
	ID              string   `json:"id"`
	DisplayName     string   `json:"display_name,omitempty"`
	Platform        string   `json:"platform,omitempty"`
	Version         string   `json:"version,omitempty"`
	DeviceFamily    string   `json:"device_family,omitempty"`
	ModelIdentifier string   `json:"model_identifier,omitempty"`
	Caps            []string `json:"caps,omitempty"`
}

// ConnectDevice is optional device-level identity that overrides client ID.
type ConnectDevice struct {
	ID string `json:"id"`
}

// ConnectParams are the parameters sent by a node during the connect handshake.
type ConnectParams struct {
	Client   ConnectClient  `json:"client"`
	Device   *ConnectDevice `json:"device,omitempty"`
	Commands []string       `json:"commands,omitempty"`
}

// PairedNode represents an already-paired node stored on the server.
type PairedNode struct {
	NodeID   string   `json:"node_id"`
	Commands []string `json:"commands,omitempty"`
}

// PairingRequest is the input for requesting a new node pairing.
type PairingRequest struct {
	NodeID          string   `json:"node_id"`
	DisplayName     string   `json:"display_name,omitempty"`
	Platform        string   `json:"platform,omitempty"`
	Version         string   `json:"version,omitempty"`
	DeviceFamily    string   `json:"device_family,omitempty"`
	ModelIdentifier string   `json:"model_identifier,omitempty"`
	Caps            []string `json:"caps,omitempty"`
	Commands        []string `json:"commands,omitempty"`
	RemoteIP        string   `json:"remote_ip,omitempty"`
}

// PendingPairingResult is returned when a pairing request was created or
// already existed.
type PendingPairingResult struct {
	Status  string `json:"status"` // "pending"
	Created bool   `json:"created"`
}

// ReconcileResult describes the outcome of reconciling a node connection.
type ReconcileResult struct {
	// NodeID is the effective node identifier used.
	NodeID string `json:"node_id"`

	// EffectiveCommands are the commands the node is allowed to use.
	EffectiveCommands []string `json:"effective_commands"`

	// PendingPairing is set when a new pairing request was created (either
	// because the node was unknown, or because it declared new commands
	// that require approval).
	PendingPairing *PendingPairingResult `json:"pending_pairing,omitempty"`
}

// ReconcileInput holds everything needed to reconcile a node connection.
type ReconcileInput struct {
	// Params are the connect parameters sent by the node.
	Params ConnectParams

	// PairedNode is the existing pairing record, or nil if the node is unknown.
	PairedNode *PairedNode

	// Allowlist is the set of allowed commands for this platform/device family.
	// If nil, all declared commands are allowed.
	Allowlist map[string]struct{}

	// ReportedClientIP is the client's IP address as seen by the server.
	ReportedClientIP string

	// RequestPairing is called when a pairing request needs to be created.
	RequestPairing func(PairingRequest) (PendingPairingResult, error)
}

// Reconcile reconciles a node's declared commands against its pairing status
// and the server's allowlist. It handles three cases:
//
//  1. Unknown node (no pairing record) → requests pairing, returns declared
//     commands as effective.
//  2. Paired node with no command upgrade → returns approved commands.
//  3. Paired node with command upgrade → requests re-pairing for the new
//     commands, returns the previously approved commands as effective.
func Reconcile(input ReconcileInput) (ReconcileResult, error) {
	// Resolve the effective node ID (device ID takes precedence).
	nodeID := input.Params.Client.ID
	if input.Params.Device != nil && input.Params.Device.ID != "" {
		nodeID = input.Params.Device.ID
	}

	// Normalize declared commands against the allowlist.
	declared := NormalizeDeclaredCommands(input.Params.Commands, input.Allowlist)

	// Case 1: Unknown node → request pairing.
	if input.PairedNode == nil {
		if input.RequestPairing == nil {
			return ReconcileResult{
				NodeID:            nodeID,
				EffectiveCommands: declared,
			}, nil
		}
		pending, err := input.RequestPairing(buildPairingRequest(nodeID, input))
		if err != nil {
			return ReconcileResult{}, err
		}
		return ReconcileResult{
			NodeID:            nodeID,
			EffectiveCommands: declared,
			PendingPairing:    &pending,
		}, nil
	}

	// Case 2/3: Paired node — resolve approved commands.
	approved := NormalizeDeclaredCommands(input.PairedNode.Commands, input.Allowlist)

	// Check for command upgrade: any declared command not in approved set?
	hasUpgrade := false
	approvedSet := toSet(approved)
	for _, cmd := range declared {
		if _, ok := approvedSet[cmd]; !ok {
			hasUpgrade = true
			break
		}
	}

	// Case 3: Command upgrade → request re-pairing, but use old approved set.
	if hasUpgrade && input.RequestPairing != nil {
		pending, err := input.RequestPairing(buildPairingRequest(nodeID, input))
		if err != nil {
			return ReconcileResult{}, err
		}
		return ReconcileResult{
			NodeID:            nodeID,
			EffectiveCommands: approved,
			PendingPairing:    &pending,
		}, nil
	}

	// Case 2: No upgrade → use declared commands.
	return ReconcileResult{
		NodeID:            nodeID,
		EffectiveCommands: declared,
	}, nil
}

// NormalizeDeclaredCommands filters and deduplicates a command list against an
// allowlist. If the allowlist is nil, all non-empty commands are returned.
func NormalizeDeclaredCommands(commands []string, allowlist map[string]struct{}) []string {
	seen := make(map[string]struct{}, len(commands))
	out := make([]string, 0, len(commands))
	for _, cmd := range commands {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			continue
		}
		if _, dup := seen[cmd]; dup {
			continue
		}
		if allowlist != nil {
			if _, allowed := allowlist[cmd]; !allowed {
				continue
			}
		}
		seen[cmd] = struct{}{}
		out = append(out, cmd)
	}
	sort.Strings(out)
	return out
}

// ResolveCommandAllowlist builds a command allowlist from a config map.
// It looks for "node_command_allowlist" as a []string key and returns a set.
// Returns nil if no allowlist is configured (all commands allowed).
func ResolveCommandAllowlist(cfg map[string]any) map[string]struct{} {
	raw, ok := cfg["node_command_allowlist"]
	if !ok {
		return nil
	}
	list, ok := raw.([]string)
	if !ok {
		// Try []any (from JSON decode).
		if arr, ok := raw.([]any); ok {
			list = make([]string, 0, len(arr))
			for _, v := range arr {
				if s, ok := v.(string); ok {
					list = append(list, s)
				}
			}
		}
	}
	if len(list) == 0 {
		return nil
	}
	result := make(map[string]struct{}, len(list))
	for _, cmd := range list {
		cmd = strings.TrimSpace(cmd)
		if cmd != "" {
			result[cmd] = struct{}{}
		}
	}
	return result
}

func buildPairingRequest(nodeID string, input ReconcileInput) PairingRequest {
	return PairingRequest{
		NodeID:          nodeID,
		DisplayName:     input.Params.Client.DisplayName,
		Platform:        input.Params.Client.Platform,
		Version:         input.Params.Client.Version,
		DeviceFamily:    input.Params.Client.DeviceFamily,
		ModelIdentifier: input.Params.Client.ModelIdentifier,
		Caps:            input.Params.Client.Caps,
		Commands:        NormalizeDeclaredCommands(input.Params.Commands, input.Allowlist),
		RemoteIP:        input.ReportedClientIP,
	}
}

func toSet(items []string) map[string]struct{} {
	s := make(map[string]struct{}, len(items))
	for _, item := range items {
		s[item] = struct{}{}
	}
	return s
}
