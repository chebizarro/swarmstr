package contextvm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"

	nostruntime "metiq/internal/nostr/runtime"
)

// ═══════════════════════════════════════════════════════════════════════════════
// Mock keyer for encryption/decryption tests
// ═══════════════════════════════════════════════════════════════════════════════

type mockKeyer struct {
	pubKey    nostr.PubKey
	encReply  string
	encErr    error
	decReply  string
	decErr    error
	signErr   error
}

func (m *mockKeyer) GetPublicKey(_ context.Context) (nostr.PubKey, error) {
	return m.pubKey, nil
}

func (m *mockKeyer) SignEvent(_ context.Context, evt *nostr.Event) error {
	if m.signErr != nil {
		return m.signErr
	}
	// Minimal signing: set PubKey.
	evt.PubKey = m.pubKey
	return nil
}

func (m *mockKeyer) Encrypt(_ context.Context, _ string, _ nostr.PubKey) (string, error) {
	if m.encErr != nil {
		return "", m.encErr
	}
	return m.encReply, nil
}

func (m *mockKeyer) Decrypt(_ context.Context, _ string, _ nostr.PubKey) (string, error) {
	if m.decErr != nil {
		return "", m.decErr
	}
	return m.decReply, nil
}

// mockNIP04Keyer also supports NIP-04 encrypt/decrypt
type mockNIP04Keyer struct {
	mockKeyer
	enc04Reply string
	enc04Err   error
	dec04Reply string
	dec04Err   error
}

func (m *mockNIP04Keyer) EncryptNIP04(_ context.Context, _ string, _ nostr.PubKey) (string, error) {
	if m.enc04Err != nil {
		return "", m.enc04Err
	}
	return m.enc04Reply, nil
}

func (m *mockNIP04Keyer) DecryptNIP04(_ context.Context, _ string, _ nostr.PubKey) (string, error) {
	if m.dec04Err != nil {
		return "", m.dec04Err
	}
	return m.dec04Reply, nil
}

// Ensure interface compliance
var _ nostr.Keyer = (*mockKeyer)(nil)
var _ nostr.Keyer = (*mockNIP04Keyer)(nil)
var _ nostruntime.NIP04Encrypter = (*mockNIP04Keyer)(nil)
var _ nostruntime.NIP04Decrypter = (*mockNIP04Keyer)(nil)

// ═══════════════════════════════════════════════════════════════════════════════
// ListTools / SendRaw go through sendRequest() directly (not the mockable
// sendContextVMRequestWithTimeout), so they require real nostr infra.
// We skip full integration and instead test the parse paths via executeJSONRPC.
// ═══════════════════════════════════════════════════════════════════════════════

func TestExecuteJSONRPC_ToolsListParsing(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	sendContextVMRequestWithTimeout = mockRequestFn(`{"jsonrpc":"2.0","result":{"tools":[{"name":"echo","description":"Echo input"}]}}`)

	raw, err := executeJSONRPC(context.Background(), nil, nil, nil, "pk", map[string]any{
		"method": "tools/list",
	}, 30*time.Second, "none")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Tools []ToolDef `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 1 || result.Tools[0].Name != "echo" {
		t.Errorf("tools: %+v", result.Tools)
	}
}

func TestExecuteJSONRPC_ServerErrorReturned(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	sendContextVMRequestWithTimeout = mockRequestFn(`{"jsonrpc":"2.0","error":{"message":"method not found"}}`)

	_, err := executeJSONRPC(context.Background(), nil, nil, nil, "pk", map[string]any{}, 30*time.Second, "none")
	if err == nil || !strings.Contains(err.Error(), "method not found") {
		t.Errorf("err: %v", err)
	}
}

func TestExecuteJSONRPC_TransportFailure(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	sendContextVMRequestWithTimeout = mockRequestErr("connection refused")

	_, err := executeJSONRPC(context.Background(), nil, nil, nil, "pk", map[string]any{}, 30*time.Second, "none")
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("err: %v", err)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// encryptForServer tests (14.3% → much higher)
// ═══════════════════════════════════════════════════════════════════════════════

func TestEncryptForServer_NonePassthrough(t *testing.T) {
	pk, _ := nostr.PubKeyFromHex("0000000000000000000000000000000000000000000000000000000000000001")
	ct, err := encryptForServer(context.Background(), nil, pk, "hello", "none")
	if err != nil {
		t.Fatal(err)
	}
	if ct != "hello" {
		t.Errorf("expected passthrough, got: %q", ct)
	}
}

func TestEncryptForServer_NIP44Success(t *testing.T) {
	pk, _ := nostr.PubKeyFromHex("0000000000000000000000000000000000000000000000000000000000000001")
	k := &mockKeyer{encReply: "encrypted44"}
	ct, err := encryptForServer(context.Background(), k, pk, "hello", "nip44")
	if err != nil {
		t.Fatal(err)
	}
	if ct != "encrypted44" {
		t.Errorf("ct: %q", ct)
	}
}

func TestEncryptForServer_NIP44Error(t *testing.T) {
	pk, _ := nostr.PubKeyFromHex("0000000000000000000000000000000000000000000000000000000000000001")
	k := &mockKeyer{encErr: fmt.Errorf("nip44 fail")}
	_, err := encryptForServer(context.Background(), k, pk, "hello", "nip44")
	if err == nil || !strings.Contains(err.Error(), "nip44 fail") {
		t.Errorf("err: %v", err)
	}
}

func TestEncryptForServer_NIP04Success(t *testing.T) {
	pk, _ := nostr.PubKeyFromHex("0000000000000000000000000000000000000000000000000000000000000001")
	k := &mockNIP04Keyer{
		mockKeyer:  mockKeyer{},
		enc04Reply: "encrypted04",
	}
	ct, err := encryptForServer(context.Background(), k, pk, "hello", "nip04")
	if err != nil {
		t.Fatal(err)
	}
	if ct != "encrypted04" {
		t.Errorf("ct: %q", ct)
	}
}

func TestEncryptForServer_NIP04NoSupport(t *testing.T) {
	pk, _ := nostr.PubKeyFromHex("0000000000000000000000000000000000000000000000000000000000000001")
	// plain mockKeyer does NOT implement NIP04Encrypter
	k := &mockKeyer{}
	_, err := encryptForServer(context.Background(), k, pk, "hello", "nip04")
	if err == nil || !strings.Contains(err.Error(), "does not support NIP-04") {
		t.Errorf("err: %v", err)
	}
}

func TestEncryptForServer_NIP04Error(t *testing.T) {
	pk, _ := nostr.PubKeyFromHex("0000000000000000000000000000000000000000000000000000000000000001")
	k := &mockNIP04Keyer{
		mockKeyer: mockKeyer{},
		enc04Err:  fmt.Errorf("nip04 broken"),
	}
	_, err := encryptForServer(context.Background(), k, pk, "hello", "nip04")
	if err == nil || !strings.Contains(err.Error(), "nip04 broken") {
		t.Errorf("err: %v", err)
	}
}

func TestEncryptForServer_AutoNIP44First(t *testing.T) {
	pk, _ := nostr.PubKeyFromHex("0000000000000000000000000000000000000000000000000000000000000001")
	k := &mockKeyer{encReply: "auto44"}
	ct, err := encryptForServer(context.Background(), k, pk, "hello", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if ct != "auto44" {
		t.Errorf("ct: %q", ct)
	}
}

func TestEncryptForServer_AutoFallbackNIP04(t *testing.T) {
	pk, _ := nostr.PubKeyFromHex("0000000000000000000000000000000000000000000000000000000000000001")
	k := &mockNIP04Keyer{
		mockKeyer:  mockKeyer{encErr: fmt.Errorf("nip44 unavailable")},
		enc04Reply: "auto04",
	}
	ct, err := encryptForServer(context.Background(), k, pk, "hello", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if ct != "auto04" {
		t.Errorf("ct: %q", ct)
	}
}

func TestEncryptForServer_AutoBothFail(t *testing.T) {
	pk, _ := nostr.PubKeyFromHex("0000000000000000000000000000000000000000000000000000000000000001")
	k := &mockNIP04Keyer{
		mockKeyer: mockKeyer{encErr: fmt.Errorf("nip44 fail")},
		enc04Err:  fmt.Errorf("nip04 fail"),
	}
	_, err := encryptForServer(context.Background(), k, pk, "hello", "auto")
	if err == nil || !strings.Contains(err.Error(), "no supported encryption path") {
		t.Errorf("err: %v", err)
	}
}

func TestEncryptForServer_AutoNoNIP04Keyer(t *testing.T) {
	pk, _ := nostr.PubKeyFromHex("0000000000000000000000000000000000000000000000000000000000000001")
	// plain mockKeyer: nip44 fails, no nip04 support
	k := &mockKeyer{encErr: fmt.Errorf("nip44 fail")}
	_, err := encryptForServer(context.Background(), k, pk, "hello", "auto")
	if err == nil || !strings.Contains(err.Error(), "no supported encryption path") {
		t.Errorf("err: %v", err)
	}
}

func TestEncryptForServer_UnknownMode(t *testing.T) {
	pk, _ := nostr.PubKeyFromHex("0000000000000000000000000000000000000000000000000000000000000001")
	_, err := encryptForServer(context.Background(), nil, pk, "hello", "unknown-mode")
	if err == nil || !strings.Contains(err.Error(), "unsupported mode") {
		t.Errorf("err: %v", err)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// decryptFromServer tests (0% → covered)
// ═══════════════════════════════════════════════════════════════════════════════

func TestDecryptFromServer_NIP44Success(t *testing.T) {
	pk, _ := nostr.PubKeyFromHex("0000000000000000000000000000000000000000000000000000000000000001")
	k := &mockKeyer{decReply: "plaintext44"}
	pt, err := decryptFromServer(context.Background(), k, pk, "cipher")
	if err != nil {
		t.Fatal(err)
	}
	if pt != "plaintext44" {
		t.Errorf("pt: %q", pt)
	}
}

func TestDecryptFromServer_NIP04Fallback(t *testing.T) {
	pk, _ := nostr.PubKeyFromHex("0000000000000000000000000000000000000000000000000000000000000001")
	k := &mockNIP04Keyer{
		mockKeyer:  mockKeyer{decErr: fmt.Errorf("nip44 fail")},
		dec04Reply: "plaintext04",
	}
	pt, err := decryptFromServer(context.Background(), k, pk, "cipher")
	if err != nil {
		t.Fatal(err)
	}
	if pt != "plaintext04" {
		t.Errorf("pt: %q", pt)
	}
}

func TestDecryptFromServer_BothFail(t *testing.T) {
	pk, _ := nostr.PubKeyFromHex("0000000000000000000000000000000000000000000000000000000000000001")
	k := &mockNIP04Keyer{
		mockKeyer: mockKeyer{decErr: fmt.Errorf("nip44 fail")},
		dec04Err:  fmt.Errorf("nip04 fail"),
	}
	_, err := decryptFromServer(context.Background(), k, pk, "cipher")
	if err == nil || !strings.Contains(err.Error(), "unable to decrypt") {
		t.Errorf("err: %v", err)
	}
}

func TestDecryptFromServer_NoNIP04(t *testing.T) {
	pk, _ := nostr.PubKeyFromHex("0000000000000000000000000000000000000000000000000000000000000001")
	// plain mockKeyer: nip44 fails, no nip04 support at all
	k := &mockKeyer{decErr: fmt.Errorf("nip44 fail")}
	_, err := decryptFromServer(context.Background(), k, pk, "cipher")
	if err == nil || !strings.Contains(err.Error(), "unable to decrypt") {
		t.Errorf("err: %v", err)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// decodeServerEvent edge cases
// ═══════════════════════════════════════════════════════════════════════════════

func TestDecodeServerEvent_SupportEncryptionTag(t *testing.T) {
	ev := nostr.Event{
		Tags: nostr.Tags{
			{"support_encryption", "true"},
		},
	}
	s := decodeServerEvent(ev)
	if !s.Encrypted {
		t.Error("expected Encrypted = true")
	}
}

func TestDecodeServerEvent_ShortTagsSkipped(t *testing.T) {
	ev := nostr.Event{
		Tags: nostr.Tags{
			{"x"}, // len < 2, should be skipped
			{"name", "test-server"},
		},
	}
	s := decodeServerEvent(ev)
	if s.Name != "test-server" {
		t.Errorf("name: %q", s.Name)
	}
}

func TestDecodeServerEvent_PictureAndWebsite(t *testing.T) {
	ev := nostr.Event{
		Tags: nostr.Tags{
			{"picture", "https://example.com/pic.png"},
			{"website", "https://example.com"},
		},
	}
	s := decodeServerEvent(ev)
	if s.Picture != "https://example.com/pic.png" {
		t.Errorf("picture: %q", s.Picture)
	}
	if s.Website != "https://example.com" {
		t.Errorf("website: %q", s.Website)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// CallToolWithTimeout edge: content parsing, isError field
// ═══════════════════════════════════════════════════════════════════════════════

func TestCallToolWithTimeout_IsError(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	sendContextVMRequestWithTimeout = mockRequestFn(`{
		"jsonrpc":"2.0",
		"result":{"content":[{"type":"text","text":"fail"}],"isError":true}
	}`)

	res, err := CallToolWithTimeout(context.Background(), nil, nil, nil, "pk", "tool", nil, 5*time.Second, "none")
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("expected IsError = true")
	}
	if len(res.Content) != 1 {
		t.Errorf("content: %+v", res.Content)
	}
}

func TestCallToolWithTimeout_EmptyContent(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	sendContextVMRequestWithTimeout = mockRequestFn(`{"jsonrpc":"2.0","result":{"content":[]}}`)

	res, err := CallToolWithTimeout(context.Background(), nil, nil, nil, "pk", "tool", nil, 5*time.Second, "none")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Content) != 0 {
		t.Errorf("expected empty content, got %d", len(res.Content))
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// executeJSONRPC edge: missing result key
// ═══════════════════════════════════════════════════════════════════════════════

func TestExecuteJSONRPC_EmptyResult(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	sendContextVMRequestWithTimeout = mockRequestFn(`{"jsonrpc":"2.0","result":null}`)

	_, err := executeJSONRPC(context.Background(), nil, nil, nil, "pk", map[string]any{}, 30*time.Second, "none")
	if err == nil || !strings.Contains(err.Error(), "missing result") {
		t.Errorf("err: %v", err)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// ListResources: unmarshal result with extra fields
// ═══════════════════════════════════════════════════════════════════════════════

func TestListResources_MultipleResources(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	sendContextVMRequestWithTimeout = mockRequestFn(`{
		"jsonrpc":"2.0",
		"result":{"resources":[
			{"uri":"file:///a.txt","name":"a"},
			{"uri":"file:///b.txt","name":"b","mimeType":"text/plain"}
		]}
	}`)

	resources, err := ListResources(context.Background(), nil, nil, nil, "pk", "none")
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 2 {
		t.Errorf("expected 2 resources, got %d", len(resources))
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// GetPrompt: error from server
// ═══════════════════════════════════════════════════════════════════════════════

func TestGetPrompt_ServerError(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	sendContextVMRequestWithTimeout = mockRequestFn(`{"jsonrpc":"2.0","error":{"message":"prompt not found"}}`)

	_, err := GetPrompt(context.Background(), nil, nil, nil, "pk", "missing", nil, "none")
	if err == nil || !strings.Contains(err.Error(), "prompt not found") {
		t.Errorf("err: %v", err)
	}
}

func TestGetPrompt_NetworkError(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	sendContextVMRequestWithTimeout = mockRequestErr("connection reset")

	_, err := GetPrompt(context.Background(), nil, nil, nil, "pk", "test", nil, "none")
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Errorf("err: %v", err)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// ReadResource: successful parse with mime type
// ═══════════════════════════════════════════════════════════════════════════════

func TestReadResource_WithMimeType(t *testing.T) {
	prev := sendContextVMRequestWithTimeout
	defer func() { sendContextVMRequestWithTimeout = prev }()

	sendContextVMRequestWithTimeout = mockRequestFn(`{
		"jsonrpc":"2.0",
		"result":{"contents":[{"uri":"file:///x","text":"hello","mimeType":"text/plain"}]}
	}`)

	res, err := ReadResource(context.Background(), nil, nil, nil, "pk", "file:///x", "none")
	if err != nil {
		t.Fatal(err)
	}
	contents, ok := res["contents"]
	if !ok {
		t.Fatal("missing contents key")
	}
	arr, ok := contents.([]any)
	if !ok || len(arr) != 1 {
		t.Errorf("contents: %v", contents)
	}
}
