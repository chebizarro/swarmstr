package export_test

import (
	"strings"
	"testing"
	"time"

	"metiq/internal/export"
)

func TestSessionToHTMLBasic(t *testing.T) {
	msgs := []export.Message{
		{Role: "user", Content: "Hello, agent!", Timestamp: 1700000000, ID: "msg-1"},
		{Role: "assistant", Content: "Hello! How can I help?", Timestamp: 1700000010, ID: "msg-2"},
	}
	html, err := export.SessionToHTML(export.SessionHTMLOptions{
		SessionID:  "test-session",
		AgentID:    "main",
		AgentName:  "TestAgent",
		Messages:   msgs,
		ExportedAt: time.Unix(1700000100, 0),
	})
	if err != nil {
		t.Fatalf("SessionToHTML: %v", err)
	}
	// Basic structural checks.
	if !strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("expected DOCTYPE")
	}
	if !strings.Contains(html, "test-session") {
		t.Error("expected session ID in output")
	}
	if !strings.Contains(html, "Hello, agent!") {
		t.Error("expected user message content")
	}
	if !strings.Contains(html, "Hello! How can I help?") {
		t.Error("expected assistant message content")
	}
}

func TestSessionToHTMLCodeBlock(t *testing.T) {
	msgs := []export.Message{
		{Role: "assistant", Content: "Here's some code:\n```python\nprint('hello')\n```"},
	}
	html, err := export.SessionToHTML(export.SessionHTMLOptions{
		SessionID: "code-session",
		Messages:  msgs,
	})
	if err != nil {
		t.Fatalf("SessionToHTML: %v", err)
	}
	if !strings.Contains(html, "<pre>") {
		t.Error("expected <pre> tag for code block")
	}
	if !strings.Contains(html, "print") {
		t.Error("expected code content")
	}
}

func TestSessionToHTMLSystemMessagesOmitted(t *testing.T) {
	msgs := []export.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "Hi"},
		{Role: "assistant", Content: "Hello!"},
	}
	html, err := export.SessionToHTML(export.SessionHTMLOptions{
		SessionID: "sys-session",
		Messages:  msgs,
	})
	if err != nil {
		t.Fatalf("SessionToHTML: %v", err)
	}
	if strings.Contains(html, "You are a helpful assistant") {
		t.Error("system prompt should be omitted from export")
	}
}

func TestSessionToHTMLXSSEscape(t *testing.T) {
	msgs := []export.Message{
		{Role: "user", Content: `<script>alert('xss')</script>`},
	}
	html, err := export.SessionToHTML(export.SessionHTMLOptions{
		SessionID: "xss-session",
		Messages:  msgs,
	})
	if err != nil {
		t.Fatalf("SessionToHTML: %v", err)
	}
	if strings.Contains(html, "<script>") {
		t.Error("raw <script> tag should be escaped in output")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Error("expected HTML-escaped script tag")
	}
}
