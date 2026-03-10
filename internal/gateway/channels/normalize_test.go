package channels

import (
	"testing"
)

func TestNormalizeInbound(t *testing.T) {
	cases := []struct {
		platform string
		botID    string
		input    string
		want     string
	}{
		// Slack
		{PlatformSlack, "U12345", "<@U12345> hello", "hello"},
		{PlatformSlack, "U12345", "<@U12345|bot> hello bot", "hello bot"},
		{PlatformSlack, "", "<@UOTHER> world", "world"},
		// Telegram
		{PlatformTelegram, "mybot", "@mybot hello", "hello"},
		{PlatformTelegram, "mybot", "@MyBot: hey", "hey"},
		{PlatformTelegram, "", "@anybot text", "@anybot text"},
		// Discord
		{PlatformDiscord, "123456789", "<@123456789> hi", "hi"},
		{PlatformDiscord, "123456789", "<@!123456789> hi", "hi"},
		{PlatformDiscord, "", "<@999> text", "text"},
		// Matrix
		{PlatformMatrix, "@bot:server.com", "@bot:server.com: hello", "hello"},
		{PlatformMatrix, "", "@user:server.com how are you", "how are you"},
		// Mattermost
		{PlatformMattermost, "mybot", "@mybot hello there", "hello there"},
		// IRC
		{PlatformIRC, "mybot", "mybot: help me", "help me"},
		{PlatformIRC, "mybot", "mybot, hello", "hello"},
		// Unknown platform — passthrough
		{"unknown", "bot", "  raw text  ", "raw text"},
		// Empty text
		{PlatformSlack, "U1", "", ""},
	}

	for _, tc := range cases {
		got := NormalizeInbound(tc.platform, tc.input, tc.botID)
		if got != tc.want {
			t.Errorf("platform=%q botID=%q input=%q: got %q want %q",
				tc.platform, tc.botID, tc.input, got, tc.want)
		}
	}
}
