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

func TestParseConfigBytesAgentContextWindow(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{"agents":[{"id":"local","model":"phi-3-mini.gguf","context_window":4096,"max_context_tokens":3000}]}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if len(doc.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(doc.Agents))
	}
	if doc.Agents[0].ContextWindow != 4096 {
		t.Fatalf("expected context_window=4096, got %d", doc.Agents[0].ContextWindow)
	}
	if doc.Agents[0].MaxContextTokens != 3000 {
		t.Fatalf("expected max_context_tokens=3000, got %d", doc.Agents[0].MaxContextTokens)
	}
}

func TestParseConfigBytesAgentContextWindowCamelCase(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{"agents":[{"id":"local","model":"gemma.gguf","contextWindow":8192}]}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if len(doc.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(doc.Agents))
	}
	if doc.Agents[0].ContextWindow != 8192 {
		t.Fatalf("expected contextWindow=8192, got %d", doc.Agents[0].ContextWindow)
	}
}

func TestParseConfigBytesOpenClawDefaultHeartbeatEvery(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{"agents":{"defaults":{"heartbeat":{"every":"2h"}}}}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if !doc.Heartbeat.Enabled {
		t.Fatalf("expected heartbeat to be enabled, got %#v", doc.Heartbeat)
	}
	if want := 2 * 60 * 60 * 1000; doc.Heartbeat.IntervalMS != want {
		t.Fatalf("expected heartbeat interval %d, got %#v", want, doc.Heartbeat)
	}
}

func TestParseConfigBytesTopLevelHeartbeatOverridesDefaults(t *testing.T) {
	doc, err := ParseConfigBytes([]byte(`{"agents":{"defaults":{"heartbeat":{"every":"2h"}}},"heartbeat":{"enabled":true,"interval_ms":15000}}`), ".json")
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if !doc.Heartbeat.Enabled || doc.Heartbeat.IntervalMS != 15000 {
		t.Fatalf("expected explicit top-level heartbeat override, got %#v", doc.Heartbeat)
	}
}
