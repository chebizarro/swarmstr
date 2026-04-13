package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

type LogsTailRequest struct {
	Cursor   int64 `json:"cursor,omitempty"`
	Limit    int   `json:"limit,omitempty"`
	MaxBytes int   `json:"max_bytes,omitempty"`
	Lines    int   `json:"lines,omitempty"`
}

type RuntimeObserveRequest struct {
	IncludeEvents *bool    `json:"include_events,omitempty"`
	IncludeLogs   *bool    `json:"include_logs,omitempty"`
	EventCursor   int64    `json:"event_cursor,omitempty"`
	LogCursor     int64    `json:"log_cursor,omitempty"`
	EventLimit    int      `json:"event_limit,omitempty"`
	LogLimit      int      `json:"log_limit,omitempty"`
	MaxBytes      int      `json:"max_bytes,omitempty"`
	WaitTimeoutMS int      `json:"wait_timeout_ms,omitempty"`
	Events        []string `json:"events,omitempty"`
	AgentID       string   `json:"agent_id,omitempty"`
	SessionID     string   `json:"session_id,omitempty"`
	ChannelID     string   `json:"channel_id,omitempty"`
	Direction     string   `json:"direction,omitempty"`
	Subsystem     string   `json:"subsystem,omitempty"`
	Source        string   `json:"source,omitempty"`
}

type RelayPolicyResponse struct {
	ReadRelays           []string `json:"read_relays"`
	WriteRelays          []string `json:"write_relays"`
	RuntimeDMRelays      []string `json:"runtime_dm_relays"`
	RuntimeControlRelays []string `json:"runtime_control_relays"`
}

func (r LogsTailRequest) Normalize() (LogsTailRequest, error) {
	if r.Limit == 0 && r.Lines != 0 {
		r.Limit = r.Lines
	}
	r.Limit = normalizeLimit(r.Limit, 100, 2000)
	r.MaxBytes = normalizeLimit(r.MaxBytes, 64*1024, 2*1024*1024)
	if r.Cursor < 0 {
		r.Cursor = 0
	}
	return r, nil
}

func (r RuntimeObserveRequest) Normalize() (RuntimeObserveRequest, error) {
	includeEvents := true
	if r.IncludeEvents != nil {
		includeEvents = *r.IncludeEvents
	}
	includeLogs := true
	if r.IncludeLogs != nil {
		includeLogs = *r.IncludeLogs
	}
	if !includeEvents && !includeLogs {
		return r, fmt.Errorf("at least one of include_events or include_logs must be true")
	}
	r.IncludeEvents = boolPtr(includeEvents)
	r.IncludeLogs = boolPtr(includeLogs)
	if r.EventCursor < 0 {
		r.EventCursor = 0
	}
	if r.LogCursor < 0 {
		r.LogCursor = 0
	}
	r.EventLimit = normalizeLimit(r.EventLimit, 20, 200)
	r.LogLimit = normalizeLimit(r.LogLimit, 20, 200)
	r.MaxBytes = normalizeLimit(r.MaxBytes, 32*1024, 256*1024)
	if r.WaitTimeoutMS < 0 {
		r.WaitTimeoutMS = 0
	}
	if r.WaitTimeoutMS > 60_000 {
		r.WaitTimeoutMS = 60_000
	}
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.ChannelID = strings.TrimSpace(r.ChannelID)
	r.Direction = strings.TrimSpace(r.Direction)
	r.Subsystem = strings.TrimSpace(r.Subsystem)
	r.Source = strings.TrimSpace(r.Source)
	r.Events = compactStringSlice(r.Events)
	return r, nil
}

func DecodeLogsTailParams(params json.RawMessage) (LogsTailRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return LogsTailRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 3 {
			return LogsTailRequest{}, fmt.Errorf("invalid params")
		}
		req := LogsTailRequest{}
		if len(arr) >= 1 {
			switch v := arr[0].(type) {
			case float64:
				if math.Trunc(v) != v {
					return LogsTailRequest{}, fmt.Errorf("invalid params")
				}
				req.Cursor = int64(v)
			case int:
				req.Cursor = int64(v)
			}
		}
		if len(arr) >= 2 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return LogsTailRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			default:
				return LogsTailRequest{}, fmt.Errorf("invalid params")
			}
		}
		if len(arr) == 3 {
			switch v := arr[2].(type) {
			case float64:
				if math.Trunc(v) != v {
					return LogsTailRequest{}, fmt.Errorf("invalid params")
				}
				req.MaxBytes = int(v)
			case int:
				req.MaxBytes = v
			default:
				return LogsTailRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[LogsTailRequest](params)
}

func DecodeRuntimeObserveParams(params json.RawMessage) (RuntimeObserveRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return RuntimeObserveRequest{}, nil
	}
	return decodeMethodParams[RuntimeObserveRequest](params)
}
