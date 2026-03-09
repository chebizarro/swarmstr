package irc

import (
	"strings"
	"testing"

	"swarmstr/internal/plugins/sdk"
)

// ── Line-splitting helpers ────────────────────────────────────────────────────

// splitPrivmsg mimics the line-splitting logic in sendPrivmsg.
func splitPrivmsg(target, text string) []string {
	prefix := "PRIVMSG " + target + " :"
	maxBody := ircMaxLineLen - len(prefix)
	var lines []string
	remaining := text
	for len(remaining) > 0 {
		chunk := remaining
		if len(chunk) > maxBody {
			split := maxBody
			for split > 0 && remaining[split] != ' ' {
				split--
			}
			if split == 0 {
				split = maxBody
			}
			chunk = remaining[:split]
			remaining = strings.TrimSpace(remaining[split:])
		} else {
			remaining = ""
		}
		lines = append(lines, prefix+chunk)
	}
	return lines
}

func TestSplitPrivmsg_Short(t *testing.T) {
	lines := splitPrivmsg("#test", "hello world")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d: %v", len(lines), lines)
	}
	if !strings.HasSuffix(lines[0], "hello world") {
		t.Fatalf("unexpected content: %q", lines[0])
	}
}

func TestSplitPrivmsg_Long(t *testing.T) {
	// Build a message longer than ircMaxLineLen.
	var sb strings.Builder
	for sb.Len() < ircMaxLineLen+100 {
		sb.WriteString("word ")
	}
	text := strings.TrimSpace(sb.String())
	lines := splitPrivmsg("#test", text)
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d", len(lines))
	}
	for i, l := range lines {
		if len(l) > ircMaxLineLen {
			t.Fatalf("line %d exceeds max length %d: len=%d", i, ircMaxLineLen, len(l))
		}
	}
}

func TestSplitPrivmsg_NoWordBoundary(t *testing.T) {
	// A single unbreakable token longer than maxBody must still split.
	prefix := "PRIVMSG #x :"
	maxBody := ircMaxLineLen - len(prefix)
	text := strings.Repeat("a", maxBody+10)
	lines := splitPrivmsg("#x", text)
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines for unbreakable token, got %d", len(lines))
	}
}

// ── parsePrefix ──────────────────────────────────────────────────────────────

func TestParsePrefix_Full(t *testing.T) {
	nick := parsePrefix(":alice!alice@host.example.com")
	if nick != "alice" {
		t.Fatalf("expected alice, got %q", nick)
	}
}

func TestParsePrefix_ServerOnly(t *testing.T) {
	nick := parsePrefix(":server.irc.net")
	if nick != "server.irc.net" {
		t.Fatalf("expected server.irc.net, got %q", nick)
	}
}

func TestParsePrefix_NoLeadingColon(t *testing.T) {
	nick := parsePrefix("bob!bob@host")
	if nick != "bob" {
		t.Fatalf("expected bob, got %q", nick)
	}
}

// ── handleLine unit tests ─────────────────────────────────────────────────────

func newBot(allowedNicks ...string) (*ircBot, *[]sdk.InboundChannelMessage) {
	var msgs []sdk.InboundChannelMessage
	allowed := map[string]bool{}
	for _, n := range allowedNicks {
		allowed[strings.ToLower(n)] = true
	}
	b := &ircBot{
		channelID:      "irc-main",
		nick:           "swarmbot",
		ircChannels:    []string{"#general"},
		allowedSenders: allowed,
		done:           make(chan struct{}),
	}
	b.onMessage = func(m sdk.InboundChannelMessage) {
		msgs = append(msgs, m)
	}
	return b, &msgs
}

func TestHandleLine_PING(t *testing.T) {
	b, _ := newBot()
	// Should not panic; send is a no-op when conn/writer is nil.
	joined := false
	b.handleLine("PING :server.example.com", &joined)
}

func TestHandleLine_Welcome_JoinsChannels(t *testing.T) {
	b, _ := newBot()
	// send is nil-safe (writer == nil), so this just tests no panic.
	joined := false
	b.handleLine(":server.irc.net 001 swarmbot :Welcome to IRC", &joined)
	if !joined {
		t.Fatal("expected joined=true after 001")
	}
}

func TestHandleLine_PrivmsgToChannel(t *testing.T) {
	b, msgs := newBot()
	joined := true
	b.handleLine(":alice!alice@host.example.com PRIVMSG #general :hello agent", &joined)
	if len(*msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(*msgs))
	}
	m := (*msgs)[0]
	if m.SenderID != "alice" {
		t.Fatalf("expected sender=alice, got %q", m.SenderID)
	}
	if m.Text != "hello agent" {
		t.Fatalf("expected text='hello agent', got %q", m.Text)
	}
	if m.ChannelID != "irc-main" {
		t.Fatalf("expected channelID=irc-main, got %q", m.ChannelID)
	}
}

func TestHandleLine_PrivmsgDirect(t *testing.T) {
	b, msgs := newBot()
	joined := true
	b.handleLine(":alice!alice@host.example.com PRIVMSG swarmbot :secret", &joined)
	if len(*msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(*msgs))
	}
	m := (*msgs)[0]
	if m.ChannelID != "irc-dm:alice" {
		t.Fatalf("expected DM channelID, got %q", m.ChannelID)
	}
}

func TestHandleLine_SkipOwnMessages(t *testing.T) {
	b, msgs := newBot()
	joined := true
	b.handleLine(":swarmbot!swarmbot@host.example.com PRIVMSG #general :I said this", &joined)
	if len(*msgs) != 0 {
		t.Fatalf("expected 0 messages (own message filtered), got %d", len(*msgs))
	}
}

func TestHandleLine_AllowedSendersFilter(t *testing.T) {
	b, msgs := newBot("alice")
	joined := true
	// alice is allowed
	b.handleLine(":alice!alice@host.example.com PRIVMSG #general :hi", &joined)
	// bob is not allowed
	b.handleLine(":bob!bob@host.example.com PRIVMSG #general :hi", &joined)
	if len(*msgs) != 1 {
		t.Fatalf("expected 1 message (only alice passes), got %d", len(*msgs))
	}
}

func TestHandleLine_AllowedSenders_Empty_AllowsAll(t *testing.T) {
	b, msgs := newBot() // no allowlist
	joined := true
	b.handleLine(":alice!alice@host.example.com PRIVMSG #general :hi", &joined)
	b.handleLine(":bob!bob@host.example.com PRIVMSG #general :yo", &joined)
	if len(*msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(*msgs))
	}
}

// ── Plugin metadata ───────────────────────────────────────────────────────────

func TestIRCPlugin_ID(t *testing.T) {
	p := &IRCPlugin{}
	if p.ID() != "irc" {
		t.Fatalf("expected irc, got %q", p.ID())
	}
}

func TestIRCPlugin_Type(t *testing.T) {
	p := &IRCPlugin{}
	if p.Type() != "IRC" {
		t.Fatalf("expected IRC, got %q", p.Type())
	}
}

func TestIRCPlugin_Capabilities(t *testing.T) {
	p := &IRCPlugin{}
	caps := p.Capabilities()
	if !caps.MultiAccount {
		t.Fatal("expected MultiAccount=true")
	}
}

func TestIRCPlugin_ConfigSchema(t *testing.T) {
	p := &IRCPlugin{}
	schema := p.ConfigSchema()
	if schema["type"] != "object" {
		t.Fatalf("expected schema type=object, got %v", schema["type"])
	}
	required, _ := schema["required"].([]string)
	requiredSet := map[string]bool{}
	for _, r := range required {
		requiredSet[r] = true
	}
	for _, field := range []string{"host", "nick", "channels"} {
		if !requiredSet[field] {
			t.Fatalf("expected %q in required fields", field)
		}
	}
}

func TestIRCPlugin_Connect_MissingHost(t *testing.T) {
	p := &IRCPlugin{}
	_, err := p.Connect(nil, "c1", map[string]any{"nick": "bot", "channels": []interface{}{"#ch"}}, nil) //nolint
	if err == nil {
		t.Fatal("expected error when host is missing")
	}
}

func TestIRCPlugin_Connect_MissingChannels(t *testing.T) {
	p := &IRCPlugin{}
	_, err := p.Connect(nil, "c1", map[string]any{"host": "irc.example.com", "nick": "bot"}, nil) //nolint
	if err == nil {
		t.Fatal("expected error when channels list is empty")
	}
}
