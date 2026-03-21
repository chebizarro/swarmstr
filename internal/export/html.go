// Package export provides session export functionality for metiq.
//
// The primary output is self-contained HTML: a single file with inline CSS and
// minimal JS that renders a conversation transcript. The file works offline and
// requires no external dependencies.
//
// Usage:
//
//	html, err := export.SessionToHTML(export.SessionHTMLOptions{
//	    SessionID: "sess-abc123",
//	    AgentID:   "main",
//	    Messages:  messages, // []Message
//	    AgentName: "Assistant",
//	})
package export

import (
	"bytes"
	"fmt"
	"html"
	"strings"
	"time"
)

// Message represents a single conversation turn for export purposes.
type Message struct {
	Role      string // "user" | "assistant" | "system" | "tool"
	Content   string
	Timestamp int64  // Unix seconds; 0 = unknown
	ID        string // Optional stable ID
}

// SessionHTMLOptions configures the HTML export.
type SessionHTMLOptions struct {
	SessionID  string
	AgentID    string
	AgentName  string
	PubKey     string // Agent Nostr pubkey (displayed in header)
	Messages   []Message
	ExportedAt time.Time
}

// SessionToHTML renders a conversation transcript as a self-contained HTML page.
// The output is valid UTF-8 and has no external dependencies.
func SessionToHTML(opts SessionHTMLOptions) (string, error) {
	if opts.ExportedAt.IsZero() {
		opts.ExportedAt = time.Now()
	}
	if opts.AgentName == "" {
		opts.AgentName = opts.AgentID
		if opts.AgentName == "" {
			opts.AgentName = "Agent"
		}
	}

	var b bytes.Buffer
	b.WriteString(htmlHeader(opts))

	for _, msg := range opts.Messages {
		if msg.Role == "system" {
			continue // omit system prompts from export
		}
		b.WriteString(renderMessage(msg))
	}

	b.WriteString(htmlFooter(opts))
	return b.String(), nil
}

func htmlHeader(opts SessionHTMLOptions) string {
	title := fmt.Sprintf("Session %s", opts.SessionID)
	if opts.SessionID == "" {
		title = "Metiq Session Export"
	}
	exportedStr := opts.ExportedAt.UTC().Format("2006-01-02 15:04 UTC")

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:#0f1117;color:#e1e4e8;min-height:100vh;padding:1rem}
.header{max-width:800px;margin:0 auto 1.5rem;padding:1rem;background:#1e2130;border-radius:8px;border-left:4px solid #7c3aed}
.header h1{font-size:1.1rem;color:#a78bfa;margin-bottom:.5rem}
.header .meta{font-size:.8rem;color:#6b7280}
.messages{max-width:800px;margin:0 auto;display:flex;flex-direction:column;gap:.75rem}
.msg{padding:.75rem 1rem;border-radius:8px;line-height:1.6;font-size:.95rem;white-space:pre-wrap;word-break:break-word}
.msg.user{background:#1e2749;border-left:3px solid #3b82f6}
.msg.assistant{background:#1a2a1a;border-left:3px solid #22c55e}
.msg.tool{background:#2a1f14;border-left:3px solid #f59e0b;font-family:monospace;font-size:.85rem}
.msg-meta{font-size:.75rem;color:#6b7280;margin-bottom:.4rem}
.msg-role{font-weight:600;text-transform:capitalize}
.msg-role.user{color:#60a5fa}
.msg-role.assistant{color:#4ade80}
.msg-role.tool{color:#fbbf24}
code{background:#2d2d2d;padding:.1em .3em;border-radius:3px;font-family:monospace;font-size:.9em}
pre{background:#2d2d2d;padding:.75rem;border-radius:6px;overflow-x:auto;margin:.5rem 0}
pre code{background:none;padding:0}
.footer{max-width:800px;margin:1.5rem auto;text-align:center;font-size:.75rem;color:#4b5563}
</style>
</head>
<body>
<div class="header">
  <h1>%s</h1>
  <div class="meta">
    <span>Session: %s</span>
    %s
    &nbsp;·&nbsp;Exported: %s
    &nbsp;·&nbsp;%d messages
  </div>
</div>
<div class="messages">
`, html.EscapeString(title), html.EscapeString(title),
		html.EscapeString(opts.SessionID),
		agentMetaHTML(opts),
		exportedStr,
		countVisible(opts.Messages))
}

func agentMetaHTML(opts SessionHTMLOptions) string {
	if opts.AgentName == "" {
		return ""
	}
	s := fmt.Sprintf(`&nbsp;·&nbsp;Agent: <strong>%s</strong>`, html.EscapeString(opts.AgentName))
	if opts.PubKey != "" {
		s += fmt.Sprintf(` (<code>%s</code>)`, html.EscapeString(opts.PubKey[:min8(opts.PubKey)]))
	}
	return s
}

func min8(s string) int {
	if len(s) < 8 {
		return len(s)
	}
	return 8
}

func countVisible(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role != "system" {
			n++
		}
	}
	return n
}

func renderMessage(msg Message) string {
	roleCls := msg.Role
	if roleCls != "user" && roleCls != "assistant" && roleCls != "tool" {
		roleCls = "assistant"
	}

	var meta strings.Builder
	meta.WriteString(fmt.Sprintf(`<span class="msg-role %s">%s</span>`, roleCls, html.EscapeString(strings.Title(msg.Role))))
	if msg.Timestamp > 0 {
		t := time.Unix(msg.Timestamp, 0).UTC()
		meta.WriteString(fmt.Sprintf(`&nbsp;·&nbsp;%s`, t.Format("2006-01-02 15:04:05")))
	}
	if msg.ID != "" {
		meta.WriteString(fmt.Sprintf(`&nbsp;·&nbsp;<code>%s</code>`, html.EscapeString(shortID(msg.ID))))
	}

	content := renderContent(msg.Content)

	return fmt.Sprintf(`<div class="msg %s">
  <div class="msg-meta">%s</div>
  %s
</div>
`, roleCls, meta.String(), content)
}

// renderContent converts plain text to safe HTML, promoting fenced code blocks.
func renderContent(text string) string {
	var out strings.Builder
	lines := strings.Split(text, "\n")
	inCode := false
	codeLines := []string{}
	lang := ""

	flush := func() {
		if len(codeLines) > 0 {
			block := html.EscapeString(strings.Join(codeLines, "\n"))
			if lang != "" {
				out.WriteString(fmt.Sprintf(`<pre><code class="language-%s">%s</code></pre>`, html.EscapeString(lang), block))
			} else {
				out.WriteString(fmt.Sprintf(`<pre><code>%s</code></pre>`, block))
			}
			codeLines = codeLines[:0]
			lang = ""
		}
	}

	for _, line := range lines {
		if !inCode && strings.HasPrefix(line, "```") {
			inCode = true
			lang = strings.TrimPrefix(line, "```")
			continue
		}
		if inCode && strings.HasPrefix(line, "```") {
			flush()
			inCode = false
			continue
		}
		if inCode {
			codeLines = append(codeLines, line)
		} else {
			out.WriteString(html.EscapeString(line))
			out.WriteString("\n")
		}
	}
	if inCode {
		flush()
	}

	return out.String()
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:8] + "…"
}

func htmlFooter(opts SessionHTMLOptions) string {
	return fmt.Sprintf(`</div>
<div class="footer">Generated by <strong>metiq</strong> &mdash; %s</div>
</body>
</html>
`, opts.ExportedAt.UTC().Format("2006-01-02 15:04 UTC"))
}
