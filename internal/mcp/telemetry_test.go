package mcp

import "testing"

func TestBuildTelemetrySnapshotIncludesAuthAndApprovalTelemetry(t *testing.T) {
	resolved := Config{
		Enabled: true,
		Servers: map[string]ResolvedServerConfig{
			"remote": {
				Name:         "remote",
				ServerConfig: ServerConfig{Enabled: true, Type: "http", URL: "https://mcp.example.com/http"},
				Source:       ConfigSourceExtraMCP,
				Precedence:   extraMCPPrecedence,
				Signature:    "remote-sig",
			},
		},
		FilteredServers: map[string]FilteredServer{
			"pending-remote": {
				ResolvedServerConfig: ResolvedServerConfig{
					Name:         "pending-remote",
					ServerConfig: ServerConfig{Enabled: true, Type: "http", URL: "https://pending.example.com/http"},
					Source:       ConfigSourceExtraMCP,
					Precedence:   extraMCPPrecedence,
					Signature:    "pending-sig",
				},
				PolicyStatus: PolicyStatusApprovalRequired,
				PolicyReason: PolicyReasonRemoteApproval,
			},
		},
		Suppressed: []SuppressedServer{{
			Name:       "duplicate",
			Source:     ConfigSourceExtraMCP,
			Precedence: extraMCPPrecedence,
			Reason:     SuppressionReasonDuplicateSignature,
		}},
	}
	runtime := ManagerSnapshot{
		Enabled: true,
		Servers: []ServerStateSnapshot{{
			Name:              "remote",
			State:             ConnectionStateNeedsAuth,
			Enabled:           true,
			Source:            ConfigSourceExtraMCP,
			Precedence:        extraMCPPrecedence,
			Signature:         "remote-sig",
			Transport:         "http",
			URL:               "https://mcp.example.com/http",
			LastError:         "401 unauthorized",
			ReconnectAttempts: 2,
			LastAttemptAtMS:   1001,
			LastFailedAtMS:    1002,
			UpdatedAtMS:       1003,
		}},
	}

	snapshot := BuildTelemetrySnapshot(resolved, runtime)
	if snapshot.Empty() {
		t.Fatal("expected non-empty telemetry snapshot")
	}
	if snapshot.Summary.TotalServers != 2 {
		t.Fatalf("expected 2 servers in summary, got %#v", snapshot.Summary)
	}
	if snapshot.Summary.NeedsAuthServers != 1 || snapshot.Summary.ApprovalRequiredServers != 1 {
		t.Fatalf("expected needs-auth and approval-required counts, got %#v", snapshot.Summary)
	}
	if snapshot.Summary.SuppressedServers != 1 {
		t.Fatalf("expected suppressed count, got %#v", snapshot.Summary)
	}
	if snapshot.Summary.Healthy {
		t.Fatalf("expected unhealthy summary when auth/approval issues exist, got %#v", snapshot.Summary)
	}
	if len(snapshot.Servers) != 2 {
		t.Fatalf("expected 2 telemetry servers, got %#v", snapshot.Servers)
	}

	remote := snapshot.Servers[1]
	if remote.Name != "remote" {
		remote = snapshot.Servers[0]
	}
	if remote.Name != "remote" || remote.State != string(ConnectionStateNeedsAuth) || !remote.RuntimePresent {
		t.Fatalf("unexpected remote telemetry row: %#v", remote)
	}
	if remote.ReconnectAttempts != 2 || remote.LastAttemptAtMS != 1001 || remote.LastFailedAtMS != 1002 {
		t.Fatalf("expected reconnect timing telemetry, got %#v", remote)
	}

	pending := snapshot.Servers[0]
	if pending.Name == "remote" {
		pending = snapshot.Servers[1]
	}
	if pending.Name != "pending-remote" || pending.State != string(PolicyStatusApprovalRequired) {
		t.Fatalf("unexpected filtered telemetry row: %#v", pending)
	}
	if pending.PolicyStatus != PolicyStatusApprovalRequired || pending.PolicyReason != PolicyReasonRemoteApproval || pending.RuntimePresent {
		t.Fatalf("unexpected filtered policy telemetry: %#v", pending)
	}
}

func TestBuildTelemetrySnapshotPendingServerIsUnhealthy(t *testing.T) {
	snapshot := BuildTelemetrySnapshot(Config{
		Enabled: true,
		Servers: map[string]ResolvedServerConfig{
			"demo": {
				Name:         "demo",
				ServerConfig: ServerConfig{Enabled: true, Command: "demo-mcp"},
				Source:       ConfigSourceExtraMCP,
				Precedence:   extraMCPPrecedence,
				Signature:    "demo-sig",
			},
		},
	}, ManagerSnapshot{})

	if snapshot.Summary.PendingServers != 1 {
		t.Fatalf("expected pending server count, got %#v", snapshot.Summary)
	}
	if snapshot.Summary.Healthy {
		t.Fatalf("expected pending-only telemetry to remain unhealthy, got %#v", snapshot.Summary)
	}
	if len(snapshot.Servers) != 1 || snapshot.Servers[0].State != string(ConnectionStatePending) || snapshot.Servers[0].Healthy {
		t.Fatalf("unexpected pending telemetry row: %#v", snapshot.Servers)
	}
}
