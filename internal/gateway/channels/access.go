package channels

import "strings"

const AccessGroupAllowFromPrefix = "accessGroup:"

type DMAccessPolicy string

const (
	DMAccessPolicyDefault   DMAccessPolicy = ""
	DMAccessPolicyClosed    DMAccessPolicy = "closed"
	DMAccessPolicyAllowlist DMAccessPolicy = "allowlist"
	DMAccessPolicyOpen      DMAccessPolicy = "open"
)

type ImplicitMentionKind string

const (
	ImplicitMentionReplyToBot           ImplicitMentionKind = "reply_to_bot"
	ImplicitMentionQuotedBot            ImplicitMentionKind = "quoted_bot"
	ImplicitMentionBotThreadParticipant ImplicitMentionKind = "bot_thread_participant"
	ImplicitMentionNative               ImplicitMentionKind = "native"
)

type AccessMessage struct {
	ChannelID            string
	SenderID             string
	Text                 string
	IsDM                 bool
	IsGroup              bool
	CanDetectMention     bool
	WasMentioned         bool
	HasAnyMention        bool
	ImplicitMentionKinds []ImplicitMentionKind
	HasControlCommand    bool
	CommandAuthorized    bool
}

type AccessPolicy struct {
	AllowFrom                         []string
	GroupAllowFrom                    []string
	StoreAllowFrom                    []string
	GroupAllowFromFallbackToAllowFrom *bool
	DMPolicy                          DMAccessPolicy
	AllowWhenEmpty                    bool
	RequireMention                    bool
	AllowTextCommands                 bool
	AllowedImplicitMentionKinds       []ImplicitMentionKind
	AccessGroups                      map[string][]string
}

type AccessDecision struct {
	Allowed                     bool
	Reason                      string
	SenderAllowed               bool
	MentionAllowed              bool
	EffectiveWasMentioned       bool
	ImplicitMention             bool
	ShouldBypassMention         bool
	MatchedImplicitMentionKinds []ImplicitMentionKind
	EffectiveAllowFrom          []string
	EffectiveGroupAllowFrom     []string
}

func DecideAccess(msg AccessMessage, policy AccessPolicy) AccessDecision {
	effectiveAllowFrom, effectiveGroupAllowFrom := ResolveEffectiveAllowFrom(policy)
	allowEntries := effectiveAllowFrom
	if msg.IsGroup && !msg.IsDM {
		allowEntries = effectiveGroupAllowFrom
	}
	resolvedAllow := expandAccessGroups(allowEntries, policy.AccessGroups)
	senderAllowed := isSenderAllowed(resolvedAllow, msg.SenderID, policy.AllowWhenEmpty || (msg.IsDM && policy.DMPolicy == DMAccessPolicyOpen))
	mention := resolveMentionDecision(msg, policy)

	decision := AccessDecision{
		SenderAllowed:               senderAllowed,
		MentionAllowed:              !mention.shouldSkip,
		EffectiveWasMentioned:       mention.effectiveWasMentioned,
		ImplicitMention:             mention.implicitMention,
		ShouldBypassMention:         mention.shouldBypassMention,
		MatchedImplicitMentionKinds: append([]ImplicitMentionKind(nil), mention.matchedImplicitMentionKinds...),
		EffectiveAllowFrom:          effectiveAllowFrom,
		EffectiveGroupAllowFrom:     effectiveGroupAllowFrom,
	}
	if !senderAllowed {
		decision.Allowed = false
		decision.Reason = "sender_denied"
		return decision
	}
	if mention.shouldSkip {
		decision.Allowed = false
		decision.Reason = "mention_required"
		return decision
	}
	decision.Allowed = true
	decision.Reason = "allowed"
	return decision
}

func ResolveEffectiveAllowFrom(policy AccessPolicy) (effectiveAllowFrom, effectiveGroupAllowFrom []string) {
	store := policy.StoreAllowFrom
	if policy.DMPolicy == DMAccessPolicyAllowlist || policy.DMPolicy == DMAccessPolicyOpen {
		store = nil
	}
	effectiveAllowFrom = normalizeEntries(append(append([]string{}, policy.AllowFrom...), store...))
	fallback := true
	if policy.GroupAllowFromFallbackToAllowFrom != nil {
		fallback = *policy.GroupAllowFromFallbackToAllowFrom
	}
	if len(policy.GroupAllowFrom) > 0 {
		effectiveGroupAllowFrom = normalizeEntries(policy.GroupAllowFrom)
	} else if fallback {
		effectiveGroupAllowFrom = normalizeEntries(policy.AllowFrom)
	} else {
		effectiveGroupAllowFrom = nil
	}
	return effectiveAllowFrom, effectiveGroupAllowFrom
}

func ParseAccessGroupAllowFromEntry(entry string) string {
	trimmed := strings.TrimSpace(entry)
	if !strings.HasPrefix(trimmed, AccessGroupAllowFromPrefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, AccessGroupAllowFromPrefix))
}

type normalizedAllow struct {
	entries     []string
	hasWildcard bool
	hasEntries  bool
}

func expandAccessGroups(entries []string, groups map[string][]string) normalizedAllow {
	expanded := make([]string, 0, len(entries))
	for _, entry := range entries {
		if group := ParseAccessGroupAllowFromEntry(entry); group != "" {
			expanded = append(expanded, groups[group]...)
			continue
		}
		expanded = append(expanded, entry)
	}
	normalized := normalizeEntries(expanded)
	out := normalizedAllow{entries: normalized, hasEntries: len(normalized) > 0}
	for _, entry := range normalized {
		if entry == "*" {
			out.hasWildcard = true
			break
		}
	}
	return out
}

func isSenderAllowed(allow normalizedAllow, senderID string, allowWhenEmpty bool) bool {
	if !allow.hasEntries {
		return allowWhenEmpty
	}
	if allow.hasWildcard {
		return true
	}
	senderID = strings.TrimSpace(senderID)
	if senderID == "" {
		return false
	}
	for _, entry := range allow.entries {
		if entry == senderID {
			return true
		}
	}
	return false
}

func normalizeEntries(entries []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

type mentionDecision struct {
	effectiveWasMentioned       bool
	implicitMention             bool
	shouldBypassMention         bool
	shouldSkip                  bool
	matchedImplicitMentionKinds []ImplicitMentionKind
}

func resolveMentionDecision(msg AccessMessage, policy AccessPolicy) mentionDecision {
	matched := matchedImplicitMentionKinds(msg.ImplicitMentionKinds, policy.AllowedImplicitMentionKinds)
	implicit := len(matched) > 0
	bypass := msg.IsGroup && policy.RequireMention && !msg.WasMentioned && !msg.HasAnyMention && policy.AllowTextCommands && msg.CommandAuthorized && msg.HasControlCommand
	effective := msg.WasMentioned || implicit || bypass
	return mentionDecision{
		effectiveWasMentioned:       effective,
		implicitMention:             implicit,
		shouldBypassMention:         bypass,
		shouldSkip:                  policy.RequireMention && msg.CanDetectMention && !effective,
		matchedImplicitMentionKinds: matched,
	}
}

func matchedImplicitMentionKinds(input, allowed []ImplicitMentionKind) []ImplicitMentionKind {
	allowedSet := map[ImplicitMentionKind]struct{}{}
	if len(allowed) > 0 {
		for _, kind := range allowed {
			allowedSet[kind] = struct{}{}
		}
	}
	seen := map[ImplicitMentionKind]struct{}{}
	out := []ImplicitMentionKind{}
	for _, kind := range input {
		if len(allowedSet) > 0 {
			if _, ok := allowedSet[kind]; !ok {
				continue
			}
		}
		if _, ok := seen[kind]; ok {
			continue
		}
		seen[kind] = struct{}{}
		out = append(out, kind)
	}
	return out
}
