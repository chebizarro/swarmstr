package nodeconnect

import (
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// NormalizeDeclaredCommands
// ---------------------------------------------------------------------------

func TestNormalizeDeclaredCommands_NoAllowlist(t *testing.T) {
	cmds := NormalizeDeclaredCommands([]string{"ping", "exec", "screenshot"}, nil)
	if len(cmds) != 3 {
		t.Fatalf("count = %d, want 3", len(cmds))
	}
	// Should be sorted.
	expected := []string{"exec", "ping", "screenshot"}
	for i, want := range expected {
		if cmds[i] != want {
			t.Errorf("cmds[%d] = %q, want %q", i, cmds[i], want)
		}
	}
}

func TestNormalizeDeclaredCommands_WithAllowlist(t *testing.T) {
	allow := map[string]struct{}{"ping": {}, "exec": {}}
	cmds := NormalizeDeclaredCommands([]string{"ping", "exec", "screenshot", "reboot"}, allow)
	if len(cmds) != 2 {
		t.Fatalf("count = %d, want 2", len(cmds))
	}
	if cmds[0] != "exec" || cmds[1] != "ping" {
		t.Errorf("cmds = %v", cmds)
	}
}

func TestNormalizeDeclaredCommands_Deduplicates(t *testing.T) {
	cmds := NormalizeDeclaredCommands([]string{"ping", "ping", "exec", "exec"}, nil)
	if len(cmds) != 2 {
		t.Fatalf("count = %d, want 2", len(cmds))
	}
}

func TestNormalizeDeclaredCommands_TrimsWhitespace(t *testing.T) {
	cmds := NormalizeDeclaredCommands([]string{"  ping  ", " ", "", "exec"}, nil)
	if len(cmds) != 2 {
		t.Fatalf("count = %d, want 2", len(cmds))
	}
}

func TestNormalizeDeclaredCommands_EmptyInput(t *testing.T) {
	cmds := NormalizeDeclaredCommands(nil, nil)
	if len(cmds) != 0 {
		t.Fatalf("count = %d, want 0", len(cmds))
	}
}

// ---------------------------------------------------------------------------
// ResolveCommandAllowlist
// ---------------------------------------------------------------------------

func TestResolveCommandAllowlist_StringSlice(t *testing.T) {
	cfg := map[string]any{
		"node_command_allowlist": []string{"ping", "exec"},
	}
	allow := ResolveCommandAllowlist(cfg)
	if len(allow) != 2 {
		t.Fatalf("count = %d, want 2", len(allow))
	}
	if _, ok := allow["ping"]; !ok {
		t.Error("expected 'ping' in allowlist")
	}
}

func TestResolveCommandAllowlist_AnySlice(t *testing.T) {
	cfg := map[string]any{
		"node_command_allowlist": []any{"ping", "screenshot"},
	}
	allow := ResolveCommandAllowlist(cfg)
	if len(allow) != 2 {
		t.Fatalf("count = %d, want 2", len(allow))
	}
}

func TestResolveCommandAllowlist_Missing(t *testing.T) {
	cfg := map[string]any{}
	allow := ResolveCommandAllowlist(cfg)
	if allow != nil {
		t.Error("expected nil for missing key")
	}
}

func TestResolveCommandAllowlist_EmptyList(t *testing.T) {
	cfg := map[string]any{
		"node_command_allowlist": []string{},
	}
	allow := ResolveCommandAllowlist(cfg)
	if allow != nil {
		t.Error("expected nil for empty list")
	}
}

// ---------------------------------------------------------------------------
// Reconcile — unknown node
// ---------------------------------------------------------------------------

func TestReconcile_UnknownNodeRequestsPairing(t *testing.T) {
	var gotReq PairingRequest
	result, err := Reconcile(ReconcileInput{
		Params: ConnectParams{
			Client:   ConnectClient{ID: "node-1", DisplayName: "My Phone", Platform: "ios"},
			Commands: []string{"ping", "screenshot"},
		},
		PairedNode:       nil,
		ReportedClientIP: "1.2.3.4",
		RequestPairing: func(req PairingRequest) (PendingPairingResult, error) {
			gotReq = req
			return PendingPairingResult{Status: "pending", Created: true}, nil
		},
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.NodeID != "node-1" {
		t.Errorf("nodeID = %q", result.NodeID)
	}
	if len(result.EffectiveCommands) != 2 {
		t.Errorf("effective = %v", result.EffectiveCommands)
	}
	if result.PendingPairing == nil {
		t.Fatal("expected pending pairing")
	}
	if !result.PendingPairing.Created {
		t.Error("expected created=true")
	}
	if gotReq.NodeID != "node-1" {
		t.Errorf("request nodeID = %q", gotReq.NodeID)
	}
	if gotReq.RemoteIP != "1.2.3.4" {
		t.Errorf("request remoteIP = %q", gotReq.RemoteIP)
	}
}

func TestReconcile_UnknownNodeNoCallback(t *testing.T) {
	result, err := Reconcile(ReconcileInput{
		Params: ConnectParams{
			Client:   ConnectClient{ID: "node-orphan"},
			Commands: []string{"ping"},
		},
		PairedNode:     nil,
		RequestPairing: nil,
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.PendingPairing != nil {
		t.Error("expected no pending pairing when no callback")
	}
	if len(result.EffectiveCommands) != 1 {
		t.Errorf("effective = %v", result.EffectiveCommands)
	}
}

// ---------------------------------------------------------------------------
// Reconcile — paired node, no upgrade
// ---------------------------------------------------------------------------

func TestReconcile_PairedNoUpgrade(t *testing.T) {
	result, err := Reconcile(ReconcileInput{
		Params: ConnectParams{
			Client:   ConnectClient{ID: "node-2"},
			Commands: []string{"ping", "exec"},
		},
		PairedNode: &PairedNode{
			NodeID:   "node-2",
			Commands: []string{"ping", "exec", "screenshot"},
		},
		RequestPairing: func(PairingRequest) (PendingPairingResult, error) {
			t.Error("should not request pairing when no upgrade")
			return PendingPairingResult{}, nil
		},
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.PendingPairing != nil {
		t.Error("expected no pending pairing")
	}
	// Effective commands should be the declared set (filtered by allowlist=nil).
	if len(result.EffectiveCommands) != 2 {
		t.Errorf("effective = %v", result.EffectiveCommands)
	}
}

// ---------------------------------------------------------------------------
// Reconcile — paired node with command upgrade
// ---------------------------------------------------------------------------

func TestReconcile_PairedWithUpgrade(t *testing.T) {
	var pairingRequested bool
	result, err := Reconcile(ReconcileInput{
		Params: ConnectParams{
			Client:   ConnectClient{ID: "node-3"},
			Commands: []string{"ping", "exec", "reboot"}, // reboot is new
		},
		PairedNode: &PairedNode{
			NodeID:   "node-3",
			Commands: []string{"ping", "exec"},
		},
		RequestPairing: func(req PairingRequest) (PendingPairingResult, error) {
			pairingRequested = true
			return PendingPairingResult{Status: "pending", Created: true}, nil
		},
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !pairingRequested {
		t.Error("expected pairing request for command upgrade")
	}
	if result.PendingPairing == nil {
		t.Fatal("expected pending pairing")
	}
	// Effective commands should be the OLD approved set, not the new declared set.
	approvedSet := toSet(result.EffectiveCommands)
	if _, ok := approvedSet["reboot"]; ok {
		t.Error("reboot should NOT be in effective commands (not yet approved)")
	}
	if _, ok := approvedSet["ping"]; !ok {
		t.Error("ping should be in effective commands")
	}
}

// ---------------------------------------------------------------------------
// Reconcile — device ID override
// ---------------------------------------------------------------------------

func TestReconcile_DeviceIDOverridesClientID(t *testing.T) {
	result, err := Reconcile(ReconcileInput{
		Params: ConnectParams{
			Client:   ConnectClient{ID: "client-id"},
			Device:   &ConnectDevice{ID: "device-id"},
			Commands: []string{"ping"},
		},
		PairedNode: nil,
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.NodeID != "device-id" {
		t.Errorf("nodeID = %q, want device-id", result.NodeID)
	}
}

func TestReconcile_EmptyDeviceIDFallsBackToClient(t *testing.T) {
	result, err := Reconcile(ReconcileInput{
		Params: ConnectParams{
			Client:   ConnectClient{ID: "client-id"},
			Device:   &ConnectDevice{ID: ""},
			Commands: []string{"ping"},
		},
		PairedNode: nil,
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.NodeID != "client-id" {
		t.Errorf("nodeID = %q, want client-id", result.NodeID)
	}
}

// ---------------------------------------------------------------------------
// Reconcile — allowlist filtering
// ---------------------------------------------------------------------------

func TestReconcile_AllowlistFiltering(t *testing.T) {
	allow := map[string]struct{}{"ping": {}, "exec": {}}
	result, err := Reconcile(ReconcileInput{
		Params: ConnectParams{
			Client:   ConnectClient{ID: "node-4"},
			Commands: []string{"ping", "exec", "reboot", "screenshot"},
		},
		PairedNode: nil,
		Allowlist:  allow,
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(result.EffectiveCommands) != 2 {
		t.Errorf("effective = %v, want [exec ping]", result.EffectiveCommands)
	}
}

// ---------------------------------------------------------------------------
// Reconcile — pairing callback error
// ---------------------------------------------------------------------------

func TestReconcile_PairingCallbackError(t *testing.T) {
	_, err := Reconcile(ReconcileInput{
		Params: ConnectParams{
			Client:   ConnectClient{ID: "node-err"},
			Commands: []string{"ping"},
		},
		PairedNode: nil,
		RequestPairing: func(PairingRequest) (PendingPairingResult, error) {
			return PendingPairingResult{}, errors.New("pairing service unavailable")
		},
	})
	if err == nil {
		t.Fatal("expected error from pairing callback")
	}
}

// ---------------------------------------------------------------------------
// Reconcile — paired with empty commands
// ---------------------------------------------------------------------------

func TestReconcile_PairedEmptyCommands(t *testing.T) {
	result, err := Reconcile(ReconcileInput{
		Params: ConnectParams{
			Client:   ConnectClient{ID: "node-empty"},
			Commands: nil,
		},
		PairedNode: &PairedNode{
			NodeID:   "node-empty",
			Commands: nil,
		},
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(result.EffectiveCommands) != 0 {
		t.Errorf("effective = %v, want empty", result.EffectiveCommands)
	}
	if result.PendingPairing != nil {
		t.Error("no pairing expected for empty commands")
	}
}

// ---------------------------------------------------------------------------
// Reconcile — pairing request carries connect metadata
// ---------------------------------------------------------------------------

func TestReconcile_PairingRequestMetadata(t *testing.T) {
	var gotReq PairingRequest
	Reconcile(ReconcileInput{
		Params: ConnectParams{
			Client: ConnectClient{
				ID:              "node-meta",
				DisplayName:     "My Laptop",
				Platform:        "macos",
				Version:         "2.1.0",
				DeviceFamily:    "MacBookPro",
				ModelIdentifier: "MacBookPro18,1",
				Caps:            []string{"canvas", "voice"},
			},
			Commands: []string{"exec"},
		},
		PairedNode:       nil,
		ReportedClientIP: "10.0.0.1",
		RequestPairing: func(req PairingRequest) (PendingPairingResult, error) {
			gotReq = req
			return PendingPairingResult{Status: "pending"}, nil
		},
	})

	if gotReq.DisplayName != "My Laptop" {
		t.Errorf("displayName = %q", gotReq.DisplayName)
	}
	if gotReq.Platform != "macos" {
		t.Errorf("platform = %q", gotReq.Platform)
	}
	if gotReq.Version != "2.1.0" {
		t.Errorf("version = %q", gotReq.Version)
	}
	if gotReq.DeviceFamily != "MacBookPro" {
		t.Errorf("deviceFamily = %q", gotReq.DeviceFamily)
	}
	if gotReq.ModelIdentifier != "MacBookPro18,1" {
		t.Errorf("modelIdentifier = %q", gotReq.ModelIdentifier)
	}
	if len(gotReq.Caps) != 2 {
		t.Errorf("caps = %v", gotReq.Caps)
	}
	if len(gotReq.Commands) != 1 || gotReq.Commands[0] != "exec" {
		t.Errorf("commands = %v", gotReq.Commands)
	}
	if gotReq.RemoteIP != "10.0.0.1" {
		t.Errorf("remoteIP = %q", gotReq.RemoteIP)
	}
}
