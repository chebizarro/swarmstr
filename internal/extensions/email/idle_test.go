package email

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"metiq/internal/plugins/sdk"
)

// fakeIMAPServer speaks just enough of the IMAP4rev1 + IDLE (RFC 2177) protocol
// to drive idleCycle: it advertises IDLE (unless idleSupported is false), and on
// the first IDLE pushes an untagged EXISTS so the client fetches and delivers
// one message.
func fakeIMAPServer(ln net.Listener, idleSupported bool) {
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()

	r := bufio.NewReader(conn)
	write := func(s string) { _, _ = io.WriteString(conn, s) }

	write("* OK IMAP4rev1 fake ready\r\n")

	searchCount := 0
	idleCount := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		fields := strings.SplitN(line, " ", 3)
		if len(fields) < 2 {
			continue
		}
		tag := fields[0]
		switch strings.ToUpper(fields[1]) {
		case "LOGIN":
			write(tag + " OK LOGIN completed\r\n")
		case "CAPABILITY":
			if idleSupported {
				write("* CAPABILITY IMAP4rev1 IDLE\r\n")
			} else {
				write("* CAPABILITY IMAP4rev1\r\n")
			}
			write(tag + " OK CAPABILITY completed\r\n")
		case "SELECT":
			write("* 0 EXISTS\r\n")
			write(tag + " OK [READ-WRITE] SELECT completed\r\n")
		case "SEARCH":
			searchCount++
			if searchCount == 1 {
				write("* SEARCH\r\n") // nothing unseen at startup
			} else {
				write("* SEARCH 1\r\n")
			}
			write(tag + " OK SEARCH completed\r\n")
		case "FETCH":
			write("* 1 FETCH (BODY[]\r\n")
			write("From: sender@example.com\r\n")
			write("Subject: Hello IDLE\r\n")
			write("Message-ID: <msg-1@example.com>\r\n")
			write("\r\n")
			write("This is the body.\r\n")
			write(")\r\n")
			write(tag + " OK FETCH completed\r\n")
		case "IDLE":
			write("+ idling\r\n")
			idleCount++
			if idleCount == 1 {
				write("* 1 EXISTS\r\n") // push a new-mail notification
			}
			// Wait for the client's DONE (or connection close).
			for {
				l, rerr := r.ReadString('\n')
				if rerr != nil {
					return
				}
				if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(l)), "DONE") {
					break
				}
			}
			write(tag + " OK IDLE terminated\r\n")
		case "LOGOUT":
			write("* BYE\r\n")
			write(tag + " OK LOGOUT completed\r\n")
			return
		default:
			write(tag + " OK completed\r\n")
		}
	}
}

// TestIMAPIdleDeliversOnExists asserts that with an IDLE-capable server, a
// pushed EXISTS notification results in an event-driven delivery.
func TestIMAPIdleDeliversOnExists(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	orig := imapDialContext
	imapDialContext = func(ctx context.Context, host string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", ln.Addr().String())
	}
	defer func() { imapDialContext = orig }()

	go fakeIMAPServer(ln, true)

	delivered := make(chan sdk.InboundChannelMessage, 1)
	b := &emailBot{
		channelID: "email-ch",
		imapHost:  "fake:993",
		imapUser:  "user@example.com",
		imapPass:  "pass",
		mailbox:   "INBOX",
		seenUIDs:  map[string]bool{},
		onMessage: func(m sdk.InboundChannelMessage) { delivered <- m },
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		_, _ = b.idleCycle(ctx)
		close(done)
	}()

	select {
	case m := <-delivered:
		if !strings.Contains(m.Text, "Hello IDLE") {
			t.Fatalf("unexpected delivered text %q", m.Text)
		}
		if !strings.Contains(strings.ToLower(m.SenderID), "sender@example.com") {
			t.Fatalf("unexpected sender %q", m.SenderID)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("IMAP IDLE did not deliver the pushed message")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("idleCycle did not stop after context cancellation")
	}
}

// TestIMAPIdleUnsupportedFallsBack asserts that when the server does not
// advertise IDLE, idleCycle reports unsupported so run() falls back to polling.
func TestIMAPIdleUnsupportedFallsBack(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	orig := imapDialContext
	imapDialContext = func(ctx context.Context, host string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", ln.Addr().String())
	}
	defer func() { imapDialContext = orig }()

	go fakeIMAPServer(ln, false)

	b := &emailBot{
		channelID: "email-ch",
		imapHost:  "fake:993",
		imapUser:  "user@example.com",
		imapPass:  "pass",
		mailbox:   "INBOX",
		seenUIDs:  map[string]bool{},
		onMessage: func(m sdk.InboundChannelMessage) {},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	unsupported, err := b.idleCycle(ctx)
	if err != nil {
		t.Fatalf("idleCycle: %v", err)
	}
	if !unsupported {
		t.Fatal("expected idleCycle to report IDLE unsupported")
	}
}
