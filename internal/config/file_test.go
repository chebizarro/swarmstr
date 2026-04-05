package config

import "testing"

func TestParseConfigBytesStorageEncrypt(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{"storage":{"encrypt":false}}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if doc.Storage.Encrypt == nil || *doc.Storage.Encrypt {
		t.Fatalf("expected storage.encrypt=false, got %#v", doc.Storage)
	}
}

func TestParseConfigBytesACPTransport(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{"acp":{"transport":"nip04"}}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if doc.ACP.Transport != "nip04" {
		t.Fatalf("expected acp.transport=nip04, got %#v", doc.ACP)
	}
}

func TestParseConfigBytesDMReplyScheme(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{"dm":{"reply_scheme":"nip17"}}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if doc.DM.ReplyScheme != "nip17" {
		t.Fatalf("expected dm.reply_scheme=nip17, got %#v", doc.DM)
	}
}

func TestParseConfigBytesAgentHeartbeatModel(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{"agents":[{"id":"main","model":"gpt-4o","heartbeat":{"model":"gpt-4o-mini"}}]}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if len(doc.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(doc.Agents))
	}
	if doc.Agents[0].Heartbeat.Model != "gpt-4o-mini" {
		t.Fatalf("expected agents[0].heartbeat.model=gpt-4o-mini, got %#v", doc.Agents[0].Heartbeat)
	}
}
