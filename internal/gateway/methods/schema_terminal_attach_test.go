package methods

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTerminalAttachAndTextParams(t *testing.T) {
	if _, err := DecodeTerminalAttachParams(json.RawMessage(`{"sessionId":""}`)); err != nil {
		t.Fatalf("decode: %v", err)
	}
	req, err := DecodeTerminalAttachParams(json.RawMessage(`{"sessionId":" term-1 "}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	req, err = req.Normalize()
	if err != nil || req.SessionID != "term-1" {
		t.Fatalf("normalize: %+v %v", req, err)
	}
	if _, err := (TerminalAttachRequest{}).Normalize(); err == nil {
		t.Fatal("expected missing sessionId error")
	}
	if _, err := (TerminalTextRequest{}).Normalize(); err == nil {
		t.Fatal("expected missing sessionId error")
	}
	textReq, err := DecodeTerminalTextParams(json.RawMessage(`{"session_id":"term-2"}`))
	if err != nil {
		t.Fatalf("decode text: %v", err)
	}
	if textReq, err = textReq.Normalize(); err != nil || textReq.SessionID != "term-2" {
		t.Fatalf("normalize text: %+v %v", textReq, err)
	}
}

func TestTerminalListParamsRejectUnknownFields(t *testing.T) {
	if _, err := DecodeTerminalListParams(nil); err != nil {
		t.Fatalf("empty params: %v", err)
	}
	if _, err := DecodeTerminalListParams(json.RawMessage(`{}`)); err != nil {
		t.Fatalf("empty object: %v", err)
	}
	if _, err := DecodeTerminalListParams(json.RawMessage(`{"bogus":true}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestTerminalUploadParamsValidation(t *testing.T) {
	req, err := DecodeTerminalUploadParams(json.RawMessage(`{"sessionId":"term-1","name":"notes.txt","contentBase64":"aGVsbG8="}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req, err = req.Normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	content, err := req.Content()
	if err != nil || string(content) != "hello" {
		t.Fatalf("content: %q %v", content, err)
	}

	if _, err := (TerminalUploadRequest{SessionID: "s", Name: "", ContentBase64: ""}).Normalize(); err == nil {
		t.Fatal("empty name accepted")
	}
	if _, err := (TerminalUploadRequest{SessionID: "s", Name: strings.Repeat("n", 256), ContentBase64: ""}).Normalize(); err == nil {
		t.Fatal("oversized name accepted")
	}
	if _, err := (TerminalUploadRequest{SessionID: "s", Name: "a", ContentBase64: "abc"}).Normalize(); err == nil {
		t.Fatal("non-multiple-of-4 base64 accepted")
	}

	// Non-canonical padding bits must be rejected (QR== decodes but is not
	// canonical for the decoded byte).
	bad := TerminalUploadRequest{SessionID: "s", Name: "a", ContentBase64: "QR=="}
	if bad, err = bad.Normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if _, err := bad.Content(); err == nil {
		t.Fatal("non-canonical base64 accepted")
	}
	// Whitespace-containing payloads are not canonical either.
	ws := TerminalUploadRequest{SessionID: "s", Name: "a", ContentBase64: "aGVs\nbG8="}
	if _, err := ws.Content(); err == nil {
		t.Fatal("whitespace base64 accepted")
	}
	// Empty content is a valid zero-byte upload.
	empty := TerminalUploadRequest{SessionID: "s", Name: "a", ContentBase64: ""}
	if content, err := empty.Content(); err != nil || len(content) != 0 {
		t.Fatalf("empty content rejected: %q %v", content, err)
	}
}

func TestAttachGrantParams(t *testing.T) {
	req, err := DecodeAttachGrantParams(json.RawMessage(`{"sessionKey":" sess-1 ","ttlMs":5000}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req, err = req.Normalize(); err != nil || req.SessionKey != "sess-1" || req.TTLMs != 5000 {
		t.Fatalf("normalize: %+v %v", req, err)
	}
	if _, err := (AttachGrantRequest{}).Normalize(); err == nil {
		t.Fatal("missing sessionKey accepted")
	}
	if _, err := DecodeAttachGrantParams(json.RawMessage(`{"sessionKey":"s","bogus":1}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestAttachRevokeParams(t *testing.T) {
	req, err := DecodeAttachRevokeParams(json.RawMessage(`{"token":" tok "}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req, err = req.Normalize(); err != nil || req.Token != "tok" {
		t.Fatalf("normalize: %+v %v", req, err)
	}
	if _, err := (AttachRevokeRequest{}).Normalize(); err == nil {
		t.Fatal("missing token accepted")
	}
}
