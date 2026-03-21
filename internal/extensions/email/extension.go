// Package email implements an Email channel extension for metiq using IMAP
// (poll for inbound) and SMTP (send outbound).
//
// Registration: import _ "metiq/internal/extensions/email" in the daemon
// main.go to register this plugin at startup.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "imap_host": "imap.gmail.com:993",     // required: IMAP server
//	  "imap_user": "you@gmail.com",          // required
//	  "imap_pass": "app-password",           // required
//	  "smtp_host": "smtp.gmail.com:587",     // required: SMTP server
//	  "smtp_user": "you@gmail.com",          // optional: defaults to imap_user
//	  "smtp_pass": "app-password",           // optional: defaults to imap_pass
//	  "from_addr": "you@gmail.com",          // From: header for outbound
//	  "mailbox": "INBOX",                    // optional: default INBOX
//	  "poll_interval_s": 30,                 // optional: default 30s
//	  "allowed_senders": ["boss@corp.com"]   // optional: allow-list
//	}
package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
)

func init() {
	channels.RegisterChannelPlugin(&EmailPlugin{})
}

// EmailPlugin is the factory for Email channel instances.
type EmailPlugin struct{}

func (e *EmailPlugin) ID() string   { return "email" }
func (e *EmailPlugin) Type() string { return "Email (IMAP+SMTP)" }

func (e *EmailPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"imap_host":       "imap.example.com:993",
		"imap_user":       "user@example.com",
		"imap_pass":       "",
		"smtp_host":       "smtp.example.com:587",
		"smtp_user":       "",
		"smtp_pass":       "",
		"from_addr":       "user@example.com",
		"mailbox":         "INBOX",
		"poll_interval_s": 30,
		"allowed_senders": []string{},
	}
}

// Capabilities declares the features supported by the Email channel.
func (e *EmailPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{
		Typing:       false,
		Reactions:    false,
		Threads:      true, // email supports threading via In-Reply-To header
		Audio:        false,
		Edit:         false,
		MultiAccount: true,
	}
}

func (e *EmailPlugin) GatewayMethods() []sdk.GatewayMethod {
	return nil
}

func (e *EmailPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	imapHost := stringCfg(cfg, "imap_host", "")
	imapUser := stringCfg(cfg, "imap_user", "")
	imapPass := stringCfg(cfg, "imap_pass", "")
	smtpHost := stringCfg(cfg, "smtp_host", "")
	smtpUser := stringCfg(cfg, "smtp_user", imapUser)
	smtpPass := stringCfg(cfg, "smtp_pass", imapPass)
	fromAddr := stringCfg(cfg, "from_addr", imapUser)
	mailbox := stringCfg(cfg, "mailbox", "INBOX")
	if mailbox == "" {
		mailbox = "INBOX"
	}
	pollSeconds := intCfg(cfg, "poll_interval_s", 30)
	if pollSeconds < 5 {
		pollSeconds = 5
	}

	if imapHost == "" || imapUser == "" || imapPass == "" {
		return nil, fmt.Errorf("email plugin %s: imap_host, imap_user, imap_pass are required", channelID)
	}
	if smtpHost == "" {
		return nil, fmt.Errorf("email plugin %s: smtp_host is required", channelID)
	}

	var allowedSenders map[string]bool
	if senders, ok := cfg["allowed_senders"].([]any); ok && len(senders) > 0 {
		allowedSenders = map[string]bool{}
		for _, s := range senders {
			if addr, ok := s.(string); ok && addr != "" {
				allowedSenders[strings.ToLower(strings.TrimSpace(addr))] = true
			}
		}
	}

	b := &emailBot{
		channelID:      channelID,
		imapHost:       imapHost,
		imapUser:       imapUser,
		imapPass:       imapPass,
		smtpHost:       smtpHost,
		smtpUser:       smtpUser,
		smtpPass:       smtpPass,
		fromAddr:       fromAddr,
		mailbox:        mailbox,
		pollInterval:   time.Duration(pollSeconds) * time.Second,
		allowedSenders: allowedSenders,
		onMessage:      onMessage,
		seenUIDs:       map[string]bool{},
	}

	go b.poll(ctx)
	log.Printf("email channel %s connected (host=%s user=%s poll=%ds)", channelID, imapHost, imapUser, pollSeconds)
	return b, nil
}

// ─── emailBot ──────────────────────────────────────────────────────────────────

type emailBot struct {
	channelID      string
	imapHost       string
	imapUser       string
	imapPass       string
	smtpHost       string
	smtpUser       string
	smtpPass       string
	fromAddr       string
	mailbox        string
	pollInterval   time.Duration
	allowedSenders map[string]bool
	onMessage      func(sdk.InboundChannelMessage)

	mu       sync.Mutex
	seenUIDs map[string]bool
	closed   bool
	// lastReplyTo tracks the most recent sender's address for outbound reply.
	lastReplyTo string
}

func (b *emailBot) ID() string { return b.channelID }

// Send replies to the last seen sender using SMTP.
func (b *emailBot) Send(ctx context.Context, text string) error {
	b.mu.Lock()
	to := replyTargetFromContextOrLast(ctx, b.lastReplyTo)
	b.mu.Unlock()
	if to == "" {
		return fmt.Errorf("no recipient known yet for email channel %s", b.channelID)
	}
	return sendEmail(b.smtpHost, b.smtpUser, b.smtpPass, b.fromAddr, to,
		"Re: metiq reply", text)
}

// SendTo sends an email to an explicit recipient address.
func (b *emailBot) SendTo(ctx context.Context, to, subject, text string) error {
	return sendEmail(b.smtpHost, b.smtpUser, b.smtpPass, b.fromAddr, to, subject, text)
}

func (b *emailBot) Close() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	log.Printf("email channel %s closed", b.channelID)
}

// ─── ThreadHandle ────────────────────────────────────────────────────────────

// SendInThread replies to a specific email thread identified by threadID.
// threadID is used as the In-Reply-To and References header value.
func (b *emailBot) SendInThread(ctx context.Context, threadID, text string) error {
	b.mu.Lock()
	to := replyTargetFromContextOrLast(ctx, b.lastReplyTo)
	b.mu.Unlock()
	if to == "" {
		return fmt.Errorf("email channel %s: no recipient known for thread reply", b.channelID)
	}
	// Build a reply with In-Reply-To header referencing threadID.
	return sendEmailInThread(b.smtpHost, b.smtpUser, b.smtpPass, b.fromAddr, to,
		"Re: "+threadID, threadID, text)
}

// poll periodically checks the IMAP mailbox for new messages.
func (b *emailBot) poll(ctx context.Context) {
	ticker := time.NewTicker(b.pollInterval)
	defer ticker.Stop()
	// First check immediately.
	b.checkMail(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.mu.Lock()
			c := b.closed
			b.mu.Unlock()
			if c {
				return
			}
			b.checkMail(ctx)
		}
	}
}

// checkMail fetches unseen messages from the IMAP server using the IMAP4
// plain-text protocol (RFC 3501).  We implement a minimal subset over TLS:
// LOGIN → SELECT → SEARCH UNSEEN → FETCH headers+body → LOGOUT.
//
// Note: this is a lightweight implementation that works with standard IMAP
// servers (Gmail, Outlook, Fastmail).  For production use, consider a full
// IMAP library.
func (b *emailBot) checkMail(ctx context.Context) {
	msgs, err := imapFetchUnseen(ctx, b.imapHost, b.imapUser, b.imapPass, b.mailbox)
	if err != nil {
		log.Printf("email channel %s: IMAP error: %v", b.channelID, err)
		return
	}
	for _, m := range msgs {
		uid := m.uid
		b.mu.Lock()
		if b.seenUIDs[uid] {
			b.mu.Unlock()
			continue
		}
		b.seenUIDs[uid] = true
		b.mu.Unlock()

		from := strings.ToLower(strings.TrimSpace(m.from))
		if len(b.allowedSenders) > 0 && !b.allowedSenders[from] {
			log.Printf("email channel %s: skipping sender %s (not in allow-list)", b.channelID, from)
			continue
		}

		b.mu.Lock()
		b.lastReplyTo = m.from
		b.mu.Unlock()

		log.Printf("email channel %s: message from=%s subject=%q", b.channelID, from, m.subject)
		b.onMessage(sdk.InboundChannelMessage{
			ChannelID: b.channelID,
			SenderID:  m.from,
			Text:      strings.TrimSpace(m.subject + "\n" + m.body),
			EventID:   uid,
			CreatedAt: m.date.Unix(),
		})
	}
}

// ─── Minimal IMAP client ──────────────────────────────────────────────────────

type imapMessage struct {
	uid     string
	from    string
	subject string
	body    string
	date    time.Time
}

// imapFetchUnseen connects to the IMAP server over TLS and fetches unseen
// messages from mailbox.  Returns a slice of messages (possibly empty).
func imapFetchUnseen(ctx context.Context, host, user, pass, mailbox string) ([]imapMessage, error) {
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 15 * time.Second},
		Config:    &tls.Config{InsecureSkipVerify: false},
	}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("IMAP connect %s: %w", host, err)
	}
	defer conn.Close()

	ic := &imapConn{conn: conn, tag: 1}

	// Read greeting.
	if _, err := ic.readLine(); err != nil {
		return nil, fmt.Errorf("IMAP greeting: %w", err)
	}

	// LOGIN
	if _, err := ic.cmd(fmt.Sprintf("LOGIN %q %q", user, pass)); err != nil {
		return nil, fmt.Errorf("IMAP LOGIN: %w", err)
	}

	// SELECT mailbox
	if _, err := ic.cmd(fmt.Sprintf("SELECT %q", mailbox)); err != nil {
		return nil, fmt.Errorf("IMAP SELECT: %w", err)
	}

	// SEARCH UNSEEN
	resp, err := ic.cmd("SEARCH UNSEEN")
	if err != nil {
		return nil, fmt.Errorf("IMAP SEARCH: %w", err)
	}
	uids := parseSearchResult(resp)
	if len(uids) == 0 {
		_, _ = ic.cmd("LOGOUT")
		return nil, nil
	}

	// FETCH
	var msgs []imapMessage
	for _, uid := range uids {
		fetchResp, err := ic.cmd(fmt.Sprintf("FETCH %s (BODY.PEEK[])", uid))
		if err != nil {
			continue
		}
		msg := parseSimpleMessage(uid, fetchResp)
		msgs = append(msgs, msg)
	}

	_, _ = ic.cmd("LOGOUT")
	return msgs, nil
}

// imapConn is a minimal IMAP connection over a net.Conn.
type imapConn struct {
	conn io.ReadWriter
	tag  int
	buf  []byte
	pos  int
	end  int
}

func (c *imapConn) readLine() (string, error) {
	var sb strings.Builder
	tmp := make([]byte, 1)
	for {
		_, err := c.conn.Read(tmp)
		if err != nil {
			return sb.String(), err
		}
		if tmp[0] == '\n' {
			return strings.TrimRight(sb.String(), "\r"), nil
		}
		sb.WriteByte(tmp[0])
	}
}

func (c *imapConn) cmd(command string) ([]string, error) {
	tag := fmt.Sprintf("T%04d", c.tag)
	c.tag++
	line := tag + " " + command + "\r\n"
	if _, err := io.WriteString(c.conn, line); err != nil {
		return nil, err
	}
	var lines []string
	for {
		l, err := c.readLine()
		if err != nil {
			return lines, err
		}
		lines = append(lines, l)
		if strings.HasPrefix(l, tag+" OK") || strings.HasPrefix(l, tag+" NO") || strings.HasPrefix(l, tag+" BAD") {
			if strings.HasPrefix(l, tag+" NO") || strings.HasPrefix(l, tag+" BAD") {
				return lines, fmt.Errorf("IMAP error: %s", l)
			}
			return lines, nil
		}
	}
}

func parseSearchResult(lines []string) []string {
	for _, l := range lines {
		if strings.HasPrefix(l, "* SEARCH ") {
			parts := strings.Fields(strings.TrimPrefix(l, "* SEARCH "))
			return parts
		}
	}
	return nil
}

func parseSimpleMessage(uid string, lines []string) imapMessage {
	msg := imapMessage{uid: uid, date: time.Now()}
	body := strings.Join(lines, "\n")
	// Extract From:, Subject: headers from raw response.
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "from: ") {
			msg.from = strings.TrimSpace(line[6:])
		} else if strings.HasPrefix(lower, "subject: ") {
			msg.subject = strings.TrimSpace(line[9:])
		}
	}
	// Body: everything after the first blank line.
	idx := strings.Index(body, "\r\n\r\n")
	if idx < 0 {
		idx = strings.Index(body, "\n\n")
	}
	if idx >= 0 {
		msg.body = strings.TrimSpace(body[idx:])
	}
	return msg
}

// ─── SMTP send ────────────────────────────────────────────────────────────────

func sendEmailInThread(smtpHost, user, pass, from, to, subject, inReplyTo, body string) error {
	host, _, err := net.SplitHostPort(smtpHost)
	if err != nil {
		host = smtpHost
	}
	auth := smtp.PlainAuth("", user, pass, host)
	msg := buildEmailMessageInThread(from, to, subject, inReplyTo, body)
	return smtp.SendMail(smtpHost, auth, from, []string{to}, msg)
}

func buildEmailMessageInThread(from, to, subject, inReplyTo, body string) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: %s\r\n", to)
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	if inReplyTo != "" {
		fmt.Fprintf(&buf, "In-Reply-To: %s\r\n", inReplyTo)
		fmt.Fprintf(&buf, "References: %s\r\n", inReplyTo)
	}
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(&buf, "Content-Transfer-Encoding: base64\r\n")
	fmt.Fprintf(&buf, "\r\n")
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	for i := 0; i < len(encoded); i += 76 {
		end := i + 76
		if end > len(encoded) {
			end = len(encoded)
		}
		fmt.Fprintf(&buf, "%s\r\n", encoded[i:end])
	}
	return buf.Bytes()
}

func sendEmail(smtpHost, user, pass, from, to, subject, body string) error {
	host, _, err := net.SplitHostPort(smtpHost)
	if err != nil {
		host = smtpHost
	}

	auth := smtp.PlainAuth("", user, pass, host)
	msg := buildEmailMessage(from, to, subject, body)
	return smtp.SendMail(smtpHost, auth, from, []string{to}, msg)
}

func buildEmailMessage(from, to, subject, body string) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: %s\r\n", to)
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(&buf, "Content-Transfer-Encoding: base64\r\n")
	fmt.Fprintf(&buf, "\r\n")
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	// Split into 76-char lines per RFC 2045.
	for i := 0; i < len(encoded); i += 76 {
		end := i + 76
		if end > len(encoded) {
			end = len(encoded)
		}
		fmt.Fprintf(&buf, "%s\r\n", encoded[i:end])
	}
	return buf.Bytes()
}

func replyTargetFromContextOrLast(ctx context.Context, fallback string) string {
	target := strings.TrimSpace(sdk.ChannelReplyTarget(ctx))
	if target != "" {
		return target
	}
	return strings.TrimSpace(fallback)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func stringCfg(cfg map[string]any, key, def string) string {
	if v, ok := cfg[key].(string); ok && v != "" {
		return v
	}
	return def
}

func intCfg(cfg map[string]any, key string, def int) int {
	if v, ok := cfg[key].(float64); ok {
		return int(v)
	}
	if v, ok := cfg[key].(int); ok {
		return v
	}
	return def
}
