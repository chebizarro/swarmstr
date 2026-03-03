package state

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func protectedTagValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(raw))
	// keep tags shorter while preserving good collision resistance for operational use.
	return "h:" + hex.EncodeToString(sum[:16])
}
