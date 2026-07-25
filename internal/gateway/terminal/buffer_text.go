// Renders a session's raw output ring as plain text for terminal.text — an
// agent/LLM affordance that wants readable output, not escape sequences.
// Mirrors OpenClaw's renderTerminalBufferText contract.
package terminal

import (
	"regexp"
	"strings"
)

// ansiSequenceRE matches the escape sequences a shell emits: CSI (ESC [ ... or
// the C1 introducer 0x9b), OSC (ESC ] ... terminated by BEL or ST), DCS/SOS/
// PM/APC strings, and remaining single-character ESC sequences.
var ansiSequenceRE = regexp.MustCompile(
	`\x1b\[[0-?]*[ -/]*[@-~]` + // CSI: ESC [ params intermediates final
		`|\x9b[0-?]*[ -/]*[@-~]` + // CSI with the C1 single-byte introducer
		`|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?` + // OSC: ESC ] ... BEL or ST
		`|\x1b[PX^_][^\x1b]*(?:\x1b\\)?` + // DCS/SOS/PM/APC strings
		`|\x1b[@-Z\\-_]`, // remaining two-byte ESC sequences
)

// controlBytesRE drops residual C0 control bytes (except tab; \r and \n are
// handled by the line/overwrite pass), DEL, and C1 control bytes — including
// the CSI introducer 0x9b, an alternative ANSI escape prefix equivalent to
// ESC [.
var controlBytesRE = regexp.MustCompile("[\x00-\x08\x0b\x0c\x0e-\x1f\x7f" + `\x{0080}-\x{009f}]`)

// RenderText approximates what a terminal would show without running a VT
// emulator: it strips ANSI sequences, collapses carriage-return overwrites
// (progress bars emit "10%\r20%\r30%" — keep the last write per line), and
// drops remaining control bytes. Cursor-movement layouts (vim, htop) will not
// reconstruct faithfully; a true screen snapshot is a tracked follow-up.
func RenderText(raw string) string {
	stripped := ansiSequenceRE.ReplaceAllString(raw, "")
	lines := strings.Split(stripped, "\n")
	for i, line := range lines {
		segments := strings.Split(line, "\r")
		kept := segments[len(segments)-1]
		// A trailing \r ("text\r\n" split) leaves an empty last segment; the
		// carriage return did not overwrite anything yet, so keep the text.
		if kept == "" && len(segments) > 1 {
			kept = segments[len(segments)-2]
		}
		lines[i] = controlBytesRE.ReplaceAllString(kept, "")
	}
	return strings.Join(lines, "\n")
}
