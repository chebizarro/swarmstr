package runtime

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// maxNIP04OutboundPlaintextRunes is the maximum runes per NIP-04 DM chunk.
// NIP-04 encryption (AES-CBC + base64) adds ~33% overhead, so 2800 chars
// encrypts to ~3700 bytes, safely under the common relay limit of 4096 bytes
// for encrypted content.
//
// This limit is NIP-04 specific and should NOT be applied to NIP-17 messages,
// which use NIP-44 encryption + NIP-59 gift-wrapping with different overhead.
const maxNIP04OutboundPlaintextRunes = 2800

// normalizeOutboundDMText performs basic outbound DM text hygiene:
// - trims leading/trailing whitespace
// - rejects empty messages
//
// This is appropriate for all outbound DM protocols. It does NOT enforce
// size limits, as those are transport-specific.
func normalizeOutboundDMText(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("dm text is empty")
	}
	return text, nil
}

// validateNIP04OutboundDMText validates text for NIP-04 outbound messages.
// It applies normalization and enforces the NIP-04-specific size limit.
//
// Use this ONLY for NIP-04 (kind:4) DM sends. Do NOT use for NIP-17/NIP-44.
func validateNIP04OutboundDMText(text string) (string, error) {
	text, err := normalizeOutboundDMText(text)
	if err != nil {
		return "", err
	}
	if utf8.RuneCountInString(text) > maxNIP04OutboundPlaintextRunes {
		return "", fmt.Errorf("dm text exceeds %d characters", maxNIP04OutboundPlaintextRunes)
	}
	return text, nil
}
