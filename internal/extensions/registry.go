// Package extensions provides a central registry of all built-in channel
// plugin constructors.  Instead of each extension self-registering via init(),
// callers use RegisterConfigured to register only the plugins that match
// entries in the live config — avoiding noisy startup logs and unnecessary
// allocations when no channels are configured.
package extensions

import (
	"metiq/internal/extensions/bluebubbles"
	"metiq/internal/extensions/discord"
	"metiq/internal/extensions/email"
	"metiq/internal/extensions/feishu"
	"metiq/internal/extensions/googlechat"
	"metiq/internal/extensions/irc"
	"metiq/internal/extensions/line"
	"metiq/internal/extensions/matrix"
	"metiq/internal/extensions/mattermost"
	"metiq/internal/extensions/msteams"
	"metiq/internal/extensions/nextcloud"
	"metiq/internal/extensions/signal"
	"metiq/internal/extensions/slack"
	"metiq/internal/extensions/synology"
	"metiq/internal/extensions/telegram"
	"metiq/internal/extensions/twitch"
	"metiq/internal/extensions/whatsapp"
	"metiq/internal/extensions/zalo"

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
	"metiq/internal/store/state"
)

// constructors maps plugin kind IDs to factory functions that create a fresh
// ChannelPlugin instance.  The factories are invoked lazily — only when the
// kind appears in the live config.
var constructors = map[string]func() sdk.ChannelPlugin{
	"bluebubbles":   func() sdk.ChannelPlugin { return &bluebubbles.BlueBubblesPlugin{} },
	"discord":       func() sdk.ChannelPlugin { return &discord.DiscordPlugin{} },
	"email":         func() sdk.ChannelPlugin { return &email.EmailPlugin{} },
	"feishu":        func() sdk.ChannelPlugin { return &feishu.FeishuPlugin{} },
	"googlechat":    func() sdk.ChannelPlugin { return &googlechat.GoogleChatPlugin{} },
	"irc":           func() sdk.ChannelPlugin { return &irc.IRCPlugin{} },
	"line":          func() sdk.ChannelPlugin { return &line.LINEPlugin{} },
	"matrix":        func() sdk.ChannelPlugin { return &matrix.MatrixPlugin{} },
	"mattermost":    func() sdk.ChannelPlugin { return &mattermost.MattermostPlugin{} },
	"msteams":       func() sdk.ChannelPlugin { return &msteams.MSTeamsPlugin{} },
	"nextcloud-talk": func() sdk.ChannelPlugin { return &nextcloud.NextcloudPlugin{} },
	"signal":        func() sdk.ChannelPlugin { return &signal.SignalPlugin{} },
	"slack":         func() sdk.ChannelPlugin { return &slack.SlackPlugin{} },
	"synology-chat": func() sdk.ChannelPlugin { return &synology.SynologyPlugin{} },
	"telegram":      func() sdk.ChannelPlugin { return &telegram.TelegramPlugin{} },
	"twitch":        func() sdk.ChannelPlugin { return &twitch.TwitchPlugin{} },
	"whatsapp":      func() sdk.ChannelPlugin { return &whatsapp.WhatsAppPlugin{} },
	"zalo":          func() sdk.ChannelPlugin { return &zalo.ZaloPlugin{} },
}

// RegisterConfigured inspects the config's NostrChannels and registers only
// the channel plugins whose kind matches a configured entry.  Returns the
// number of plugins registered.
func RegisterConfigured(cfg state.ConfigDoc) int {
	// Collect unique kinds referenced by the config.
	needed := map[string]struct{}{}
	for _, ch := range cfg.NostrChannels {
		if ch.Kind != "" {
			needed[ch.Kind] = struct{}{}
		}
	}

	registered := 0
	for kind := range needed {
		ctor, ok := constructors[kind]
		if !ok {
			continue // not a built-in extension (may be a JS plugin)
		}
		channels.RegisterChannelPlugin(ctor())
		registered++
	}
	return registered
}
