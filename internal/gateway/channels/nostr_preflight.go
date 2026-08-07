// Package channels — nostr_preflight.go ports openclaw-nostr
// channel.ts:resolveNostrGroupPreflight into Go as a pure, side-effect-free
// decision. It is the gating core: run at arrival-time (provisional admission
// gate) AND re-evaluated at execution against current config. It must never
// mutate stateful guards (bot-loop guard, breaker) — those record ONCE at
// execution only.
//
// It layers over the SDK-faithful mention decision (resolveMentionDecision in
// access.go) and adds the Nostr-specific facts: control commands, explicit +
// implicit mentions (implicit suppressed for peers), the allowBots known-bot
// gate, ambient-backfill drop, and the dropReason precedence.
package channels

import (
	"regexp"
	"strings"
	"unicode/utf8"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
)

// Inbound event kinds.
const (
	InboundEventUserRequest = "user_request"
	InboundEventRoomEvent   = "room_event"
)

// Drop reasons, in precedence order.
const (
	DropUnauthorizedControlCommand = "unauthorized_control_command"
	DropBotDisallowed              = "bot_disallowed"
	DropBotRequiresMention         = "bot_requires_mention"
	DropNoMention                  = "no_mention"
	DropBackfillAmbient            = "backfill_ambient"
	DropShouldReplyGate            = "should_reply_gate"
)

// NostrAllowBots is the per-room known-bot gate.
type NostrAllowBots string

const (
	AllowBotsOff      NostrAllowBots = "off"
	AllowBotsMentions NostrAllowBots = "mentions"
	AllowBotsAll      NostrAllowBots = "all"
)

// DeliveryPhaseBackfill marks events replayed from a reconnect/backfill burst.
const DeliveryPhaseBackfill = "backfill"

// ResolveNostrAllowBots normalizes a raw config value: "off"/false -> off,
// "all"/true -> all, anything else -> mentions (default).
func ResolveNostrAllowBots(raw any) NostrAllowBots {
	switch v := raw.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "off":
			return AllowBotsOff
		case "all":
			return AllowBotsAll
		case "mentions":
			return AllowBotsMentions
		}
	case bool:
		if v {
			return AllowBotsAll
		}
		return AllowBotsOff
	}
	return AllowBotsMentions
}

// NostrInboundMeta carries per-event facts the preflight needs.
type NostrInboundMeta struct {
	EventID           string
	ThreadRootEventID string
	// ReplyToEventID is the direct reply target ("e" reply tag), consumed by
	// the R2 takeover coordinator (a reply to a contested event stands a
	// pending takeover down).
	ReplyToEventID          string
	MentionedPubkeys        []string
	ReplyToSenderPubkey     string
	QuoteSenderPubkey       string
	ThreadHasBotParticipant bool
	// DeliveryPhase is "backfill" for replayed events, else live.
	DeliveryPhase string
}

// NostrPreflightInput is the pure input to ResolveNostrGroupPreflight.
type NostrPreflightInput struct {
	BotPubkey    string // hex
	ChannelName  string
	GroupID      string
	GroupAddress string // "<host>'<groupId>"; falls back to GroupID when empty
	SenderPubkey string // hex
	Text         string
	AgentID      string
	Meta         NostrInboundMeta

	RoomAllowFrom    []string
	AccountAllowFrom []string

	// RequireMention is the room config value; nil defaults to true.
	RequireMention *bool
	// AllowTextCommands defaults to true (resolved by the caller from cfg).
	AllowTextCommands bool
	// UnmentionedRoomEvent is true when the room's unmentionedInbound policy is
	// "room_event" (observe) rather than "user_request".
	UnmentionedRoomEvent bool
	// AllowBots gates known-bot senders (default "mentions").
	AllowBots NostrAllowBots
	// SenderIsPeer suppresses implicit mentions (verdict==true, or unknown only
	// when a room opts into peerDampingUnknownAsPeer). NEVER drives the hard gate.
	SenderIsPeer bool
	// SenderIsKnownBot drives the allowBots hard gate — set ONLY for a
	// definitive known bot (peer index verdict == true). Never for humans/unknown.
	SenderIsKnownBot bool

	// ShouldReplyGate enables the cheap ambient relevance gate. Runtime callers
	// pass the per-room policy (default true); false preserves legacy dispatch.
	ShouldReplyGate bool
	// ShouldReplyAliases names this routed agent/account. AgentID is included
	// automatically; callers may add profile/deployment aliases.
	ShouldReplyAliases []string
	// ShouldReplyCapabilities are locally declared capability/tool terms.
	ShouldReplyCapabilities []string
}

// NostrPreflightResult is the decision.
type NostrPreflightResult struct {
	RoomKey                     string
	RequireMention              bool
	AllowTextCommands           bool
	HasControlCommand           bool
	HasAbortRequest             bool
	CommandAuthorized           bool
	CommandName                 string
	ExplicitWasMentioned        bool
	EffectiveWasMentioned       bool
	HasAnyMention               bool
	ImplicitMentionKinds        []ImplicitMentionKind
	MatchedImplicitMentionKinds []ImplicitMentionKind
	ShouldDrop                  bool
	DropReason                  string
	InboundEventKind            string
	// ShouldReplyGateDecision is populated only for otherwise-admitted ambient
	// traffic. Mentions, replies-to-bot, commands, aborts, and trust drops bypass it.
	ShouldReplyGateDecision *ShouldReplyGateDecision
}

// NormalizeNostrRoomSessionKey mirrors normalizeNostrRoomSessionKey: strips all
// whitespace, lowercases, and prefixes "nostr:room:". This is the session +
// conversation id.
func NormalizeNostrRoomSessionKey(groupAddress string) string {
	var b strings.Builder
	b.Grow(len(groupAddress))
	for _, r := range groupAddress {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v' {
			continue
		}
		b.WriteRune(r)
	}
	return "nostr:room:" + strings.ToLower(b.String())
}

var abortRequestRe = regexp.MustCompile(`(?i)^(?:abort|cancel|stop)\b`)

func isNostrAbortRequestText(text string) bool {
	return abortRequestRe.MatchString(strings.TrimSpace(text))
}

func stripNostrURIPrefix(s string) string {
	if len(s) >= 6 && strings.EqualFold(s[:6], "nostr:") {
		return s[6:]
	}
	return s
}

// normalizeNostrPubkey canonicalizes an npub/hex (optionally nostr:-prefixed)
// pubkey to lowercase hex, or "" if it is not a valid pubkey.
func normalizeNostrPubkey(value string) string {
	t := stripNostrURIPrefix(strings.TrimSpace(value))
	if t == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(t), "npub1") {
		prefix, val, err := nip19.Decode(t)
		if err != nil || prefix != "npub" {
			return ""
		}
		pk, ok := val.(nostr.PubKey)
		if !ok {
			return ""
		}
		return pk.Hex()
	}
	pk, err := nostr.PubKeyFromHex(t)
	if err != nil {
		return ""
	}
	return pk.Hex()
}

var (
	npubTokenRe = regexp.MustCompile(`(?:[nN][oO][sS][tT][rR]:)?npub1[023456789acdefghjklmnpqrstuvwxyz]{58}`)
	hexTokenRe  = regexp.MustCompile(`(?:[nN][oO][sS][tT][rR]:)?[0-9a-fA-F]{64}`)
)

func isLowerAlnum(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z')
}

func isHexRune(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// findBoundedTokens returns regex matches whose immediately preceding and
// following runes are NOT in the boundary class (mirroring the reference's
// (?:^|[^…]) / (?=$|[^…]) guards, which RE2 cannot express directly).
func findBoundedTokens(text string, re *regexp.Regexp, boundary func(rune) bool) []string {
	var out []string
	for _, loc := range re.FindAllStringIndex(text, -1) {
		start, end := loc[0], loc[1]
		if start > 0 {
			if r, _ := utf8.DecodeLastRuneInString(text[:start]); boundary(r) {
				continue
			}
		}
		if end < len(text) {
			if r, _ := utf8.DecodeRuneInString(text[end:]); boundary(r) {
				continue
			}
		}
		out = append(out, text[start:end])
	}
	return out
}

// extractNostrMentionPubkeys pulls npub… / nostr:npub… / bare 64-hex mentions
// from message text, returning normalized lowercase-hex pubkeys.
func extractNostrMentionPubkeys(text string) []string {
	set := map[string]struct{}{}
	for _, tok := range findBoundedTokens(text, npubTokenRe, isLowerAlnum) {
		if pk := normalizeNostrPubkey(tok); pk != "" {
			set[pk] = struct{}{}
		}
	}
	for _, tok := range findBoundedTokens(text, hexTokenRe, isHexRune) {
		if pk := normalizeNostrPubkey(tok); pk != "" {
			set[pk] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for pk := range set {
		out = append(out, pk)
	}
	return out
}

var nostrControlCommandNames = map[string]struct{}{
	"help": {}, "commands": {}, "tools": {}, "status": {}, "diagnostics": {},
	"tasks": {}, "context": {}, "export-session": {}, "export": {},
	"export-trajectory": {}, "trajectory": {}, "whoami": {}, "model": {},
}

// extractNostrTextCommandName returns the command name for a "/name…" message,
// or "" when the text is not a slash command.
func extractNostrTextCommandName(text string) string {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if !strings.HasPrefix(trimmed, "/") {
		return ""
	}
	token := trimmed
	if i := strings.IndexFunc(trimmed, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v'
	}); i >= 0 {
		token = trimmed[:i]
	}
	token = strings.TrimSuffix(token, ":")
	if len(token) < 2 {
		return ""
	}
	name := token[1:] // drop leading "/"
	if i := strings.IndexByte(name, '@'); i >= 0 {
		name = name[:i]
	}
	return name
}

func hasNostrControlCommand(text string, allowTextCommands bool) (bool, string) {
	if !allowTextCommands {
		return false, ""
	}
	name := extractNostrTextCommandName(text)
	if name == "" {
		return false, ""
	}
	if _, ok := nostrControlCommandNames[name]; ok {
		return true, name
	}
	return false, ""
}

// allowListExplicitlyIncludes reports whether allowFrom explicitly names sender.
// The wildcard "*" does NOT authorize commands.
func allowListExplicitlyIncludes(allowFrom []string, normalizedSender string) bool {
	if normalizedSender == "" {
		return false
	}
	for _, entry := range allowFrom {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" || trimmed == "*" {
			continue
		}
		if normalizeNostrPubkey(trimmed) == normalizedSender {
			return true
		}
	}
	return false
}

func resolveNostrImplicitMentionKinds(meta NostrInboundMeta, botPubkey string, senderIsPeer bool) []ImplicitMentionKind {
	// Implicit mentions keep a bot engaged in a HUMAN thread; between agents they
	// only feed ping-pong, so a peer author suppresses all of them.
	if senderIsPeer {
		return nil
	}
	nb := normalizeNostrPubkey(botPubkey)
	if nb == "" {
		return nil
	}
	var kinds []ImplicitMentionKind
	if normalizeNostrPubkey(meta.ReplyToSenderPubkey) == nb {
		kinds = append(kinds, ImplicitMentionReplyToBot)
	}
	if normalizeNostrPubkey(meta.QuoteSenderPubkey) == nb {
		kinds = append(kinds, ImplicitMentionQuotedBot)
	}
	if meta.ThreadHasBotParticipant {
		kinds = append(kinds, ImplicitMentionBotThreadParticipant)
	}
	return kinds
}

func resolveNostrMentionFacts(botPubkey, text string, mentionedPubkeys []string) (explicit, hasAny bool) {
	nb := normalizeNostrPubkey(botPubkey)
	set := map[string]struct{}{}
	for _, e := range mentionedPubkeys {
		if n := normalizeNostrPubkey(e); n != "" {
			set[n] = struct{}{}
		}
	}
	for _, e := range extractNostrMentionPubkeys(text) {
		set[e] = struct{}{}
	}
	if nb != "" {
		_, explicit = set[nb]
	}
	return explicit, len(set) > 0
}

// ResolveNostrGroupPreflight computes the gating decision. Pure and
// side-effect-free.
func ResolveNostrGroupPreflight(in NostrPreflightInput) NostrPreflightResult {
	groupAddress := in.GroupAddress
	if strings.TrimSpace(groupAddress) == "" {
		groupAddress = in.GroupID
	}
	roomKey := NormalizeNostrRoomSessionKey(groupAddress)

	requireMention := true
	if in.RequireMention != nil {
		requireMention = *in.RequireMention
	}

	hasCommand, commandName := hasNostrControlCommand(in.Text, in.AllowTextCommands)
	hasAbort := isNostrAbortRequestText(in.Text)

	normalizedSender := normalizeNostrPubkey(in.SenderPubkey)
	commandAuthorized := hasCommand &&
		(allowListExplicitlyIncludes(in.RoomAllowFrom, normalizedSender) ||
			allowListExplicitlyIncludes(in.AccountAllowFrom, normalizedSender))

	explicitWasMentioned, hasAnyMention := resolveNostrMentionFacts(in.BotPubkey, in.Text, in.Meta.MentionedPubkeys)
	implicitKinds := resolveNostrImplicitMentionKinds(in.Meta, in.BotPubkey, in.SenderIsPeer)

	// Reuse the SDK-faithful mention decision (access.go).
	md := resolveMentionDecision(
		AccessMessage{
			IsGroup:              true,
			CanDetectMention:     true,
			WasMentioned:         explicitWasMentioned,
			HasAnyMention:        hasAnyMention,
			ImplicitMentionKinds: implicitKinds,
			HasControlCommand:    hasCommand,
			CommandAuthorized:    commandAuthorized,
		},
		AccessPolicy{
			RequireMention:    requireMention,
			AllowTextCommands: in.AllowTextCommands,
		},
	)

	inboundEventKind := classifyNostrGroupInboundEvent(in.UnmentionedRoomEvent, md.effectiveWasMentioned, hasCommand, hasAbort)

	isAmbientBackfill := in.Meta.DeliveryPhase == DeliveryPhaseBackfill &&
		!md.effectiveWasMentioned && !hasCommand && !hasAbort

	// allowBots gate — definitive KNOWN bots only.
	allowBots := in.AllowBots
	if allowBots == "" {
		allowBots = AllowBotsMentions
	}
	botGateDrop := ""
	if in.SenderIsKnownBot {
		switch allowBots {
		case AllowBotsOff:
			botGateDrop = DropBotDisallowed
		case AllowBotsMentions:
			if !explicitWasMentioned {
				botGateDrop = DropBotRequiresMention
			}
		}
	}

	dropReason := ""
	switch {
	case hasCommand && !commandAuthorized:
		dropReason = DropUnauthorizedControlCommand
	case botGateDrop != "":
		dropReason = botGateDrop
	case md.shouldSkip:
		dropReason = DropNoMention
	case isAmbientBackfill:
		dropReason = DropBackfillAmbient
	}

	var shouldReplyDecision *ShouldReplyGateDecision
	// Relevance is strictly downstream of trust/command/mention decisions. It
	// may shed only otherwise-admitted ambient traffic; it can never turn a
	// rejection into an admission or alter DMs (which do not use this preflight).
	if dropReason == "" && !md.effectiveWasMentioned && !hasCommand && !hasAbort {
		aliases := append([]string(nil), in.ShouldReplyAliases...)
		aliases = append(aliases, in.AgentID)
		decision := ResolveShouldReplyGate(ShouldReplyGateInput{
			Text:             in.Text,
			Aliases:          aliases,
			Capabilities:     in.ShouldReplyCapabilities,
			SenderIsKnownBot: in.SenderIsKnownBot,
			Enabled:          in.ShouldReplyGate,
		}, nil)
		shouldReplyDecision = &decision
		if decision.Outcome != ShouldReplyPass {
			dropReason = DropShouldReplyGate
		}
	}

	return NostrPreflightResult{
		RoomKey:                     roomKey,
		RequireMention:              requireMention,
		AllowTextCommands:           in.AllowTextCommands,
		HasControlCommand:           hasCommand,
		HasAbortRequest:             hasAbort,
		CommandAuthorized:           commandAuthorized,
		CommandName:                 commandName,
		ExplicitWasMentioned:        explicitWasMentioned,
		EffectiveWasMentioned:       md.effectiveWasMentioned,
		HasAnyMention:               hasAnyMention,
		ImplicitMentionKinds:        implicitKinds,
		MatchedImplicitMentionKinds: md.matchedImplicitMentionKinds,
		ShouldDrop:                  dropReason != "",
		DropReason:                  dropReason,
		InboundEventKind:            inboundEventKind,
		ShouldReplyGateDecision:     shouldReplyDecision,
	}
}

func classifyNostrGroupInboundEvent(unmentionedRoomEvent, effectiveWasMentioned, hasControlCommand, hasAbortRequest bool) string {
	if !unmentionedRoomEvent {
		return InboundEventUserRequest
	}
	if effectiveWasMentioned || hasControlCommand || hasAbortRequest {
		return InboundEventUserRequest
	}
	return InboundEventRoomEvent
}
