package hooks

import (
	"fmt"
	"os"
	"path/filepath"
)

func makeBootMDHandler(opts BundledHandlerOpts) HookHandler {
	return func(ev *Event) error {
		if ev.EventType != "gateway" || ev.Action != "startup" {
			return nil
		}
		if opts.WorkspaceDir == nil {
			return nil
		}
		workspaceDir := opts.WorkspaceDir()
		if workspaceDir == "" {
			return nil
		}

		bootPath := filepath.Join(workspaceDir, "BOOT.md")
		data, err := os.ReadFile(bootPath)
		if os.IsNotExist(err) {
			// No BOOT.md — nothing to do.
			return nil
		}
		if err != nil {
			return fmt.Errorf("boot-md: read BOOT.md: %w", err)
		}

		if opts.RunBootMD == nil {
			return nil
		}

		sessionKey := ev.SessionKey
		if sessionKey == "" {
			sessionKey = "main"
		}

		return opts.RunBootMD(sessionKey, string(data))
	}
}
