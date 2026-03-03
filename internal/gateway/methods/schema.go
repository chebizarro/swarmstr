package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"swarmstr/internal/memory"
	"swarmstr/internal/store/state"
)

const (
	MethodSupportedMethods = "supportedmethods"
	MethodStatus           = "status.get"
	MethodMemorySearch     = "memory.search"
	MethodChatSend         = "chat.send"
	MethodSessionGet       = "session.get"
	MethodListGet          = "list.get"
	MethodListPut          = "list.put"
	MethodRelayPolicyGet   = "relay.policy.get"
	MethodConfigGet        = "config.get"
	MethodConfigPut        = "config.put"
)

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
}

type MemorySearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type MemorySearchResponse struct {
	Results []memory.IndexedMemory `json:"results"`
}

type ChatSendRequest struct {
	To   string `json:"to"`
	Text string `json:"text"`
}

type SessionGetRequest struct {
	SessionID string `json:"session_id"`
	Limit     int    `json:"limit,omitempty"`
}

type SessionGetResponse struct {
	Session    state.SessionDoc           `json:"session"`
	Transcript []state.TranscriptEntryDoc `json:"transcript"`
}

type ListGetRequest struct {
	Name string `json:"name"`
}

type ListPutRequest struct {
	Name            string   `json:"name"`
	Items           []string `json:"items"`
	ExpectedVersion int      `json:"expected_version,omitempty"`
	ExpectedEvent   string   `json:"expected_event,omitempty"`
}

type ConfigPutRequest struct {
	Config          state.ConfigDoc `json:"config"`
	ExpectedVersion int             `json:"expected_version,omitempty"`
	ExpectedEvent   string          `json:"expected_event,omitempty"`
}

type RelayPolicyResponse struct {
	ReadRelays           []string `json:"read_relays"`
	WriteRelays          []string `json:"write_relays"`
	RuntimeDMRelays      []string `json:"runtime_dm_relays"`
	RuntimeControlRelays []string `json:"runtime_control_relays"`
}

func (r MemorySearchRequest) Normalize() (MemorySearchRequest, error) {
	r.Query = strings.TrimSpace(r.Query)
	if r.Query == "" {
		return r, fmt.Errorf("query is required")
	}
	if len(r.Query) > 256 {
		r.Query = r.Query[:256]
	}
	r.Limit = normalizeLimit(r.Limit, 20, 200)
	return r, nil
}

func (r ChatSendRequest) Normalize() (ChatSendRequest, error) {
	r.To = strings.TrimSpace(r.To)
	r.Text = strings.TrimSpace(r.Text)
	if r.To == "" || r.Text == "" {
		return r, fmt.Errorf("to and text are required")
	}
	const maxTextRunes = 4096
	if utf8.RuneCountInString(r.Text) > maxTextRunes {
		return r, fmt.Errorf("text exceeds %d characters", maxTextRunes)
	}
	return r, nil
}

func (r SessionGetRequest) Normalize() (SessionGetRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	r.Limit = normalizeLimit(r.Limit, 50, 500)
	return r, nil
}

func (r ListGetRequest) Normalize() (ListGetRequest, error) {
	r.Name = normalizeListName(r.Name)
	if r.Name == "" {
		return r, fmt.Errorf("name is required")
	}
	return r, nil
}

func (r ListPutRequest) Normalize() (ListPutRequest, error) {
	r.Name = normalizeListName(r.Name)
	if r.Name == "" {
		return r, fmt.Errorf("name is required")
	}
	out := make([]string, 0, len(r.Items))
	seen := map[string]struct{}{}
	for _, item := range r.Items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	r.Items = out
	if r.ExpectedVersion < 0 {
		return r, fmt.Errorf("expected_version must be >= 0")
	}
	r.ExpectedEvent = strings.TrimSpace(r.ExpectedEvent)
	return r, nil
}

func (r ConfigPutRequest) Normalize() (ConfigPutRequest, error) {
	if strings.TrimSpace(r.Config.DM.Policy) == "" {
		return r, fmt.Errorf("config.dm.policy is required")
	}
	if r.Config.Version == 0 {
		r.Config.Version = 1
	}
	if r.ExpectedVersion < 0 {
		return r, fmt.Errorf("expected_version must be >= 0")
	}
	r.ExpectedEvent = strings.TrimSpace(r.ExpectedEvent)
	return r, nil
}

func SupportedMethods() []string {
	return []string{
		MethodSupportedMethods,
		MethodStatus,
		MethodMemorySearch,
		MethodChatSend,
		MethodSessionGet,
		MethodListGet,
		MethodListPut,
		MethodRelayPolicyGet,
		MethodConfigGet,
		MethodConfigPut,
	}
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

func DecodeChatSendParams(params json.RawMessage) (ChatSendRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ChatSendRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 2 {
			return ChatSendRequest{}, fmt.Errorf("invalid params")
		}
		to, ok := arr[0].(string)
		if !ok {
			return ChatSendRequest{}, fmt.Errorf("invalid params")
		}
		text, ok := arr[1].(string)
		if !ok {
			return ChatSendRequest{}, fmt.Errorf("invalid params")
		}
		return ChatSendRequest{To: to, Text: text}, nil
	}
	return decodeMethodParams[ChatSendRequest](params)
}

func DecodeSessionGetParams(params json.RawMessage) (SessionGetRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return SessionGetRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return SessionGetRequest{}, fmt.Errorf("invalid params")
		}
		sessionID, ok := arr[0].(string)
		if !ok {
			return SessionGetRequest{}, fmt.Errorf("invalid params")
		}
		req := SessionGetRequest{SessionID: sessionID}
		if len(arr) > 1 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return SessionGetRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			}
		}
		return req, nil
	}
	return decodeMethodParams[SessionGetRequest](params)
}

func DecodeConfigPutParams(params json.RawMessage) (ConfigPutRequest, error) {
	if isJSONArray(params) {
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return ConfigPutRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return ConfigPutRequest{}, fmt.Errorf("invalid params")
		}
		var cfg state.ConfigDoc
		if err := json.Unmarshal(arr[0], &cfg); err != nil {
			return ConfigPutRequest{}, fmt.Errorf("invalid params")
		}
		req := ConfigPutRequest{Config: cfg}
		if len(arr) == 2 {
			if err := decodeWritePrecondition(arr[1], &req.ExpectedVersion, &req.ExpectedEvent); err != nil {
				return ConfigPutRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[ConfigPutRequest](params)
}

func DecodeListGetParams(params json.RawMessage) (ListGetRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ListGetRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return ListGetRequest{}, fmt.Errorf("invalid params")
		}
		name, ok := arr[0].(string)
		if !ok {
			return ListGetRequest{}, fmt.Errorf("invalid params")
		}
		return ListGetRequest{Name: name}, nil
	}
	return decodeMethodParams[ListGetRequest](params)
}

func DecodeListPutParams(params json.RawMessage) (ListPutRequest, error) {
	if isJSONArray(params) {
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return ListPutRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) < 2 || len(arr) > 3 {
			return ListPutRequest{}, fmt.Errorf("invalid params")
		}
		var name string
		if err := json.Unmarshal(arr[0], &name); err != nil {
			return ListPutRequest{}, fmt.Errorf("invalid params")
		}
		var items []string
		if err := json.Unmarshal(arr[1], &items); err != nil {
			return ListPutRequest{}, fmt.Errorf("invalid params")
		}
		req := ListPutRequest{Name: name, Items: items}
		if len(arr) == 3 {
			if err := decodeWritePrecondition(arr[2], &req.ExpectedVersion, &req.ExpectedEvent); err != nil {
				return ListPutRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[ListPutRequest](params)
}

func decodeMethodParams[T any](params json.RawMessage) (T, error) {
	var out T
	if len(bytes.TrimSpace(params)) == 0 {
		return out, nil
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return out, fmt.Errorf("invalid params")
	}
	return out, nil
}

func isJSONArray(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '['
}

func normalizeLimit(value, def, max int) int {
	if value <= 0 {
		return def
	}
	if value > max {
		return max
	}
	return value
}

func normalizeListName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

func decodeWritePrecondition(raw json.RawMessage, expectedVersion *int, expectedEvent *string) error {
	var pre struct {
		ExpectedVersion *int   `json:"expected_version"`
		ExpectedEvent   string `json:"expected_event"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pre); err != nil {
		return err
	}
	if pre.ExpectedVersion != nil {
		*expectedVersion = *pre.ExpectedVersion
	}
	*expectedEvent = pre.ExpectedEvent
	return nil
}
