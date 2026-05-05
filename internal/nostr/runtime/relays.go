package runtime

import (
	"strings"

	nostr "fiatjaf.com/nostr"
)

func sanitizeRelayList(relays []string) []string {
	out := make([]string, 0, len(relays))
	seen := map[string]struct{}{}
	for _, relay := range relays {
		relay = normalizeRuntimeRelayURL(relay)
		if relay == "" {
			continue
		}
		if _, ok := seen[relay]; ok {
			continue
		}
		seen[relay] = struct{}{}
		out = append(out, relay)
	}
	return out
}

func normalizeRuntimeRelayURL(relay string) string {
	if strings.TrimSpace(relay) == "" {
		return ""
	}
	out := nostr.NormalizeURL(relay)
	if out == "" {
		return ""
	}
	if idx := strings.Index(out, "://"); idx >= 0 {
		scheme := strings.ToLower(out[:idx])
		rest := out[idx+3:]
		if rest == "" || strings.HasPrefix(rest, "/") || strings.HasPrefix(rest, "?") {
			return ""
		}
		out = scheme + "://" + rest
	}
	return out
}
