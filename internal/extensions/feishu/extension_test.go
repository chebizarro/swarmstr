package feishu

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"metiq/internal/plugins/sdk"
)

func TestPlugin_ID(t *testing.T) {
	p := &FeishuPlugin{}
	if id := p.ID(); id != "feishu" {
		t.Fatalf("expected feishu, got %s", id)
	}
}

func TestPlugin_Type(t *testing.T) {
	p := &FeishuPlugin{}
	if typ := p.Type(); typ == "" {
		t.Fatal("Type must not be empty")
	}
}

func TestPlugin_ConfigSchema(t *testing.T) {
	p := &FeishuPlugin{}
	schema := p.ConfigSchema()
	if schema == nil {
		t.Fatal("ConfigSchema must not be nil")
	}
	for _, key := range []string{"app_id", "app_secret", "chat_id"} {
		props, _ := schema["properties"].(map[string]any)
		if _, ok := props[key]; !ok {
			t.Errorf("missing expected property %q", key)
		}
	}
}

func TestPlugin_Capabilities(t *testing.T) {
	p := &FeishuPlugin{}
	caps := p.Capabilities()
	if !caps.Typing {
		t.Error("expected Typing capability")
	}
	if !caps.Threads {
		t.Error("expected Threads capability")
	}
}

func TestPlugin_GatewayMethods(t *testing.T) {
	p := &FeishuPlugin{}
	methods := p.GatewayMethods()
	if methods != nil {
		t.Errorf("expected nil GatewayMethods, got %v", methods)
	}
}

func TestPlugin_ImplementsChannelPlugin(t *testing.T) {
	var _ sdk.ChannelPlugin = (*FeishuPlugin)(nil)
}

func TestDecryptEvent_RoundTrip(t *testing.T) {
	key := "test-encrypt-key-12345"
	plaintext := []byte(`{"event":"hello"}`)

	// Encrypt with AES-CBC + PKCS7 using same key derivation as the code.
	h := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(h[:])
	if err != nil {
		t.Fatal(err)
	}
	// Pad plaintext to block size.
	padLen := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := make([]byte, len(plaintext)+padLen)
	copy(padded, plaintext)
	for i := len(plaintext); i < len(padded); i++ {
		padded[i] = byte(padLen)
	}
	iv := make([]byte, aes.BlockSize) // zero IV for test
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)

	encoded := base64.StdEncoding.EncodeToString(append(iv, ct...))

	got, err := decryptEvent(key, encoded)
	if err != nil {
		t.Fatalf("decryptEvent: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("expected %q, got %q", plaintext, got)
	}
}

func TestDecryptEvent_BadBase64(t *testing.T) {
	_, err := decryptEvent("key", "not-valid-base64!!!")
	if err == nil {
		t.Fatal("expected error for bad base64")
	}
}

func TestDecryptEvent_TooShort(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("tiny"))
	_, err := decryptEvent("key", short)
	if err == nil {
		t.Fatal("expected error for short ciphertext")
	}
}

func TestDecryptEvent_BadPadding(t *testing.T) {
	key := "test-key"
	h := sha256.Sum256([]byte(key))
	block, _ := aes.NewCipher(h[:])

	// Craft ciphertext with invalid padding (last byte = 0).
	iv := make([]byte, aes.BlockSize)
	plain := make([]byte, aes.BlockSize) // all zeros = pad byte 0 which is invalid
	ct := make([]byte, aes.BlockSize)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, plain)

	encoded := base64.StdEncoding.EncodeToString(append(iv, ct...))
	_, err := decryptEvent(key, encoded)
	if err == nil {
		t.Fatal("expected error for invalid padding")
	}
}

func TestBotID(t *testing.T) {
	b := &feishuBot{channelID: "test-ch"}
	if b.ID() != "test-ch" {
		t.Errorf("expected test-ch, got %s", b.ID())
	}
}

func TestBotGetToken_Empty(t *testing.T) {
	b := &feishuBot{}
	if tok := b.getToken(); tok != "" {
		t.Errorf("expected empty token, got %q", tok)
	}
}
