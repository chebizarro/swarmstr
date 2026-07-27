package channels

// nostr_body.go builds the body presented to the agent for a NIP-29 group
// message, per the room's ambientPolicy (openclaw-nostr
// channel.ts:buildNostrGroupBodyForAgent). This is how "observe" (room_event /
// ambient) messages are surfaced: the agent still sees them, but wrapped with
// cautionary guidance so it only replies when appropriate — rather than being
// hard-dropped or answered like a direct request.

const (
	nostrBodyMentionsOther = "This NIP-29 group message mentions another participant, not you. Do not respond."
	nostrBodyScan          = "This NIP-29 group message does not mention you. Scan it before responding and only answer if a response would be appropriate."
	// NostrSenderIsBotNote is surfaced (as trusted per-turn context) when the
	// sender is a definitive automated agent, so the model can treat automated
	// senders differently. Loop-control only — never an authorization signal.
	NostrSenderIsBotNote = "The sender of this message is an automated agent (bot)."
)

// BuildNostrGroupBodyForAgent wraps the raw message text per the room's
// ambient policy. Explicit-mention / requireMention bodies pass through raw; a
// message tagging another participant is prefixed with a do-not-respond note;
// otherwise ambient messages get the scan wrapper (default) or pass through raw
// when ambientRespond is set.
func BuildNostrGroupBodyForAgent(text string, pf NostrPreflightResult, ambientRespond bool) string {
	if pf.RequireMention || pf.EffectiveWasMentioned {
		return text
	}
	if pf.HasAnyMention {
		return nostrBodyMentionsOther + "\n\n" + text
	}
	if ambientRespond {
		return text
	}
	return nostrBodyScan + "\n\n" + text
}
