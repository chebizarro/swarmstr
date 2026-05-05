package acp

import "testing"

func TestPingPongBuildersAndDecoders(t *testing.T) {
	ping := NewPing("task-ping-1", "sender-a", PingPayload{Nonce: "nonce-1"})
	if ping.ACPType != "ping" {
		t.Fatalf("ping type = %q, want ping", ping.ACPType)
	}
	if ping.TaskID != "task-ping-1" {
		t.Fatalf("ping task_id = %q", ping.TaskID)
	}
	decodedPing, err := DecodePingPayload(ping.Payload)
	if err != nil {
		t.Fatalf("DecodePingPayload: %v", err)
	}
	if decodedPing.Nonce != "nonce-1" {
		t.Fatalf("ping nonce = %q, want nonce-1", decodedPing.Nonce)
	}

	pong := NewPong(ping.TaskID, "sender-b", PongPayload{Nonce: decodedPing.Nonce})
	if pong.ACPType != "pong" {
		t.Fatalf("pong type = %q, want pong", pong.ACPType)
	}
	decodedPong, err := DecodePongPayload(pong.Payload)
	if err != nil {
		t.Fatalf("DecodePongPayload: %v", err)
	}
	if decodedPong.Nonce != "nonce-1" {
		t.Fatalf("pong nonce = %q, want nonce-1", decodedPong.Nonce)
	}
}
