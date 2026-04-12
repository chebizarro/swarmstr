package email

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"metiq/internal/plugins/sdk"
)

// ─── Plugin metadata ──────────────────────────────────────────────────────────

func TestPlugin_ID(t *testing.T) {
	p := &EmailPlugin{}
	if p.ID() != "email" {
		t.Errorf("ID: %q", p.ID())
	}
}

func TestPlugin_Type(t *testing.T) {
	p := &EmailPlugin{}
	if p.Type() == "" {
		t.Error("Type should not be empty")
	}
}

func TestPlugin_ConfigSchema(t *testing.T) {
	p := &EmailPlugin{}
	schema := p.ConfigSchema()
	if schema == nil {
		t.Fatal("expected non-nil schema")
	}
	// Schema uses example-value format, check required keys exist
	for _, key := range []string{"imap_host", "smtp_host", "from_addr", "mailbox"} {
		if _, exists := schema[key]; !exists {
			t.Errorf("missing key: %s", key)
		}
	}
}

func TestPlugin_Capabilities(t *testing.T) {
	p := &EmailPlugin{}
	caps := p.Capabilities()
	if !caps.Threads {
		t.Error("email should support threads")
	}
}

func TestPlugin_GatewayMethods(t *testing.T) {
	p := &EmailPlugin{}
	methods := p.GatewayMethods()
	// Email plugin currently returns nil (no extra gateway methods)
	if methods != nil {
		t.Errorf("expected nil gateway methods, got %d", len(methods))
	}
}

// ─── replyTargetFromContextOrLast ─────────────────────────────────────────────

func TestReplyTargetFromContextOrLast_ContextWins(t *testing.T) {
	ctx := sdk.WithChannelReplyTarget(context.Background(), "ctx-target")
	got := replyTargetFromContextOrLast(ctx, "fallback")
	if got != "ctx-target" {
		t.Errorf("expected ctx-target, got %q", got)
	}
}

func TestReplyTargetFromContextOrLast_FallbackUsed(t *testing.T) {
	got := replyTargetFromContextOrLast(context.Background(), "fallback")
	if got != "fallback" {
		t.Errorf("expected fallback, got %q", got)
	}
}

func TestReplyTargetFromContextOrLast_EmptyBoth(t *testing.T) {
	got := replyTargetFromContextOrLast(context.Background(), "")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestReplyTargetFromContextOrLast_WhitespaceContext(t *testing.T) {
	ctx := sdk.WithChannelReplyTarget(context.Background(), "  ")
	got := replyTargetFromContextOrLast(ctx, "fallback")
	// Whitespace-only should fall back
	if got != "fallback" {
		t.Errorf("expected fallback for whitespace, got %q", got)
	}
}

// ─── stringCfg / intCfg ──────────────────────────────────────────────────────

func TestStringCfg(t *testing.T) {
	cfg := map[string]any{
		"host":  "smtp.example.com",
		"empty": "",
		"num":   42,
	}
	if got := stringCfg(cfg, "host", "default"); got != "smtp.example.com" {
		t.Errorf("host: %q", got)
	}
	if got := stringCfg(cfg, "missing", "default"); got != "default" {
		t.Errorf("missing: %q", got)
	}
	if got := stringCfg(cfg, "empty", "default"); got != "default" {
		t.Errorf("empty string should use default: %q", got)
	}
	if got := stringCfg(cfg, "num", "default"); got != "default" {
		t.Errorf("non-string should use default: %q", got)
	}
}

func TestIntCfg(t *testing.T) {
	cfg := map[string]any{
		"port_float": float64(587),
		"port_int":   993,
		"str":        "nope",
	}
	if got := intCfg(cfg, "port_float", 25); got != 587 {
		t.Errorf("float64 port: %d", got)
	}
	if got := intCfg(cfg, "port_int", 25); got != 993 {
		t.Errorf("int port: %d", got)
	}
	if got := intCfg(cfg, "missing", 25); got != 25 {
		t.Errorf("missing: %d", got)
	}
	if got := intCfg(cfg, "str", 25); got != 25 {
		t.Errorf("non-number: %d", got)
	}
}

// ─── parseSearchResult ────────────────────────────────────────────────────────

func TestParseSearchResult_Normal(t *testing.T) {
	lines := []string{
		"* SEARCH 1 2 3",
		"A001 OK SEARCH completed",
	}
	uids := parseSearchResult(lines)
	if len(uids) != 3 {
		t.Fatalf("expected 3 UIDs, got %d: %v", len(uids), uids)
	}
	if uids[0] != "1" || uids[1] != "2" || uids[2] != "3" {
		t.Errorf("uids: %v", uids)
	}
}

func TestParseSearchResult_NoResults(t *testing.T) {
	lines := []string{
		"* SEARCH ",
		"A001 OK SEARCH completed",
	}
	uids := parseSearchResult(lines)
	if len(uids) != 0 {
		t.Errorf("expected empty, got: %v", uids)
	}
}

func TestParseSearchResult_NoSearchLine(t *testing.T) {
	lines := []string{"A001 OK SEARCH completed"}
	uids := parseSearchResult(lines)
	if uids != nil {
		t.Errorf("expected nil, got: %v", uids)
	}
}

// ─── parseSimpleMessage ───────────────────────────────────────────────────────

func TestParseSimpleMessage_Basic(t *testing.T) {
	lines := []string{
		"From: user@example.com",
		"Subject: Test Subject",
		"Content-Type: text/plain",
		"",
		"Hello, this is the body.",
	}
	msg := parseSimpleMessage("42", lines)
	if msg.uid != "42" {
		t.Errorf("uid: %q", msg.uid)
	}
	if msg.from != "user@example.com" {
		t.Errorf("from: %q", msg.from)
	}
	if msg.subject != "Test Subject" {
		t.Errorf("subject: %q", msg.subject)
	}
	if !strings.Contains(msg.body, "Hello, this is the body") {
		t.Errorf("body: %q", msg.body)
	}
}

func TestParseSimpleMessage_CaseInsensitiveHeaders(t *testing.T) {
	lines := []string{
		"from: Alice <alice@test.com>",
		"subject: RE: Hello",
		"",
		"Reply body",
	}
	msg := parseSimpleMessage("1", lines)
	if msg.from != "Alice <alice@test.com>" {
		t.Errorf("from: %q", msg.from)
	}
	if msg.subject != "RE: Hello" {
		t.Errorf("subject: %q", msg.subject)
	}
}

func TestParseSimpleMessage_NoBlankLine(t *testing.T) {
	lines := []string{
		"From: test@test.com",
		"Subject: No Body",
	}
	msg := parseSimpleMessage("1", lines)
	if msg.body != "" {
		t.Errorf("expected empty body, got: %q", msg.body)
	}
}

// ─── buildEmailMessage ────────────────────────────────────────────────────────

func TestBuildEmailMessage(t *testing.T) {
	msg := buildEmailMessage("from@test.com", "to@test.com", "Subject", "Hello World")
	s := string(msg)
	if !strings.Contains(s, "From: from@test.com") {
		t.Error("missing From header")
	}
	if !strings.Contains(s, "To: to@test.com") {
		t.Error("missing To header")
	}
	if !strings.Contains(s, "Subject: Subject") {
		t.Error("missing Subject header")
	}
	if !strings.Contains(s, "Content-Transfer-Encoding: base64") {
		t.Error("missing encoding header")
	}
	// Verify base64 body is decodable
	parts := strings.SplitN(s, "\r\n\r\n", 2)
	if len(parts) != 2 {
		t.Fatal("expected header/body separator")
	}
	b64 := strings.ReplaceAll(strings.TrimSpace(parts[1]), "\r\n", "")
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	if string(decoded) != "Hello World" {
		t.Errorf("decoded body: %q", string(decoded))
	}
}

func TestBuildEmailMessage_LongBody(t *testing.T) {
	body := strings.Repeat("x", 200)
	msg := buildEmailMessage("a@b.com", "c@d.com", "S", body)
	s := string(msg)
	// Base64 lines should be max 76 chars
	parts := strings.SplitN(s, "\r\n\r\n", 2)
	if len(parts) < 2 {
		t.Fatal("missing body")
	}
	for _, line := range strings.Split(strings.TrimSpace(parts[1]), "\r\n") {
		if len(line) > 76 {
			t.Errorf("line exceeds 76 chars: %d", len(line))
		}
	}
}

// ─── buildEmailMessageInThread ────────────────────────────────────────────────

func TestBuildEmailMessageInThread_HasReplyHeaders(t *testing.T) {
	msg := buildEmailMessageInThread("from@test.com", "to@test.com", "RE: Test", "<msg-id@test.com>", "Reply body")
	s := string(msg)
	if !strings.Contains(s, "In-Reply-To: <msg-id@test.com>") {
		t.Error("missing In-Reply-To header")
	}
	if !strings.Contains(s, "References: <msg-id@test.com>") {
		t.Error("missing References header")
	}
}

func TestBuildEmailMessageInThread_NoInReplyTo(t *testing.T) {
	msg := buildEmailMessageInThread("from@test.com", "to@test.com", "Test", "", "Body")
	s := string(msg)
	if strings.Contains(s, "In-Reply-To") {
		t.Error("should not have In-Reply-To when empty")
	}
}

// ─── emailBot metadata ────────────────────────────────────────────────────────

// ─── Connect validation ──────────────────────────────────────────────────────

func TestConnect_MissingIMAPHost(t *testing.T) {
	p := &EmailPlugin{}
	_, err := p.Connect(context.Background(), "e1", map[string]any{
		"imap_user": "user",
		"imap_pass": "pass",
		"smtp_host": "smtp.example.com",
	}, nil)
	if err == nil {
		t.Fatal("expected error when imap_host is missing")
	}
}

func TestConnect_MissingIMAPUser(t *testing.T) {
	p := &EmailPlugin{}
	_, err := p.Connect(context.Background(), "e1", map[string]any{
		"imap_host": "imap.example.com",
		"imap_pass": "pass",
		"smtp_host": "smtp.example.com",
	}, nil)
	if err == nil {
		t.Fatal("expected error when imap_user is missing")
	}
}

func TestConnect_MissingSMTPHost(t *testing.T) {
	p := &EmailPlugin{}
	_, err := p.Connect(context.Background(), "e1", map[string]any{
		"imap_host": "imap.example.com",
		"imap_user": "user",
		"imap_pass": "pass",
	}, nil)
	if err == nil {
		t.Fatal("expected error when smtp_host is missing")
	}
}

func TestConnect_ValidConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so poll loop exits
	p := &EmailPlugin{}
	h, err := p.Connect(ctx, "email-ch", map[string]any{
		"imap_host": "imap.example.com",
		"imap_user": "user",
		"imap_pass": "pass",
		"smtp_host": "smtp.example.com",
	}, func(sdk.InboundChannelMessage) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.ID() != "email-ch" {
		t.Fatalf("expected email-ch, got %s", h.ID())
	}
	h.Close()
}

func TestClose_Idempotent(t *testing.T) {
	b := &emailBot{channelID: "e1", seenUIDs: map[string]bool{}}
	b.Close()
	b.Close() // should not panic
}

func TestSend_NoRecipient(t *testing.T) {
	b := &emailBot{channelID: "e1", seenUIDs: map[string]bool{}}
	err := b.Send(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error when no recipient known")
	}
}

func TestSendInThread_NoRecipient(t *testing.T) {
	b := &emailBot{channelID: "e1", seenUIDs: map[string]bool{}}
	err := b.SendInThread(context.Background(), "<msg-id>", "hello")
	if err == nil {
		t.Fatal("expected error when no recipient known")
	}
}

// ─── emailBot metadata ────────────────────────────────────────────────────────

func TestEmailBot_ID(t *testing.T) {
	b := &emailBot{channelID: "email-1"}
	if b.ID() != "email-1" {
		t.Errorf("ID: %q", b.ID())
	}
}
