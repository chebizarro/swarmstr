package toolbuiltin

import (
	"context"
	"encoding/json"
	"testing"
)

func TestFIPSStatusTool_AllProviders(t *testing.T) {
	tool := FIPSStatusTool(FIPSStatusOpts{
		Transport: func() *FIPSTransportHealth {
			return &FIPSTransportHealth{
				Listening:         true,
				ListenAddr:        "[fd12::abcd]:1337",
				ActiveConnections: 3,
				IdentityCacheSize: 5,
			}
		},
		Control: func() *FIPSControlHealth {
			return &FIPSControlHealth{
				Listening:  true,
				ListenAddr: "[fd12::abcd]:1338",
			}
		},
		Selector: func() *FIPSSelectorHealth {
			return &FIPSSelectorHealth{
				Preference:            "fips-first",
				ReachabilityCacheSize: 2,
			}
		},
		Peers: func() []FIPSPeerHealth {
			return []FIPSPeerHealth{
				{Name: "Agent-A", Pubkey: "aabb", FIPSAddr: "fd12::1", Reachable: "yes"},
				{Name: "Agent-B", Pubkey: "ccdd", FIPSAddr: "fd12::2", Reachable: "unknown"},
			}
		},
	})

	out, err := tool(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result FIPSStatusResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !result.Enabled {
		t.Error("expected enabled=true")
	}
	if result.Transport == nil {
		t.Fatal("expected transport to be set")
	}
	if result.Transport.ActiveConnections != 3 {
		t.Errorf("active_connections = %d, want 3", result.Transport.ActiveConnections)
	}
	if result.Transport.ListenAddr != "[fd12::abcd]:1337" {
		t.Errorf("listen_addr = %q", result.Transport.ListenAddr)
	}
	if result.Control == nil || !result.Control.Listening {
		t.Error("expected control listening")
	}
	if result.Selector == nil || result.Selector.Preference != "fips-first" {
		t.Error("expected selector preference fips-first")
	}
	if result.PeerCount != 2 {
		t.Errorf("peer_count = %d, want 2", result.PeerCount)
	}
}

func TestFIPSStatusTool_NoProviders(t *testing.T) {
	tool := FIPSStatusTool(FIPSStatusOpts{})

	out, err := tool(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result FIPSStatusResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result.Enabled {
		t.Error("expected enabled=false when no providers")
	}
	if result.Transport != nil {
		t.Error("expected nil transport")
	}
	if result.PeerCount != 0 {
		t.Errorf("peer_count = %d, want 0", result.PeerCount)
	}
}

func TestFIPSStatusTool_TransportOnly(t *testing.T) {
	tool := FIPSStatusTool(FIPSStatusOpts{
		Transport: func() *FIPSTransportHealth {
			return &FIPSTransportHealth{
				Listening:         true,
				ActiveConnections: 1,
			}
		},
	})

	out, err := tool(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result FIPSStatusResult
	json.Unmarshal([]byte(out), &result)
	if !result.Enabled {
		t.Error("expected enabled=true")
	}
	if result.Transport == nil || result.Transport.ActiveConnections != 1 {
		t.Error("transport should report 1 connection")
	}
	if result.Control != nil {
		t.Error("control should be nil")
	}
	if result.Selector != nil {
		t.Error("selector should be nil")
	}
}

func TestBuildFIPSHealthInfo_Nil(t *testing.T) {
	info := BuildFIPSHealthInfo(FIPSStatusOpts{})
	if info != nil {
		t.Error("expected nil when no providers set")
	}
}

func TestBuildFIPSHealthInfo_Full(t *testing.T) {
	info := BuildFIPSHealthInfo(FIPSStatusOpts{
		Transport: func() *FIPSTransportHealth {
			return &FIPSTransportHealth{
				Listening:         true,
				ListenAddr:        "[fd12::abcd]:1337",
				ActiveConnections: 4,
			}
		},
		Control: func() *FIPSControlHealth {
			return &FIPSControlHealth{Listening: true, ListenAddr: "[fd12::abcd]:1338"}
		},
		Selector: func() *FIPSSelectorHealth {
			return &FIPSSelectorHealth{Preference: "relay-first"}
		},
		Peers: func() []FIPSPeerHealth {
			return []FIPSPeerHealth{{Name: "A", Pubkey: "aa"}, {Name: "B", Pubkey: "bb"}}
		},
	})

	if info == nil {
		t.Fatal("expected non-nil info")
	}
	if !info.Enabled {
		t.Error("expected enabled=true")
	}
	if !info.TransportListening {
		t.Error("expected transport listening")
	}
	if info.ActiveConnections != 4 {
		t.Errorf("active_connections = %d, want 4", info.ActiveConnections)
	}
	if !info.ControlListening {
		t.Error("expected control listening")
	}
	if info.Preference != "relay-first" {
		t.Errorf("preference = %q, want relay-first", info.Preference)
	}
	if info.FIPSPeerCount != 2 {
		t.Errorf("fips_peer_count = %d, want 2", info.FIPSPeerCount)
	}
}

func TestFleetAgentsToolWithOpts_FIPSReachability(t *testing.T) {
	agents := []FleetEntry{
		{Pubkey: "aabb", Name: "Agent-A", FIPSEnabled: true},
		{Pubkey: "ccdd", Name: "Agent-B", FIPSEnabled: true},
		{Pubkey: "eeff", Name: "Agent-C", FIPSEnabled: false},
	}
	reachable := map[string]bool{"aabb": true, "ccdd": false}

	tool := FleetAgentsToolWithOpts(FleetAgentsToolOpts{
		GetAgents: func() []FleetEntry { return agents },
		FIPSReachable: func(pk string) bool {
			return reachable[pk]
		},
	})

	out, err := tool(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp struct {
		Agents []FleetEntry `json:"agents"`
	}
	json.Unmarshal([]byte(out), &resp)

	if len(resp.Agents) != 3 {
		t.Fatalf("agent count = %d, want 3", len(resp.Agents))
	}

	// Agent-A is FIPS enabled and reachable.
	if resp.Agents[0].FIPSReachable != "yes" {
		t.Errorf("Agent-A fips_reachable = %q, want yes", resp.Agents[0].FIPSReachable)
	}
	// Agent-B is FIPS enabled but not reachable.
	if resp.Agents[1].FIPSReachable != "no" {
		t.Errorf("Agent-B fips_reachable = %q, want no", resp.Agents[1].FIPSReachable)
	}
	// Agent-C is not FIPS enabled — should have no reachability annotation.
	if resp.Agents[2].FIPSReachable != "" {
		t.Errorf("Agent-C fips_reachable = %q, want empty", resp.Agents[2].FIPSReachable)
	}
}

func TestFleetAgentsTool_BackwardCompatible(t *testing.T) {
	// Original FleetAgentsTool (without opts) should still work.
	agents := []FleetEntry{
		{Pubkey: "aabb", Name: "Test"},
	}
	tool := FleetAgentsTool(func() []FleetEntry { return agents })
	out, err := tool(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp struct {
		Count int `json:"count"`
	}
	json.Unmarshal([]byte(out), &resp)
	if resp.Count != 1 {
		t.Errorf("count = %d, want 1", resp.Count)
	}
}
