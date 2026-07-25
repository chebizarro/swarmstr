package methods

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf16"
)

// Terminal method schemas. PTY grids are bounded so a hostile client cannot
// request an allocation that overflows the terminal backend's row/column
// math (mirrors the OpenClaw TerminalDimension 1..2000 contract).

const (
	terminalDimensionMin = 1
	terminalDimensionMax = 2000

	// MaxTerminalUploadBytes bounds one file staged through terminal.upload
	// (mirrors the OpenClaw MAX_TERMINAL_UPLOAD_BYTES contract).
	MaxTerminalUploadBytes = 16 * 1024 * 1024
	// MaxTerminalUploadBase64Length is the base64 expansion of the byte cap.
	MaxTerminalUploadBase64Length = (MaxTerminalUploadBytes + 2) / 3 * 4
	// maxTerminalUploadNameLength bounds the browser-provided file name before
	// filesystem sanitization (UTF-16 code units, mirroring OpenClaw).
	maxTerminalUploadNameLength = 255
)

// TerminalOpenRequest opens a shell session; the server picks the shell, cwd,
// and confinement from the (optional) agent workspace.
type TerminalOpenRequest struct {
	AgentID string `json:"agent_id,omitempty"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
}

// TerminalInputRequest writes client keystrokes to the session stdin.
type TerminalInputRequest struct {
	SessionID string `json:"session_id"`
	Data      string `json:"data"`
}

// TerminalResizeRequest resizes the PTY grid after the client viewport changes.
type TerminalResizeRequest struct {
	SessionID string `json:"session_id"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
}

// TerminalCloseRequest closes a session and kills its process tree.
type TerminalCloseRequest struct {
	SessionID string `json:"session_id"`
}

// TerminalAttachRequest attaches the calling admin connection to a live
// session. Connection-owned sessions use take-over: the previous owner stops
// receiving output and gets terminal.exit with reason "detached".
type TerminalAttachRequest struct {
	SessionID string `json:"session_id"`
}

// TerminalListRequest enumerates attachable sessions; it carries no fields.
type TerminalListRequest struct{}

// TerminalTextRequest reads the current output buffer as plain text without
// attaching (an agent/LLM affordance).
type TerminalTextRequest struct {
	SessionID string `json:"session_id"`
}

// TerminalUploadRequest stages one file next to an existing terminal session.
// Metiq deviation: the file lands in the session's working directory under
// os.Root containment rather than a host temp dir.
type TerminalUploadRequest struct {
	SessionID     string `json:"session_id"`
	Name          string `json:"name"`
	ContentBase64 string `json:"contentBase64"`
}

func validTerminalDimension(v int) bool {
	return v >= terminalDimensionMin && v <= terminalDimensionMax
}

func (r TerminalOpenRequest) Normalize() (TerminalOpenRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	if !validTerminalDimension(r.Cols) || !validTerminalDimension(r.Rows) {
		return r, fmt.Errorf("invalid terminal.open params: cols and rows must be between %d and %d", terminalDimensionMin, terminalDimensionMax)
	}
	return r, nil
}

func (r TerminalInputRequest) Normalize() (TerminalInputRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("invalid terminal.input params: sessionId is required")
	}
	return r, nil
}

func (r TerminalResizeRequest) Normalize() (TerminalResizeRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("invalid terminal.resize params: sessionId is required")
	}
	if !validTerminalDimension(r.Cols) || !validTerminalDimension(r.Rows) {
		return r, fmt.Errorf("invalid terminal.resize params: cols and rows must be between %d and %d", terminalDimensionMin, terminalDimensionMax)
	}
	return r, nil
}

func (r TerminalCloseRequest) Normalize() (TerminalCloseRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("invalid terminal.close params: sessionId is required")
	}
	return r, nil
}

func (r TerminalAttachRequest) Normalize() (TerminalAttachRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("invalid terminal.attach params: sessionId is required")
	}
	return r, nil
}

func (r TerminalTextRequest) Normalize() (TerminalTextRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("invalid terminal.text params: sessionId is required")
	}
	return r, nil
}

func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		n += len(utf16.Encode([]rune{r}))
	}
	return n
}

func (r TerminalUploadRequest) Normalize() (TerminalUploadRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("invalid terminal.upload params: sessionId is required")
	}
	if r.Name == "" || utf16Length(r.Name) > maxTerminalUploadNameLength {
		return r, fmt.Errorf("invalid terminal.upload params: name must be 1..%d characters", maxTerminalUploadNameLength)
	}
	if len(r.ContentBase64) > MaxTerminalUploadBase64Length {
		return r, fmt.Errorf("terminal upload exceeds %d bytes", MaxTerminalUploadBytes)
	}
	if len(r.ContentBase64)%4 != 0 {
		return r, fmt.Errorf("invalid terminal.upload base64 content")
	}
	return r, nil
}

// Content decodes the upload payload, enforcing canonical padded base64
// (mirrors OpenClaw's isCanonicalTerminalUploadBase64: no whitespace, exact
// padding, zero-valued unused bits) and the decoded-size cap.
func (r TerminalUploadRequest) Content() ([]byte, error) {
	decoded, err := base64.StdEncoding.Strict().DecodeString(r.ContentBase64)
	if err != nil {
		return nil, fmt.Errorf("invalid terminal.upload base64 content")
	}
	if len(decoded) > MaxTerminalUploadBytes {
		return nil, fmt.Errorf("terminal upload exceeds %d bytes", MaxTerminalUploadBytes)
	}
	// Strict() still ignores \r\n; a canonical payload round-trips exactly.
	if base64.StdEncoding.EncodeToString(decoded) != r.ContentBase64 {
		return nil, fmt.Errorf("invalid terminal.upload base64 content")
	}
	return decoded, nil
}

func DecodeTerminalOpenParams(params json.RawMessage) (TerminalOpenRequest, error) {
	return decodeMethodParams[TerminalOpenRequest](params)
}

func DecodeTerminalInputParams(params json.RawMessage) (TerminalInputRequest, error) {
	return decodeMethodParams[TerminalInputRequest](params)
}

func DecodeTerminalResizeParams(params json.RawMessage) (TerminalResizeRequest, error) {
	return decodeMethodParams[TerminalResizeRequest](params)
}

func DecodeTerminalCloseParams(params json.RawMessage) (TerminalCloseRequest, error) {
	return decodeMethodParams[TerminalCloseRequest](params)
}

func DecodeTerminalAttachParams(params json.RawMessage) (TerminalAttachRequest, error) {
	return decodeMethodParams[TerminalAttachRequest](params)
}

func DecodeTerminalListParams(params json.RawMessage) (TerminalListRequest, error) {
	return decodeMethodParams[TerminalListRequest](params)
}

func DecodeTerminalTextParams(params json.RawMessage) (TerminalTextRequest, error) {
	return decodeMethodParams[TerminalTextRequest](params)
}

func DecodeTerminalUploadParams(params json.RawMessage) (TerminalUploadRequest, error) {
	return decodeMethodParams[TerminalUploadRequest](params)
}
