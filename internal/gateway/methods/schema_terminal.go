package methods

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Terminal method schemas. PTY grids are bounded so a hostile client cannot
// request an allocation that overflows the terminal backend's row/column
// math (mirrors the OpenClaw TerminalDimension 1..2000 contract).

const (
	terminalDimensionMin = 1
	terminalDimensionMax = 2000
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
