package methods

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeSessionsFilesParams(t *testing.T) {
	list, err := DecodeSessionsFilesListParams(json.RawMessage(`{"key":" session-a ","agent_id":"main","search":"go"}`))
	if err != nil || list.SessionKey != "session-a" || list.AgentID != "main" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	get, err := DecodeSessionsFilesGetParams(json.RawMessage(`{"sessionKey":"session-a","path":"a.txt"}`))
	if err != nil || get.Path != "a.txt" {
		t.Fatalf("get=%+v err=%v", get, err)
	}
	hash := strings.Repeat("a", 64)
	set, err := DecodeSessionsFilesSetParams(json.RawMessage(`{"session_key":"session-a","path":"a.txt","content":"new","expected_hash":"` + hash + `"}`))
	if err != nil || set.ExpectedHash != hash {
		t.Fatalf("set=%+v err=%v", set, err)
	}
	reveal, err := DecodeSessionsFilesRevealParams(json.RawMessage(`{"key":"session-a"}`))
	if err != nil || reveal.Key != "session-a" {
		t.Fatalf("reveal=%+v err=%v", reveal, err)
	}
	if _, err := DecodeSessionsFilesSetParams(json.RawMessage(`{"sessionKey":"s","path":"a","content":"x","expectedHash":"BAD"}`)); err == nil {
		t.Fatal("expected hash validation")
	}
	if _, err := DecodeSessionsFilesListParams(json.RawMessage(`{"sessionKey":"s","extra":true}`)); err == nil {
		t.Fatal("expected unknown-field error")
	}
}

func TestDecodeSessionsCatalogParams(t *testing.T) {
	list, err := DecodeSessionsCatalogListParams(json.RawMessage(`{"catalogId":"metiq-local","limitPerHost":20,"search":" x "}`))
	if err != nil || list.LimitPerHost != 20 || list.Search != "x" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	read, err := DecodeSessionsCatalogReadParams(json.RawMessage(`{"catalogId":"metiq-local","hostId":"gateway-local","threadId":"s","limit":10}`))
	if err != nil || read.Limit != 10 {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	if _, err := DecodeSessionsCatalogArchiveParams(json.RawMessage(`{"catalogId":"metiq-local","hostId":"gateway-local","threadId":"s","confirmNoOtherRunner":false}`)); err == nil {
		t.Fatal("expected confirmation error")
	}
	if _, err := DecodeSessionsCatalogContinueParams(json.RawMessage(`{"catalogId":"other","hostId":"gateway-local","threadId":"s"}`)); err == nil {
		t.Fatal("expected locator error")
	}
	if _, err := DecodeSessionsCatalogListParams(json.RawMessage(`{"cursors":{"gateway-local":"x"}}`)); err == nil {
		t.Fatal("expected catalog cursor requirement")
	}
}
