// Package channels — outbound.go carries NIP-29 outbound-builder parity helpers
// (swarmstr-qa5c): the `previous` tag builder that anchors a new message to
// recent room events, and the generated-failure-reply matcher used to suppress
// error spew into rooms the agent was not directly addressed in.
package channels

import (
	"regexp"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"
)

// NIP29MaxPreviousRefs caps the number of recent-event references stamped on an
// outbound group message (NIP-29 "previous" tag).
const NIP29MaxPreviousRefs = 50

// nip29PreviousPrefixLen is the length of each referenced event-id prefix.
const nip29PreviousPrefixLen = 8

// nip29PublishTimeout bounds a group publish so a wedged relay cannot hang the
// send (openclaw publishTimeoutSeconds default).
const nip29PublishTimeout = 30 * time.Second

// BuildPreviousEventTag builds the NIP-29 `previous` tag from recent room event
// ids (most-recent last), using the first 8 chars of up to 50 ids and excluding
// duplicates. Returns nil when there is nothing to reference (best-effort: 0-2
// refs are acceptable on a cold session, >=3 recommended). Callers must have
// already excluded the bot's own event ids.
func BuildPreviousEventTag(recentIDs []string) nostr.Tag {
	start := 0
	if len(recentIDs) > NIP29MaxPreviousRefs {
		start = len(recentIDs) - NIP29MaxPreviousRefs
	}
	tag := nostr.Tag{"previous"}
	seen := make(map[string]struct{}, NIP29MaxPreviousRefs)
	for _, id := range recentIDs[start:] {
		id = strings.TrimSpace(id)
		if len(id) < nip29PreviousPrefixLen {
			continue
		}
		prefix := id[:nip29PreviousPrefixLen]
		if _, ok := seen[prefix]; ok {
			continue
		}
		seen[prefix] = struct{}{}
		tag = append(tag, prefix)
	}
	if len(tag) == 1 {
		return nil // no references available
	}
	return tag
}

// generatedFailureReplyPatterns matches operational/provider failure strings
// that must not be spewed into an ambient room (openclaw-nostr
// isGeneratedFailureReplyPayload).
var generatedFailureReplyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^⚠️\s*(?:Rate-limited\b|API rate limit reached\b|All models are temporarily rate-limited\b|Selected model is at capacity\b)`),
	regexp.MustCompile(`(?i)^⚠️\s*(?:[^\n]+ returned a billing error|API provider returned a billing error)\b`),
	regexp.MustCompile(`(?i)^The AI service is temporarily (?:rate-limited|overloaded)\b`),
	regexp.MustCompile(`(?i)^LLM request (?:rate limited\b|timed out\b|failed:|rejected:)`),
	regexp.MustCompile(`(?i)^Authentication (?:refresh|is missing|failed with)\b`),
	regexp.MustCompile(`(?i)^Browser OAuth (?:did not complete|returned an invalid)\b`),
	regexp.MustCompile(`(?i)^The provider returned an HTML error page\b`),
	regexp.MustCompile(`(?i)^Context overflow: prompt too large\b`),
	regexp.MustCompile(`(?i)^Reasoning is required for this model endpoint\b`),
	regexp.MustCompile(`(?i)^Message ordering conflict\b`),
	regexp.MustCompile(`(?i)^Session history (?:or replay state is invalid|looks corrupted)\b`),
	regexp.MustCompile(`(?i)^HTTP\s+\d{3}\b`),
	regexp.MustCompile(`(?i)^LLM streaming response contained a malformed fragment\b`),
}

// IsGeneratedFailureReplyPayload reports whether text is a generated
// operational-failure reply (rate limit / billing / timeout / auth / overflow /
// HTTP error). Such replies are suppressed for ambient room_event traffic and
// only delivered when the agent was directly addressed.
func IsGeneratedFailureReplyPayload(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	for _, re := range generatedFailureReplyPatterns {
		if re.MatchString(trimmed) {
			return true
		}
	}
	return false
}
