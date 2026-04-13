package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
	"metiq/internal/store/state"
	"strings"
)

type ListGetRequest struct {
	Name string `json:"name"`
}

type ListPutRequest struct {
	Name               string   `json:"name"`
	Items              []string `json:"items"`
	ExpectedVersion    int      `json:"expected_version,omitempty"`
	ExpectedVersionSet bool     `json:"-"`
	ExpectedEvent      string   `json:"expected_event,omitempty"`
}

type ConfigPutRequest struct {
	Config             state.ConfigDoc `json:"config"`
	ExpectedVersion    int             `json:"expected_version,omitempty"`
	ExpectedVersionSet bool            `json:"-"`
	ExpectedEvent      string          `json:"expected_event,omitempty"`
	BaseHash           string          `json:"baseHash,omitempty"`
}

type ConfigSetRequest struct {
	Key      string `json:"key"`
	Value    any    `json:"value"`
	Raw      string `json:"raw,omitempty"`
	BaseHash string `json:"baseHash,omitempty"`
}

type ConfigApplyRequest struct {
	Config         state.ConfigDoc `json:"config"`
	Raw            string          `json:"raw,omitempty"`
	BaseHash       string          `json:"baseHash,omitempty"`
	SessionKey     string          `json:"sessionKey,omitempty"`
	Note           string          `json:"note,omitempty"`
	RestartDelayMS int             `json:"restartDelayMs,omitempty"`
}

type ConfigPatchRequest struct {
	Patch          map[string]any `json:"patch"`
	Raw            string         `json:"raw,omitempty"`
	BaseHash       string         `json:"baseHash,omitempty"`
	SessionKey     string         `json:"sessionKey,omitempty"`
	Note           string         `json:"note,omitempty"`
	RestartDelayMS int            `json:"restartDelayMs,omitempty"`
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
	if r.ExpectedVersionSet && r.ExpectedVersion < 0 {
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
	if r.ExpectedVersionSet && r.ExpectedVersion < 0 {
		return r, fmt.Errorf("expected_version must be >= 0")
	}
	r.BaseHash = strings.TrimSpace(r.BaseHash)
	r.ExpectedEvent = strings.TrimSpace(r.ExpectedEvent)
	return r, nil
}

func (r ConfigSetRequest) Normalize() (ConfigSetRequest, error) {
	r.Key = strings.TrimSpace(r.Key)
	r.Raw = strings.TrimSpace(r.Raw)
	r.BaseHash = strings.TrimSpace(r.BaseHash)
	if r.Key == "" && r.Raw == "" {
		return r, fmt.Errorf("key is required")
	}
	return r, nil
}

func (r ConfigApplyRequest) Normalize() (ConfigApplyRequest, error) {
	r.Raw = strings.TrimSpace(r.Raw)
	r.BaseHash = strings.TrimSpace(r.BaseHash)
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	r.Note = strings.TrimSpace(r.Note)
	if r.RestartDelayMS < 0 {
		r.RestartDelayMS = 0
	}
	if r.Raw != "" {
		return r, nil
	}
	if strings.TrimSpace(r.Config.DM.Policy) == "" {
		return r, fmt.Errorf("config.dm.policy is required")
	}
	if r.Config.Version == 0 {
		r.Config.Version = 1
	}
	return r, nil
}

func (r ConfigPatchRequest) Normalize() (ConfigPatchRequest, error) {
	r.Raw = strings.TrimSpace(r.Raw)
	r.BaseHash = strings.TrimSpace(r.BaseHash)
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	r.Note = strings.TrimSpace(r.Note)
	if r.RestartDelayMS < 0 {
		r.RestartDelayMS = 0
	}
	if r.Raw == "" && len(r.Patch) == 0 {
		return r, fmt.Errorf("patch is required")
	}
	return r, nil
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
			expectedVersionSet, err := decodeWritePrecondition(arr[1], &req.ExpectedVersion, &req.ExpectedEvent, &req.BaseHash)
			if err != nil {
				return ConfigPutRequest{}, fmt.Errorf("invalid params")
			}
			req.ExpectedVersionSet = expectedVersionSet
		}
		return req, nil
	}
	params = normalizeObjectParamAliases(params)
	type configPutCompatRequest struct {
		Config          state.ConfigDoc `json:"config"`
		ExpectedVersion *int            `json:"expected_version,omitempty"`
		ExpectedEvent   string          `json:"expected_event,omitempty"`
		BaseHash        string          `json:"baseHash,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat configPutCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return ConfigPutRequest{}, fmt.Errorf("invalid params")
	}
	req := ConfigPutRequest{Config: compat.Config, ExpectedEvent: compat.ExpectedEvent, BaseHash: compat.BaseHash}
	if compat.ExpectedVersion != nil {
		req.ExpectedVersionSet = true
		req.ExpectedVersion = *compat.ExpectedVersion
	}
	return req, nil
}

func DecodeConfigSetParams(params json.RawMessage) (ConfigSetRequest, error) {
	if isJSONArray(params) {
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return ConfigSetRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 2 {
			return ConfigSetRequest{}, fmt.Errorf("invalid params")
		}
		var req ConfigSetRequest
		if err := json.Unmarshal(arr[0], &req.Key); err != nil {
			return ConfigSetRequest{}, fmt.Errorf("invalid params")
		}
		if err := json.Unmarshal(arr[1], &req.Value); err != nil {
			return ConfigSetRequest{}, fmt.Errorf("invalid params")
		}
		return req, nil
	}
	return decodeMethodParams[ConfigSetRequest](params)
}

func DecodeConfigApplyParams(params json.RawMessage) (ConfigApplyRequest, error) {
	if isJSONArray(params) {
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return ConfigApplyRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return ConfigApplyRequest{}, fmt.Errorf("invalid params")
		}
		var rawString string
		if err := json.Unmarshal(arr[0], &rawString); err == nil {
			return ConfigApplyRequest{Raw: rawString}, nil
		}
		var cfg state.ConfigDoc
		if err := json.Unmarshal(arr[0], &cfg); err != nil {
			return ConfigApplyRequest{}, fmt.Errorf("invalid params")
		}
		return ConfigApplyRequest{Config: cfg}, nil
	}
	return decodeMethodParams[ConfigApplyRequest](params)
}

func DecodeConfigPatchParams(params json.RawMessage) (ConfigPatchRequest, error) {
	if isJSONArray(params) {
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return ConfigPatchRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return ConfigPatchRequest{}, fmt.Errorf("invalid params")
		}
		var rawString string
		if err := json.Unmarshal(arr[0], &rawString); err == nil {
			return ConfigPatchRequest{Raw: rawString}, nil
		}
		var patch map[string]any
		if err := json.Unmarshal(arr[0], &patch); err != nil {
			return ConfigPatchRequest{}, fmt.Errorf("invalid params")
		}
		return ConfigPatchRequest{Patch: patch}, nil
	}
	req, err := decodeMethodParams[ConfigPatchRequest](params)
	if err != nil {
		return ConfigPatchRequest{}, err
	}
	if req.Raw != "" || len(req.Patch) > 0 {
		return req, nil
	}
	patch, err := decodeMethodParams[map[string]any](params)
	if err != nil {
		return ConfigPatchRequest{}, err
	}
	return ConfigPatchRequest{Patch: patch}, nil
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
			expectedVersionSet, err := decodeWritePrecondition(arr[2], &req.ExpectedVersion, &req.ExpectedEvent, nil)
			if err != nil {
				return ListPutRequest{}, fmt.Errorf("invalid params")
			}
			req.ExpectedVersionSet = expectedVersionSet
		}
		return req, nil
	}
	params = normalizeObjectParamAliases(params)
	type listPutCompatRequest struct {
		Name            string   `json:"name"`
		Items           []string `json:"items"`
		ExpectedVersion *int     `json:"expected_version,omitempty"`
		ExpectedEvent   string   `json:"expected_event,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat listPutCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return ListPutRequest{}, fmt.Errorf("invalid params")
	}
	req := ListPutRequest{Name: compat.Name, Items: compat.Items, ExpectedEvent: compat.ExpectedEvent}
	if compat.ExpectedVersion != nil {
		req.ExpectedVersionSet = true
		req.ExpectedVersion = *compat.ExpectedVersion
	}
	return req, nil
}

func DecodeTalkConfigParams(params json.RawMessage) (TalkConfigRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return TalkConfigRequest{}, nil
	}
	return decodeMethodParams[TalkConfigRequest](params)
}

func DecodeConfigDocFromRaw(raw string) (state.ConfigDoc, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return state.ConfigDoc{}, fmt.Errorf("raw is required")
	}
	var cfg state.ConfigDoc
	if err := json.Unmarshal([]byte(trimmed), &cfg); err != nil {
		return state.ConfigDoc{}, fmt.Errorf("invalid raw config")
	}
	return cfg, nil
}

func DecodeConfigPatchFromRaw(raw string) (map[string]any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("raw is required")
	}
	var patch map[string]any
	if err := json.Unmarshal([]byte(trimmed), &patch); err != nil {
		return nil, fmt.Errorf("invalid raw patch")
	}
	if len(patch) == 0 {
		return nil, fmt.Errorf("patch is required")
	}
	return patch, nil
}
