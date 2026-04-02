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
