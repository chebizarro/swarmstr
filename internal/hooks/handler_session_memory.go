package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func makeSessionMemoryHandler(opts BundledHandlerOpts) HookHandler {
	return func(ev *Event) error {
		if opts.WorkspaceDir == nil {
			return nil
		}
		workspaceDir := opts.WorkspaceDir()
		if workspaceDir == "" {
			return nil
		}

		sessionKey := ev.SessionKey
		if sessionKey == "" {
			sessionKey = "main"
		}

		// How many messages to include (configurable via event context).
		msgLimit := 15
		if v, ok := ev.Context["messages"]; ok {
			if n, ok := v.(int); ok && n > 0 {
				msgLimit = n
			}
		}

		// Fetch transcript.
		var lines []string
		if opts.GetTranscript != nil {
			msgs, err := opts.GetTranscript(sessionKey, msgLimit)
			if err == nil {
				for _, m := range msgs {
					lines = append(lines, fmt.Sprintf("**%s**: %s", m.Role, m.Content))
				}
			}
		}

		if len(lines) == 0 {
			// Nothing to save.
			return nil
		}

		// Generate slug.
		dateStr := time.Now().UTC().Format("2006-01-02")
		slug := time.Now().UTC().Format("1504")
		if opts.GenerateSlug != nil {
			excerpt := strings.Join(lines, "\n")
			if len(excerpt) > 2000 {
				excerpt = excerpt[:2000]
			}
			if s, err := opts.GenerateSlug(excerpt); err == nil && s != "" {
				slug = s
			}
		}

		// Build memory file content.
		now := time.Now().UTC()
		content := fmt.Sprintf("# Session: %s\n\n- **Session Key**: %s\n- **Saved**: %s\n\n## Conversation\n\n%s\n",
			now.Format("2006-01-02 15:04:05 UTC"),
			sessionKey,
			now.Format(time.RFC3339),
			strings.Join(lines, "\n\n"),
		)

		// Write to <workspace>/memory/YYYY-MM-DD-slug.md
		memoryDir := filepath.Join(workspaceDir, "memory")
		if err := os.MkdirAll(memoryDir, 0o750); err != nil {
			return fmt.Errorf("session-memory: mkdir: %w", err)
		}

		filename := fmt.Sprintf("%s-%s.md", dateStr, slug)
		outPath := filepath.Join(memoryDir, filename)
		if err := os.WriteFile(outPath, []byte(content), 0o640); err != nil {
			return fmt.Errorf("session-memory: write: %w", err)
		}

		ev.Messages = append(ev.Messages, fmt.Sprintf("💾 Session saved to memory/%s", filename))
		return nil
	}
}
