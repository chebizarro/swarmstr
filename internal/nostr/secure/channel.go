package secure

import (
	"context"
	"fmt"

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
// If there is no colon, the whole payload is treated as enc="" (plaintext passthrough).
func parseAndDecrypt(codec EnvelopeCodec, payload string) (string, error) {
	enc := ""
	ciphertext := payload
	for i := 0; i < len(payload); i++ {
		if payload[i] == ':' {
			enc = payload[:i]
			ciphertext = payload[i+1:]
			break
		}
	}
	return codec.Decrypt(ciphertext, enc)
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
}

// NewEncryptedChannelPlugin wraps plugin with transparent NIP-44 encryption.
func NewEncryptedChannelPlugin(plugin sdk.ChannelPlugin) *EncryptedChannelPlugin {
	return &EncryptedChannelPlugin{ChannelPlugin: plugin}
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

	// Wrap onMessage to decrypt inbound payloads if encryption is enabled.
	wrappedOnMessage := onMessage
	var codec *NIP44PeerCodec
	if privKey != "" && peerPubKey != "" {
		var err error
		codec, err = NewNIP44PeerCodec(privKey, peerPubKey)
		if err != nil {
			return nil, fmt.Errorf("encrypted channel %q: init codec: %w", channelID, err)
		}
		wrappedOnMessage = func(msg sdk.InboundChannelMessage) {
			// Try to decrypt; fall back to plaintext on failure.
			// Payload is expected as "<enc>:<ciphertext>" (same format Send() produces).
			if plain, decErr := parseAndDecrypt(codec, msg.Text); decErr == nil {
				msg.Text = plain
			}
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
