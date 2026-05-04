package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
)

func mustSecretKey(t *testing.T, skHex string) nostr.SecretKey {
	t.Helper()
	sk, err := nostr.SecretKeyFromHex(skHex)
	if err != nil {
		t.Fatalf("parse secret key: %v", err)
	}
	return sk
}

func mustPubKey(t *testing.T, sk nostr.SecretKey) nostr.PubKey {
	t.Helper()
	return nostr.GetPublicKey([32]byte(sk))
}

func TestDecryptNIP04RejectsSenderMismatch(t *testing.T) {
	recipient := mustSecretKey(t, "8f2a559490f4f35f4b2f8a8e02b2b3ec0ed0098f0d8b0f5e53f62f8c33f1f4a1")
	sender := mustSecretKey(t, "7d4d5ae5d62b37dd4ce1d85d17f9f5cc3a6f7d42b8f42ce1d0f615db2a0c2b83")
	wrongSender := mustSecretKey(t, "1c4c50d67b3f11a6c85aa9b9b97d3d5e4dcfc7f7d7f828948a1d1b57f96f0e2b")

	ciphertext, err := encryptNIP04(sender, mustPubKey(t, recipient), "hello from sender")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	_, err = decryptNIP04(recipient, mustPubKey(t, wrongSender), ciphertext)
	if err == nil {
		t.Fatal("expected sender mismatch to fail")
	}
	if !errors.Is(err, ErrInvalidPadding) && !errors.Is(err, ErrInvalidPlaintext) {
		t.Fatalf("expected padding/plaintext validation error, got %v", err)
	}
}

func TestPKCS7UnpadRejectsMalformedPadding(t *testing.T) {
	_, err := pkcs7Unpad([]byte("bad-padding\x02\x03"), 16)
	if !errors.Is(err, ErrInvalidPadding) {
		t.Fatalf("expected ErrInvalidPadding, got %v", err)
	}
}

// ─── Additional NIP-04 and DM bus tests (Phase 6) ───────────────────────────

func TestNIP04_EncryptDecryptRoundTrip(t *testing.T) {
	sk1 := nostr.Generate()
	pk1 := nostr.GetPublicKey(sk1)
	sk2 := nostr.Generate()
	pk2 := nostr.GetPublicKey(sk2)

	plaintext := "hello, this is a secret message"
	ciphertext, err := encryptNIP04(sk1, pk2, plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if ciphertext == "" || ciphertext == plaintext {
		t.Fatal("ciphertext should differ from plaintext")
	}
	got, err := decryptNIP04(sk2, pk1, ciphertext)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != plaintext {
		t.Errorf("decrypted: %q, want %q", got, plaintext)
	}
}

func TestNIP04_DecryptInvalidFormat(t *testing.T) {
	sk := nostr.Generate()
	pk := nostr.GetPublicKey(sk)
	tests := []struct {
		name    string
		content string
	}{
		{"no iv separator", "justciphertext"},
		{"empty ciphertext", "?iv=AAAAAAAAAAAAAAAAAAAAAA=="},
		{"empty iv", "AAAAAAAAAA==?iv="},
		{"empty both", "?iv="},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decryptNIP04(sk, pk, tt.content)
			if err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestDecryptNIP04WithSharedSecret_BadBase64Ciphertext(t *testing.T) {
	_, err := decryptNIP04WithSharedSecret(make([]byte, 32), "!!!not-base64!!!?iv=AAAAAAAAAAAAAAAAAAAAAA==")
	if err == nil {
		t.Error("expected error for bad base64 ciphertext")
	}
}

func TestDecryptNIP04WithSharedSecret_BadBase64IV(t *testing.T) {
	_, err := decryptNIP04WithSharedSecret(make([]byte, 32), "AAAAAAAAAA==?iv=!!!not-base64!!!")
	if err == nil {
		t.Error("expected error for bad base64 iv")
	}
}

func TestDecryptNIP04WithSharedSecret_WrongIVLength(t *testing.T) {
	_, err := decryptNIP04WithSharedSecret(make([]byte, 32), "AAAAAAAAAA==?iv=AAAAAAAAAAA=")
	if err == nil {
		t.Error("expected error for wrong IV length")
	}
}

func TestPkcs7Unpad_Valid(t *testing.T) {
	data := []byte("hello world!")
	padding := byte(4)
	padded := append(data, padding, padding, padding, padding)
	result, err := pkcs7Unpad(padded, 16)
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != "hello world!" {
		t.Errorf("got %q", string(result))
	}
}

func TestPkcs7Unpad_FullBlockPadding(t *testing.T) {
	padded := make([]byte, 16)
	for i := range padded {
		padded[i] = 16
	}
	result, err := pkcs7Unpad(padded, 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty, got %d bytes", len(result))
	}
}

func TestPkcs7Unpad_EmptyInput(t *testing.T) {
	_, err := pkcs7Unpad(nil, 16)
	if !errors.Is(err, ErrInvalidPadding) {
		t.Errorf("expected ErrInvalidPadding, got %v", err)
	}
}

func TestPkcs7Unpad_ZeroPadByte(t *testing.T) {
	data := make([]byte, 16)
	data[15] = 0
	_, err := pkcs7Unpad(data, 16)
	if !errors.Is(err, ErrInvalidPadding) {
		t.Errorf("expected ErrInvalidPadding, got %v", err)
	}
}

func TestPkcs7Unpad_InconsistentPadding(t *testing.T) {
	data := make([]byte, 16)
	data[12] = 1
	data[13] = 4
	data[14] = 4
	data[15] = 4
	_, err := pkcs7Unpad(data, 16)
	if !errors.Is(err, ErrInvalidPadding) {
		t.Errorf("expected ErrInvalidPadding, got %v", err)
	}
}

func TestPkcs7Unpad_PadExceedsBlockSize(t *testing.T) {
	data := make([]byte, 16)
	data[15] = 17
	_, err := pkcs7Unpad(data, 16)
	if !errors.Is(err, ErrInvalidPadding) {
		t.Errorf("expected ErrInvalidPadding, got %v", err)
	}
}

func TestNIP04KeyerAdapter_RoundTrip(t *testing.T) {
	sk1 := nostr.Generate()
	pk1 := nostr.GetPublicKey(sk1)
	sk2 := nostr.Generate()
	pk2 := nostr.GetPublicKey(sk2)

	adapter1 := newNIP04KeyerAdapter(sk1)
	adapter2 := newNIP04KeyerAdapter(sk2)
	ctx := context.Background()

	ciphertext, err := adapter1.EncryptNIP04(ctx, "secret", pk2)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	plaintext, err := adapter2.DecryptNIP04(ctx, ciphertext, pk1)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if plaintext != "secret" {
		t.Errorf("plaintext: %q", plaintext)
	}
}

func TestDMBus_MarkSeen(t *testing.T) {
	bus := &DMBus{
		seenSet: map[string]struct{}{},
		seenCap: 3,
	}
	if bus.markSeen("id1") {
		t.Error("first time should not be seen")
	}
	if !bus.markSeen("id1") {
		t.Error("second time should be seen")
	}
	bus.markSeen("id2")
	bus.markSeen("id3")
	bus.markSeen("id4")
	if _, ok := bus.seenSet["id1"]; ok {
		t.Error("id1 should have been evicted")
	}
}

func TestDMBus_EmitErr_NilHandler(t *testing.T) {
	bus := &DMBus{}
	bus.emitErr(nil)
	bus.emitErr(context.Canceled)
}

func TestDMBus_EmitErr_WithHandler(t *testing.T) {
	var got error
	bus := &DMBus{onError: func(err error) { got = err }}
	bus.emitErr(context.Canceled)
	if got != context.Canceled {
		t.Errorf("got %v", got)
	}
}

func TestDMBusHandledAuthClosedIsNotFailure(t *testing.T) {
	relay := "wss://auth.example"
	health := NewRelayHealthTracker()
	health.Seed([]string{relay})
	subHealth := NewSubHealthTracker("dm")
	errCount := 0
	b := &DMBus{
		health:    health,
		subHealth: subHealth,
		onError:   func(error) { errCount++ },
	}

	if b.handleDMRelayClose(relay, "auth-required: sign in", true) {
		t.Fatal("handled auth CLOSED should not schedule a failure retry")
	}
	if errCount != 0 {
		t.Fatalf("handled auth CLOSED should not emit user-visible errors, got %d", errCount)
	}
	snap := subHealth.Snapshot([]string{relay}, DMReplayWindowDefault)
	if snap.LastClosedReason != "" || snap.LastClosedRelay != "" {
		t.Fatalf("handled auth CLOSED should not latch sub-health close state: %+v", snap)
	}
	if !health.Allowed(relay, time.Now()) {
		t.Fatal("handled auth CLOSED should not degrade relay health")
	}
}

func TestDMBusNonAuthClosedRecordsFailure(t *testing.T) {
	relay := "wss://closed.example"
	health := NewRelayHealthTracker()
	health.Seed([]string{relay})
	subHealth := NewSubHealthTracker("dm")
	var gotErr error
	b := &DMBus{
		health:    health,
		subHealth: subHealth,
		onError:   func(err error) { gotErr = err },
	}

	if !b.handleDMRelayClose(relay, "restricted: policy", false) {
		t.Fatal("non-auth CLOSED should schedule a retry")
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "restricted") {
		t.Fatalf("expected surfaced close error, got %v", gotErr)
	}
	snap := subHealth.Snapshot([]string{relay}, DMReplayWindowDefault)
	if snap.LastClosedReason != "restricted: policy" || snap.LastClosedRelay != relay {
		t.Fatalf("sub-health close not recorded: %+v", snap)
	}
}

func TestDMBusEOSERecordsRelaySuccess(t *testing.T) {
	relay := "wss://eose.example"
	health := NewRelayHealthTracker()
	health.Seed([]string{relay})
	health.RecordFailure(relay)
	b := &DMBus{health: health}

	b.handleDMRelayEOSE(relay)

	if !health.Allowed(relay, time.Now()) {
		t.Fatal("EOSE should be consumed as relay progress, not a subscription failure")
	}
}

func TestDMBusRejectsFutureDM(t *testing.T) {
	recipient := nostr.Generate()
	sender := nostr.Generate()
	ciphertext, err := encryptNIP04(sender, nostr.GetPublicKey(recipient), "hello from the future")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	evt := nostr.Event{
		Kind:      nostr.KindEncryptedDirectMessage,
		CreatedAt: nostr.Timestamp(time.Now().Add(time.Hour).Unix()),
		Tags:      nostr.Tags{{"p", nostr.GetPublicKey(recipient).Hex()}},
		Content:   ciphertext,
	}
	if err := newNIP04KeyerAdapter(sender).SignEvent(context.Background(), &evt); err != nil {
		t.Fatalf("sign: %v", err)
	}

	var gotErr error
	b := &DMBus{
		public:       nostr.GetPublicKey(recipient),
		nip04Keyer:   newNIP04KeyerAdapter(recipient),
		hasNIP04Key:  true,
		replayWindow: 0,
		seenSet:      map[string]struct{}{},
		seenCap:      16,
		messageQueue: make(chan InboundDM, 1),
		ctx:          context.Background(),
		onError:      func(err error) { gotErr = err },
	}

	b.handleInbound(nostr.RelayEvent{Event: evt, Relay: &nostr.Relay{URL: "wss://relay.example"}})

	if gotErr == nil || !strings.Contains(gotErr.Error(), "future dm") {
		t.Fatalf("expected future-skew rejection, got %v", gotErr)
	}
	select {
	case msg := <-b.messageQueue:
		t.Fatalf("future DM should not be delivered: %+v", msg)
	default:
	}
}

func TestDMBus_NIP04EncryptKeyer_WithLocalKey(t *testing.T) {
	sk := nostr.Generate()
	adapter := newNIP04KeyerAdapter(sk)
	bus := &DMBus{nip04Keyer: adapter, hasNIP04Key: true}
	got := bus.nip04EncryptKeyer()
	if _, ok := got.(nip04KeyerAdapter); !ok {
		t.Errorf("expected nip04KeyerAdapter, got %T", got)
	}
}

func TestDMBus_NIP04EncryptKeyer_FallsBackToSignKeyer(t *testing.T) {
	sk := nostr.Generate()
	adapter := newNIP04KeyerAdapter(sk)
	bus := &DMBus{signKeyer: adapter, hasNIP04Key: false}
	got := bus.nip04EncryptKeyer()
	if got != adapter {
		t.Error("should fall back to signKeyer")
	}
}

func TestChunkDMText_ShortText(t *testing.T) {
	text := "Hello, world!"
	chunks := chunkDMText(text)
	if len(chunks) != 1 || chunks[0] != text {
		t.Errorf("expected single chunk %q, got %v", text, chunks)
	}
}

func TestChunkDMText_EmptyText(t *testing.T) {
	chunks := chunkDMText("")
	if len(chunks) != 0 {
		t.Errorf("expected no chunks for empty text, got %v", chunks)
	}
	chunks = chunkDMText("   ")
	if len(chunks) != 0 {
		t.Errorf("expected no chunks for whitespace, got %v", chunks)
	}
}

func TestChunkDMText_LongText(t *testing.T) {
	// Create text longer than maxDMPlaintextRunes
	long := strings.Repeat("word ", maxDMPlaintextRunes/4) // ~5 chars per word
	chunks := chunkDMText(long)
	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks for long text, got %d", len(chunks))
	}
	for i, chunk := range chunks {
		runeCount := len([]rune(chunk))
		if runeCount > maxDMPlaintextRunes {
			t.Errorf("chunk %d exceeds limit: %d > %d", i, runeCount, maxDMPlaintextRunes)
		}
	}
}

func TestChunkDMText_PrefersParagraphBreak(t *testing.T) {
	// Build text with paragraph break - total must exceed limit
	part := strings.Repeat("x", maxDMPlaintextRunes*2/3)
	text := part + "\n\n" + part
	chunks := chunkDMText(text)
	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks split at paragraph, got %d", len(chunks))
	}
	// First chunk should include content up to the paragraph break
	if !strings.HasSuffix(chunks[0], "x") {
		t.Errorf("expected first chunk to end with x content, got %q", chunks[0][len(chunks[0])-20:])
	}
}

func TestChunkDMText_PrefersSentenceBreak(t *testing.T) {
	// Build text with sentence break - total must exceed limit
	sentence := strings.Repeat("x", maxDMPlaintextRunes*2/3) + ". "
	text := sentence + strings.Repeat("y", maxDMPlaintextRunes*2/3)
	chunks := chunkDMText(text)
	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks split at sentence, got %d", len(chunks))
	}
	// First chunk should end with the sentence (period)
	if !strings.HasSuffix(strings.TrimSpace(chunks[0]), ".") {
		t.Errorf("expected first chunk to end with period, got %q", chunks[0])
	}
}
