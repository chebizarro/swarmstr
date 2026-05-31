package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	commandLoggerMaxBytes = 10 * 1024 * 1024
	commandLoggerBackups  = 3
)

func makeCommandLoggerHandler(opts BundledHandlerOpts) HookHandler {
	return func(ev *Event) error {
		logDir := opts.LogDir
		if logDir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil
			}
			logDir = filepath.Join(home, ".metiq", "logs")
		}

		if err := os.MkdirAll(logDir, 0o750); err != nil {
			return nil // silent fail
		}

		entry := map[string]any{
			"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
			"event":      ev.Name,
			"action":     ev.Action,
			"sessionKey": ev.SessionKey,
		}
		// Copy safe context fields.
		for k, v := range ev.Context {
			switch k {
			case "senderId", "source", "channelId":
				entry[k] = v
			}
		}

		line, err := json.Marshal(entry)
		if err != nil {
			return nil
		}
		line = append(line, '\n')

		logFile := filepath.Join(logDir, "commands.log")
		rotateCommandLogIfNeeded(logFile, int64(len(line)))
		f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
		if err != nil {
			return fmt.Errorf("command-logger: open: %w", err)
		}
		defer f.Close()

		_, _ = f.Write(line)
		return nil
	}
}

func rotateCommandLogIfNeeded(logFile string, incomingBytes int64) {
	info, err := os.Stat(logFile)
	if err != nil || info.Size()+incomingBytes <= commandLoggerMaxBytes {
		return
	}
	for i := commandLoggerBackups; i >= 1; i-- {
		oldPath := fmt.Sprintf("%s.%d", logFile, i)
		if i == commandLoggerBackups {
			_ = os.Remove(oldPath)
			continue
		}
		_ = os.Rename(oldPath, fmt.Sprintf("%s.%d", logFile, i+1))
	}
	_ = os.Rename(logFile, logFile+".1")
}
