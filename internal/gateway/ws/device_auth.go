package ws

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"

	"swarmstr/internal/gateway/protocol"
)

func verifyDeviceSignatureForConnect(device *protocol.ConnectDevice, connect protocol.ConnectParams, role string, token string) bool {
	if device == nil {
		return false
	}
	payloadV3 := buildDeviceAuthPayloadV3(device, connect, role, token)
	if verifyDeviceSignature(device.PublicKey, payloadV3, device.Signature) {
		return true
	}
	payloadV2 := buildDeviceAuthPayloadV2(device, connect, role, token)
	return verifyDeviceSignature(device.PublicKey, payloadV2, device.Signature)
}

func buildDeviceAuthPayloadV2(device *protocol.ConnectDevice, connect protocol.ConnectParams, role string, token string) string {
	scopes := strings.Join(connect.Scopes, ",")
	return strings.Join([]string{
		"v2",
		strings.TrimSpace(device.ID),
		strings.TrimSpace(connect.Client.ID),
		strings.TrimSpace(connect.Client.Mode),
		strings.TrimSpace(role),
		scopes,
		fmt.Sprintf("%d", device.SignedAt),
		token,
		strings.TrimSpace(device.Nonce),
	}, "|")
}

func buildDeviceAuthPayloadV3(device *protocol.ConnectDevice, connect protocol.ConnectParams, role string, token string) string {
	scopes := strings.Join(connect.Scopes, ",")
	return strings.Join([]string{
		"v3",
		strings.TrimSpace(device.ID),
		strings.TrimSpace(connect.Client.ID),
		strings.TrimSpace(connect.Client.Mode),
		strings.TrimSpace(role),
		scopes,
		fmt.Sprintf("%d", device.SignedAt),
		token,
		strings.TrimSpace(device.Nonce),
		normalizeDeviceMetadataForAuth(connect.Client.Platform),
		normalizeDeviceMetadataForAuth(connect.Client.DeviceFamily),
	}, "|")
}

func normalizeDeviceMetadataForAuth(v string) string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return ""
	}
	out := make([]rune, 0, len(trimmed))
	for _, r := range trimmed {
		if r >= 'A' && r <= 'Z' {
			out = append(out, r+('a'-'A'))
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

func deriveDeviceIDFromPublicKey(publicKey string) (string, error) {
	raw, err := devicePublicKeyRawBytes(publicKey)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func verifyDeviceSignature(publicKey string, payload string, signature string) bool {
	pkRaw, err := devicePublicKeyRawBytes(publicKey)
	if err != nil {
		return false
	}
	if len(pkRaw) != ed25519.PublicKeySize {
		return false
	}
	sig, err := decodeSignature(signature)
	if err != nil {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pkRaw), []byte(payload), sig)
}

func devicePublicKeyRawBytes(publicKey string) ([]byte, error) {
	trimmed := strings.TrimSpace(publicKey)
	if trimmed == "" {
		return nil, fmt.Errorf("public key missing")
	}
	if strings.Contains(trimmed, "BEGIN") {
		block, _ := pem.Decode([]byte(trimmed))
		if block == nil {
			return nil, fmt.Errorf("invalid pem")
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		ed, ok := parsed.(ed25519.PublicKey)
		if !ok {
			return nil, fmt.Errorf("unsupported public key type")
		}
		return append([]byte(nil), ed...), nil
	}
	raw, err := decodeBase64URL(trimmed)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func decodeSignature(signature string) ([]byte, error) {
	s := strings.TrimSpace(signature)
	if s == "" {
		return nil, fmt.Errorf("signature missing")
	}
	if sig, err := decodeBase64URL(s); err == nil {
		return sig, nil
	}
	sig, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	return sig, nil
}

func decodeBase64URL(input string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(input); err == nil {
		return b, nil
	}
	if b, err := base64.URLEncoding.DecodeString(input); err == nil {
		return b, nil
	}
	return nil, fmt.Errorf("invalid base64url")
}
