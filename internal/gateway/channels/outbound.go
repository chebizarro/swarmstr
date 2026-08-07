// Package channels — outbound.go carries NIP-29 outbound-builder parity helpers
// (swarmstr-qa5c): the `previous` tag builder that anchors a new message to
// recent room events, and the generated-failure-reply matcher used to suppress
// error spew into rooms the agent was not directly addressed in.
package channels

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	nostr "fiatjaf.com/nostr"
)

// NIP29RoomDeletionKind is the NIP-29 relay-managed delete-event kind.
const NIP29RoomDeletionKind = nostr.Kind(9005)

var pureACKReactions = map[string]string{
	"got it":       "👍",
	"on it":        "✅",
	"ok":           "👍",
	"okay":         "👍",
	"ack":          "👍",
	"acknowledged": "👍",
	"thanks":       "👍",
	"thank you":    "👍",
	"will do":      "✅",
	"sounds good":  "👍",
}

// ClassifyPureACK reports whether text contains only a short acknowledgement
// (optionally preceded by one or more @mentions and followed by punctuation).
// The returned emoji is suitable for replacing the posted acknowledgement with
// a NIP-25 reaction. Any additional substantive content makes the match fail.
func ClassifyPureACK(text string) (string, bool) {
	fields := strings.Fields(strings.TrimSpace(text))
	for len(fields) > 0 && strings.HasPrefix(fields[0], "@") {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return "", false
	}
	body := strings.Join(fields, " ")
	if emoji, ok := classifyACKEmoji(body); ok {
		return emoji, true
	}
	emoji, ok := pureACKReactions[NormalizeEchoText(body)]
	return emoji, ok
}

func classifyACKEmoji(text string) (string, bool) {
	emoji := ""
	for _, r := range strings.TrimSpace(text) {
		switch {
		case r == '👍':
			emoji = "👍"
		case r == '✅' && emoji == "":
			emoji = "✅"
		case r == '\ufe0f' || r >= '\U0001F3FB' && r <= '\U0001F3FF':
			// Emoji presentation selector and skin-tone modifiers.
		case unicode.IsSpace(r) || strings.ContainsRune(".,!?;:()[]{}", r):
			// Cosmetic punctuation is allowed around an emoji-only ACK.
		default:
			return "", false
		}
	}
	return emoji, emoji != ""
}

// BuildNIP29ReactionEvent builds an unsigned NIP-29 room reaction (kind:7,
// NIP-25 shape + `h` tag + best-effort `previous` refs). Per NIP-25 the target
// author pubkey is required (emitted as the `p` tag); targetKind, when > 0, is
// emitted as the `k` tag. Empty content defaults to "+".
func BuildNIP29ReactionEvent(groupID, emoji, targetEventID, targetPubkey string, targetKind int, recentIDs []string) (nostr.Event, error) {
	if strings.TrimSpace(targetEventID) == "" {
		return nostr.Event{}, fmt.Errorf("nip29 reaction: target event id is required")
	}
	if strings.TrimSpace(targetPubkey) == "" {
		return nostr.Event{}, fmt.Errorf("nip29 reaction: target author pubkey is required")
	}
	if strings.TrimSpace(emoji) == "" {
		emoji = "+"
	}
	tags := nostr.Tags{{"h", groupID}}
	if prev := BuildPreviousEventTag(recentIDs); prev != nil {
		tags = append(tags, prev)
	}
	tags = append(tags, nostr.Tag{"e", targetEventID}, nostr.Tag{"p", targetPubkey})
	if targetKind > 0 {
		tags = append(tags, nostr.Tag{"k", strconv.Itoa(targetKind)})
	}
	return nostr.Event{
		Kind:      nostr.KindReaction,
		Content:   emoji,
		Tags:      tags,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
	}, nil
}

// BuildNIP29DeletionEvent builds an unsigned NIP-29 delete-event (kind:9005,
// `h` tag + best-effort `previous` refs + `["e", targetEventID]`). content is an
// optional human-readable reason.
func BuildNIP29DeletionEvent(groupID, targetEventID, reason string, recentIDs []string) (nostr.Event, error) {
	if strings.TrimSpace(targetEventID) == "" {
		return nostr.Event{}, fmt.Errorf("nip29 deletion: target event id is required")
	}
	tags := nostr.Tags{{"h", groupID}}
	if prev := BuildPreviousEventTag(recentIDs); prev != nil {
		tags = append(tags, prev)
	}
	tags = append(tags, nostr.Tag{"e", targetEventID})
	return nostr.Event{
		Kind:      NIP29RoomDeletionKind,
		Content:   reason,
		Tags:      tags,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
	}, nil
}

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
