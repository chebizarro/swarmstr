package methods

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"

	mcppkg "metiq/internal/mcp"
	"metiq/internal/memory"
	"metiq/internal/store/state"
	"strings"
	"unicode/utf8"
)

// ErrConfigConflict is returned when a config mutation is rejected because
// the caller's base_hash does not match the server's current config hash.
// This implements optimistic concurrency control for config updates.
var ErrConfigConflict = errors.New("config conflict: base_hash mismatch")

// CheckBaseHash validates that baseHash (from the request) matches the hash
// of current.  If baseHash is empty, the check is skipped.  Returns
// ErrConfigConflict if the hashes differ.
func CheckBaseHash(current state.ConfigDoc, baseHash string) error {
	baseHash = strings.TrimSpace(baseHash)
	if baseHash == "" {
		return nil
	}
	got := current.Hash()
	if got != baseHash {
		return fmt.Errorf("%w: have %s, client sent %s", ErrConfigConflict, got, baseHash)
	}
	return nil
}

type CallRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type CallResponse struct {
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type StatusResponse struct {
	PubKey        string   `json:"pubkey"`
	Relays        []string `json:"relays"`
	DMPolicy      string   `json:"dm_policy"`
	UptimeSeconds int      `json:"uptime_seconds"`
	UptimeMS      int64    `json:"uptime_ms"`
	Version       string   `json:"version"`

	// Subscriptions reports health snapshots for long-lived subscriptions.
	// Omitted when empty (e.g. during early startup).
	Subscriptions []SubHealthInfo `json:"subscriptions,omitempty"`

	// RelaySets reports current NIP-51 kind:30002 relay sets.
	// Omitted when no relay sets are loaded.
	RelaySets map[string][]string `json:"relay_sets,omitempty"`

	// MCP reports external MCP lifecycle/health telemetry when MCP is configured.
	MCP *mcppkg.TelemetrySnapshot `json:"mcp,omitempty"`

	// FIPS reports FIPS mesh transport health when the experimental_fips
	// build tag is enabled and the transport is active.
	FIPS any `json:"fips,omitempty"`
}

// SubHealthInfo is the JSON-friendly representation of a subscription health
// snapshot, suitable for the status.get response and /status slash command.
type SubHealthInfo struct {
	Label            string   `json:"label"`
	BoundRelays      []string `json:"bound_relays"`
	LastEventAt      int64    `json:"last_event_at,omitempty"`
	LastReconnectAt  int64    `json:"last_reconnect_at,omitempty"`
	LastClosedReason string   `json:"last_closed_reason,omitempty"`
	ReplayWindowMS   int64    `json:"replay_window_ms"`
	EventCount       int64    `json:"event_count"`
	ReconnectCount   int64    `json:"reconnect_count"`
}

type MemorySearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type MemorySearchResponse struct {
	Results []memory.IndexedMemory `json:"results"`
}

// MemoryCompactRequest asks the context engine to compact the given session.
// If SessionID is empty, all sessions are compacted.
type MemoryCompactRequest struct {
	SessionID string `json:"session_id,omitempty"`
}

// MemoryCompactResponse reports the result of a compaction operation.
type MemoryCompactResponse struct {
	OK           bool   `json:"ok"`
	SessionsRun  int    `json:"sessions_run"`
	TokensBefore int    `json:"tokens_before,omitempty"`
	TokensAfter  int    `json:"tokens_after,omitempty"`
	Summary      string `json:"summary,omitempty"`
}

type CanvasGetRequest struct {
	ID string `json:"id"`
}

type CanvasListRequest struct{}

type CanvasUpdateRequest struct {
	ID          string `json:"id"`
	ContentType string `json:"content_type"`
	Data        string `json:"data"`
}

type CanvasDeleteRequest struct {
	ID string `json:"id"`
}

func (r MemorySearchRequest) Normalize() (MemorySearchRequest, error) {
	r.Query = strings.TrimSpace(r.Query)
	if r.Query == "" {
		return r, fmt.Errorf("query is required")
	}
	if utf8.RuneCountInString(r.Query) > 256 {
		r.Query = truncateRunes(r.Query, 256)
	}
	r.Limit = normalizeLimit(r.Limit, 20, 200)
	return r, nil
}

func DecodeMemorySearchParams(params json.RawMessage) (MemorySearchRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return MemorySearchRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return MemorySearchRequest{}, fmt.Errorf("invalid params")
		}
		query, ok := arr[0].(string)
		if !ok {
			return MemorySearchRequest{}, fmt.Errorf("invalid params")
		}
		req := MemorySearchRequest{Query: query}
		if len(arr) > 1 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return MemorySearchRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			}
		}
		return req, nil
	}
	return decodeMethodParams[MemorySearchRequest](params)
}
