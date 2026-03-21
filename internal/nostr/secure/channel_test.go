package secure_test

import (
	"context"
	"errors"
	"testing"

	"metiq/internal/nostr/secure"
	"metiq/internal/plugins/sdk"
)

// ─── stub ChannelHandle ───────────────────────────────────────────────────────

type stubHandle struct {
	id       string
	lastSent string
	sendErr  error
}

func (h *stubHandle) ID() string                                { return h.id }
func (h *stubHandle) Send(_ context.Context, text string) error { h.lastSent = text; return h.sendErr }
func (h *stubHandle) Close()                                    {}

// ─── stub ChannelPlugin ───────────────────────────────────────────────────────

type stubPlugin struct {
	handle        *stubHandle
	lastOnMessage func(sdk.InboundChannelMessage)
}

func (p *stubPlugin) ID() string                   { return "stub" }
func (p *stubPlugin) Type() string                 { return "stub" }
func (p *stubPlugin) ConfigSchema() map[string]any { return nil }
func (p *stubPlugin) Connect(
	_ context.Context,
	_ string,
	_ map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	p.lastOnMessage = onMessage
	return p.handle, nil
}

// ─── NIP44PeerCodec tests ─────────────────────────────────────────────────────

// Use low-value private keys that the nostr library accepts as valid scalars.
const (
	alicePrivHex = "0000000000000000000000000000000000000000000000000000000000000001"
	bobPrivHex   = "0000000000000000000000000000000000000000000000000000000000000002"
)

func TestNIP44PeerCodec_RoundTrip(t *testing.T) {
	aliceToBob, err := secure.NewNIP44PeerCodecFromPrivKeys(alicePrivHex, bobPrivHex)
	if err != nil {
		t.Fatalf("new alice→bob codec: %v", err)
	}
	bobToAlice, err := secure.NewNIP44PeerCodecFromPrivKeys(bobPrivHex, alicePrivHex)
	if err != nil {
		t.Fatalf("new bob→alice codec: %v", err)
	}

	plaintext := "hello, encrypted world!"
	ciphertext, enc, err := aliceToBob.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if enc != "nip44" {
		t.Errorf("expected enc=nip44, got %q", enc)
	}
	if ciphertext == plaintext {
		t.Error("ciphertext should differ from plaintext")
	}

	recovered, err := bobToAlice.Decrypt(ciphertext, enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if recovered != plaintext {
		t.Errorf("round-trip: got %q, want %q", recovered, plaintext)
	}
}

func TestNIP44PeerCodec_DecryptPlaintext(t *testing.T) {
	codec, err := secure.NewNIP44PeerCodecFromPrivKeys(alicePrivHex, bobPrivHex)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	result, err := codec.Decrypt("raw text", "")
	if err != nil {
		t.Fatalf("decrypt plaintext: %v", err)
	}
	if result != "raw text" {
		t.Errorf("got %q, want %q", result, "raw text")
	}
}

func TestNIP44PeerCodec_EncryptEmptyReturnsError(t *testing.T) {
	codec, err := secure.NewNIP44PeerCodecFromPrivKeys(alicePrivHex, bobPrivHex)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, _, err = codec.Encrypt("")
	if err == nil {
		t.Error("expected error encrypting empty string")
	}
}

func TestNIP44PeerCodec_DecryptUnknownEncReturnsError(t *testing.T) {
	codec, err := secure.NewNIP44PeerCodecFromPrivKeys(alicePrivHex, bobPrivHex)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err = codec.Decrypt("ciphertext", "nip04")
	if err == nil {
		t.Error("expected error for unknown enc scheme")
	}
}

func TestPubKeyHexFromPrivKeyHex(t *testing.T) {
	pub, err := secure.PubKeyHexFromPrivKeyHex(alicePrivHex)
	if err != nil {
		t.Fatalf("derive pubkey: %v", err)
	}
	if len(pub) != 64 {
		t.Errorf("expected 64-char hex pubkey, got len=%d: %q", len(pub), pub)
	}
}

// ─── EncryptedHandle tests ────────────────────────────────────────────────────

func TestEncryptedHandle_SendEncrypts(t *testing.T) {
	codec, err := secure.NewNIP44PeerCodecFromPrivKeys(alicePrivHex, bobPrivHex)
	if err != nil {
		t.Fatalf("setup codec: %v", err)
	}
	raw := &stubHandle{id: "h1"}
	handle := secure.NewEncryptedHandle(raw, codec)

	if err := handle.Send(context.Background(), "secret message"); err != nil {
		t.Fatalf("send: %v", err)
	}

	if raw.lastSent == "secret message" {
		t.Error("expected encrypted payload, got plaintext")
	}
	if len(raw.lastSent) < 7 || raw.lastSent[:6] != "nip44:" {
		t.Errorf("expected nip44: prefix in %q", raw.lastSent)
	}
}

func TestEncryptedHandle_DecryptInbound_RoundTrip(t *testing.T) {
	aliceToBob, err := secure.NewNIP44PeerCodecFromPrivKeys(alicePrivHex, bobPrivHex)
	if err != nil {
		t.Fatalf("setup alice codec: %v", err)
	}
	bobToAlice, err := secure.NewNIP44PeerCodecFromPrivKeys(bobPrivHex, alicePrivHex)
	if err != nil {
		t.Fatalf("setup bob codec: %v", err)
	}

	// Alice sends.
	rawAlice := &stubHandle{id: "alice"}
	aliceHandle := secure.NewEncryptedHandle(rawAlice, aliceToBob)
	if err := aliceHandle.Send(context.Background(), "ping from alice"); err != nil {
		t.Fatalf("alice send: %v", err)
	}

	// Bob decrypts what Alice sent.
	bobHandle := secure.NewEncryptedHandle(&stubHandle{id: "bob"}, bobToAlice)
	plaintext, err := bobHandle.DecryptInbound(rawAlice.lastSent)
	if err != nil {
		t.Fatalf("bob decrypt: %v", err)
	}
	if plaintext != "ping from alice" {
		t.Errorf("got %q, want %q", plaintext, "ping from alice")
	}
}

func TestEncryptedHandle_SendError_Propagates(t *testing.T) {
	codec, err := secure.NewNIP44PeerCodecFromPrivKeys(alicePrivHex, bobPrivHex)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	raw := &stubHandle{id: "h1", sendErr: errors.New("network failure")}
	handle := secure.NewEncryptedHandle(raw, codec)

	err = handle.Send(context.Background(), "hello")
	if err == nil {
		t.Error("expected propagated send error")
	}
}

// ─── EncryptedChannelPlugin tests ─────────────────────────────────────────────

func TestEncryptedChannelPlugin_NoKeys_PlaintextPassthrough(t *testing.T) {
	inner := &stubPlugin{handle: &stubHandle{id: "ch1"}}
	plugin := secure.NewEncryptedChannelPlugin(inner)

	var received string
	cfg := map[string]any{} // no e2e keys
	handle, err := plugin.Connect(context.Background(), "ch1", cfg, func(msg sdk.InboundChannelMessage) {
		received = msg.Text
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Send should be plaintext.
	if err := handle.Send(context.Background(), "hello"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if inner.handle.lastSent != "hello" {
		t.Errorf("expected plaintext send, got %q", inner.handle.lastSent)
	}

	// Simulate plaintext inbound.
	inner.lastOnMessage(sdk.InboundChannelMessage{Text: "world", SenderID: "x"})
	if received != "world" {
		t.Errorf("expected plaintext inbound %q, got %q", "world", received)
	}
}

func TestEncryptedChannelPlugin_WithKeys_EncryptDecrypt(t *testing.T) {
	inner := &stubPlugin{handle: &stubHandle{id: "ch2"}}
	plugin := secure.NewEncryptedChannelPlugin(inner)

	// Derive Bob's pubkey so we can pass it in the config.
	bobPubHex, err := secure.PubKeyHexFromPrivKeyHex(bobPrivHex)
	if err != nil {
		t.Fatalf("derive bob pubkey: %v", err)
	}

	var received string
	cfg := map[string]any{
		"e2e_private_key": alicePrivHex,
		"e2e_peer_pubkey": bobPubHex,
	}

	handle, err := plugin.Connect(context.Background(), "ch2", cfg, func(msg sdk.InboundChannelMessage) {
		received = msg.Text
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Send should be encrypted (nip44: prefix).
	if err := handle.Send(context.Background(), "secret"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if inner.handle.lastSent == "secret" {
		t.Error("expected encrypted payload, got plaintext")
	}
	if len(inner.handle.lastSent) < 7 || inner.handle.lastSent[:6] != "nip44:" {
		t.Errorf("expected nip44: prefix in %q", inner.handle.lastSent)
	}

	// Simulate Bob encrypting "hello from bob" for Alice and sending it inbound.
	bobCodec, err := secure.NewNIP44PeerCodecFromPrivKeys(bobPrivHex, alicePrivHex)
	if err != nil {
		t.Fatalf("bob codec: %v", err)
	}
	ct, enc, err := bobCodec.Encrypt("hello from bob")
	if err != nil {
		t.Fatalf("bob encrypt: %v", err)
	}
	payload := enc + ":" + ct
	inner.lastOnMessage(sdk.InboundChannelMessage{Text: payload, SenderID: "bob"})

	if received != "hello from bob" {
		t.Errorf("expected decrypted inbound %q, got %q", "hello from bob", received)
	}
}
