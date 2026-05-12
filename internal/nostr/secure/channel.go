package secure

import (
	"context"
	"fmt"
	"strings"

	"metiq/internal/plugins/sdk"
)

// EncryptedHandle wraps an sdk.ChannelHandle, transparently applying NIP-44
// encryption to outbound Send() calls and making the Encrypt/Decrypt helpers
// available for callers that process inbound messages.
//
// Usage:
//
//	codec, _ := NewNIP44PeerCodec(myPrivKey, peerPubKey)
//	wrapped  := NewEncryptedHandle(rawHandle, codec)
//	// wrapped.Send() encrypts before forwarding
//	// Decrypt inbound: plaintext, _ = wrapped.DecryptInbound(ciphertext)
type EncryptedHandle struct {
	sdk.ChannelHandle
	codec EnvelopeCodec
}

// NewEncryptedHandle wraps handle with transparent NIP-44 encryption.
// All Send() calls will be encrypted with codec before being forwarded to
// the underlying handle.  Inbound decryption is exposed via DecryptInbound().
func NewEncryptedHandle(handle sdk.ChannelHandle, codec EnvelopeCodec) *EncryptedHandle {
	return &EncryptedHandle{ChannelHandle: handle, codec: codec}
}

// Send encrypts text with the configured codec then forwards to the underlying
// handle's Send() method.
func (h *EncryptedHandle) Send(ctx context.Context, text string) error {
	ciphertext, enc, err := h.codec.Encrypt(text)
	if err != nil {
		return fmt.Errorf("encrypted channel send: encrypt: %w", err)
	}
	// Pack as "enc:ciphertext" so the receiver knows the encoding scheme.
	payload := enc + ":" + ciphertext
	return h.ChannelHandle.Send(ctx, payload)
}

// DecryptInbound parses and decrypts a payload received from the channel.
// Expected format: "<enc>:<ciphertext>" or plain text if enc == "".
func (h *EncryptedHandle) DecryptInbound(payload string) (string, error) {
	// Split on first colon to extract enc prefix.
	enc := ""
	ciphertext := payload
	for i := 0; i < len(payload); i++ {
		if payload[i] == ':' {
			enc = payload[:i]
			ciphertext = payload[i+1:]
			break
		}
	}
	return h.codec.Decrypt(ciphertext, enc)
}

// parseAndDecrypt splits a "<enc>:<ciphertext>" payload and calls codec.Decrypt.
func parseAndDecrypt(codec EnvelopeCodec, payload string) (string, error) {
	enc, ciphertext := splitEnvelopePayload(payload)
	return codec.Decrypt(ciphertext, enc)
}

func splitEnvelopePayload(payload string) (enc string, ciphertext string) {
	ciphertext = payload
	for i := 0; i < len(payload); i++ {
		if payload[i] == ':' {
			return payload[:i], payload[i+1:]
		}
	}
	return "", ciphertext
}

// SecretResolver resolves secret references such as env:NAME or $NAME before
// E2E channel keys are used. It is intentionally satisfied by secrets.Store
// without importing the secrets package into this transport decorator.
type SecretResolver interface {
	Resolve(ref string) (value string, found bool)
}

// EncryptedChannelPlugin is a ChannelPlugin decorator that wraps the handles
// returned by an underlying plugin with NIP-44 encryption.
//
// Config keys consumed (in addition to the underlying plugin's schema):
//   - "e2e_private_key": hex private key used for encryption
//   - "e2e_peer_pubkey": hex pubkey of the remote party to encrypt for
//
// Both keys must be present for encryption to be enabled.  If either is
// absent, the underlying handle is returned unwrapped (plaintext).
type EncryptedChannelPlugin struct {
	sdk.ChannelPlugin
	secrets SecretResolver
}

// NewEncryptedChannelPlugin wraps plugin with transparent NIP-44 encryption.
func NewEncryptedChannelPlugin(plugin sdk.ChannelPlugin) *EncryptedChannelPlugin {
	return &EncryptedChannelPlugin{ChannelPlugin: plugin}
}

// NewEncryptedChannelPluginWithSecrets wraps plugin with transparent NIP-44
// encryption and resolves e2e_private_key/e2e_peer_pubkey through resolver when
// those values are secret references.
func NewEncryptedChannelPluginWithSecrets(plugin sdk.ChannelPlugin, resolver SecretResolver) *EncryptedChannelPlugin {
	return &EncryptedChannelPlugin{ChannelPlugin: plugin, secrets: resolver}
}

// Connect delegates to the underlying plugin.Connect and, when e2e config keys
// are present, wraps the resulting handle with NIP-44 encryption.
func (p *EncryptedChannelPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	privKey, _ := cfg["e2e_private_key"].(string)
	peerPubKey, _ := cfg["e2e_peer_pubkey"].(string)
	required := boolConfig(cfg, "e2e.required") || boolConfig(cfg, "e2e_required")

	var err error
	privKey, err = p.resolveSecretValue(privKey)
	if err != nil {
		return nil, fmt.Errorf("encrypted channel %q: resolve e2e_private_key: %w", channelID, err)
	}
	peerPubKey, err = p.resolveSecretValue(peerPubKey)
	if err != nil {
		return nil, fmt.Errorf("encrypted channel %q: resolve e2e_peer_pubkey: %w", channelID, err)
	}

	// Wrap onMessage to decrypt inbound payloads if encryption is enabled.
	wrappedOnMessage := onMessage
	var codec *NIP44PeerCodec
	if privKey != "" || peerPubKey != "" || required {
		if privKey == "" || peerPubKey == "" {
			return nil, fmt.Errorf("encrypted channel %q: e2e_private_key and e2e_peer_pubkey are required when E2E is configured", channelID)
		}
		codec, err = NewNIP44PeerCodec(privKey, peerPubKey)
		if err != nil {
			return nil, fmt.Errorf("encrypted channel %q: init codec: %w", channelID, err)
		}
		wrappedOnMessage = func(msg sdk.InboundChannelMessage) {
			// Fail closed: when E2E is configured, plaintext, malformed, or
			// undecryptable inbound payloads are dropped instead of delivered.
			plain, decErr := parseAndDecrypt(codec, msg.Text)
			if decErr != nil || envelopeEncoding(msg.Text) != EncNIP44 {
				return
			}
			msg.Text = plain
			onMessage(msg)
		}
	}

	handle, err := p.ChannelPlugin.Connect(ctx, channelID, cfg, wrappedOnMessage)
	if err != nil {
		return nil, err
	}

	// Wrap handle with encryption only when both keys are configured.
	if codec != nil {
		return NewEncryptedHandle(handle, codec), nil
	}
	return handle, nil
}

func (p *EncryptedChannelPlugin) resolveSecretValue(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || p.secrets == nil || !looksLikeSecretRef(value) {
		return value, nil
	}
	resolved, found := p.secrets.Resolve(value)
	if !found || strings.TrimSpace(resolved) == "" {
		return "", fmt.Errorf("secret reference %q was not found", value)
	}
	return strings.TrimSpace(resolved), nil
}

func looksLikeSecretRef(value string) bool {
	return strings.HasPrefix(value, "$") || strings.HasPrefix(value, "env:")
}

func envelopeEncoding(payload string) string {
	enc, _ := splitEnvelopePayload(payload)
	return strings.ToLower(strings.TrimSpace(enc))
}

func boolConfig(cfg map[string]any, key string) bool {
	v, ok := cfg[key]
	if !ok {
		return false
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return strings.EqualFold(strings.TrimSpace(b), "true") || strings.TrimSpace(b) == "1"
	default:
		return false
	}
}
