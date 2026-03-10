// Package channels normalize.go — per-platform inbound text normalization.
package channels

import (
	"regexp"
	"strings"
)

// ─── Platform identifiers ─────────────────────────────────────────────────────

const (
	PlatformSlack      = "slack"
	PlatformTelegram   = "telegram"
	PlatformDiscord    = "discord"
	PlatformMatrix     = "matrix"
	PlatformWhatsApp   = "whatsapp"
	PlatformMattermost = "mattermost"
	PlatformIRC        = "irc"
	PlatformEmail      = "email"
	PlatformMSTeams    = "msteams"
	PlatformGoogleChat = "googlechat"
	PlatformSignal     = "signal"
)

// precompiled mention patterns (platform-specific).
var (
	// Slack: <@UXXXXXXX> or <@UXXXXXXX|displayname>
	slackMentionRe = regexp.MustCompile(`<@[A-Z0-9]+(?:\|[^>]*)?>`)

	// Discord: <@!XXXXXXX> or <@XXXXXXX>
	discordMentionRe = regexp.MustCompile(`<@!?[0-9]+>`)

	// Matrix mentions are typically the full displayname at word boundary, e.g.
	// "BotName: ..." or "@user:server" – we strip the @localpart:server prefix.
	matrixMentionRe = regexp.MustCompile(`@[A-Za-z0-9._\-]+:[A-Za-z0-9._\-]+`)
)

// NormalizeInbound strips platform-specific bot mention prefixes and whitespace
// from an inbound message before it reaches the agent.
//
// platform is one of the Platform* constants (e.g. "slack", "telegram", "discord").
// botID is the bot's platform-native identifier (used to match the mention).
// An empty botID is acceptable — the function will still strip generic patterns.
func NormalizeInbound(platform, text, botID string) string {
	switch strings.ToLower(platform) {
	case PlatformSlack:
		text = normalizeSlack(text, botID)
	case PlatformTelegram:
		text = normalizeTelegram(text, botID)
	case PlatformDiscord:
		text = normalizeDiscord(text, botID)
	case PlatformMatrix:
		text = normalizeMatrix(text, botID)
	case PlatformMattermost:
		text = normalizeMattermost(text, botID)
	case PlatformIRC:
		text = normalizeIRC(text, botID)
	}
	return strings.TrimSpace(text)
}

// normalizeSlack strips <@BOTID> and <@BOTID|name> mentions.
func normalizeSlack(text, botID string) string {
	if botID != "" {
		// Fast path: strip the specific bot mention.
		specific := regexp.MustCompile(`<@` + regexp.QuoteMeta(botID) + `(?:\|[^>]*)?>:?\s*`)
		text = specific.ReplaceAllString(text, "")
	}
	// Also strip any remaining generic mentions.
	text = slackMentionRe.ReplaceAllString(text, "")
	return text
}

// normalizeTelegram strips @botname mentions (case-insensitive).
func normalizeTelegram(text, botID string) string {
	if botID == "" {
		return text
	}
	// botID for Telegram is typically the @username without the @.
	re := regexp.MustCompile(`(?i)@` + regexp.QuoteMeta(botID) + `\b:?\s*`)
	text = re.ReplaceAllString(text, "")
	return text
}

// normalizeDiscord strips <@BOTID> and <@!BOTID> mentions.
func normalizeDiscord(text, botID string) string {
	if botID != "" {
		specific := regexp.MustCompile(`<@!?` + regexp.QuoteMeta(botID) + `>:?\s*`)
		text = specific.ReplaceAllString(text, "")
	}
	text = discordMentionRe.ReplaceAllString(text, "")
	return text
}

// normalizeMatrix strips @localpart:server mention prefixes.
func normalizeMatrix(text, botID string) string {
	if botID != "" {
		// Matrix botID is typically "@bot:server".
		re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(botID) + `:?\s*`)
		text = re.ReplaceAllString(text, "")
	}
	text = matrixMentionRe.ReplaceAllString(text, "")
	return text
}

// normalizeMattermost strips @username mentions (like Slack but simpler).
func normalizeMattermost(text, botID string) string {
	if botID == "" {
		return text
	}
	re := regexp.MustCompile(`(?i)@` + regexp.QuoteMeta(botID) + `\b:?\s*`)
	text = re.ReplaceAllString(text, "")
	return text
}

// normalizeIRC strips "BotName: " prefixes common in IRC direct messages.
func normalizeIRC(text, botID string) string {
	if botID == "" {
		return text
	}
	re := regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(botID) + `[:,]?\s*`)
	text = re.ReplaceAllString(text, "")
	return text
}
