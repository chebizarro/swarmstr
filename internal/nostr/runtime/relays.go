package runtime

import "strings"

func sanitizeRelayList(relays []string) []string {
	out := make([]string, 0, len(relays))
	seen := map[string]struct{}{}
	for _, relay := range relays {
		relay = strings.TrimSpace(relay)
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
