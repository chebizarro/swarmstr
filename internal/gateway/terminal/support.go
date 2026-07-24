package terminal

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"syscall"
)

// randomSessionID returns a 16-byte hex session id with a stable prefix so
// terminal ids are recognizable in logs.
func randomSessionID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "term-fallback"
	}
	return "term-" + hex.EncodeToString(buf)
}

// sysWaitStatus extracts the platform wait status when available so exit
// signals can be reported distinctly from exit codes.
func sysWaitStatus(state *os.ProcessState) (syscall.WaitStatus, bool) {
	ws, ok := state.Sys().(syscall.WaitStatus)
	return ws, ok
}
