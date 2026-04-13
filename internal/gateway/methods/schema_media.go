package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type TalkConfigRequest struct {
	IncludeSecrets bool `json:"includeSecrets,omitempty"`
}

type TalkModeRequest struct {
	Mode string `json:"mode"`
}

type BrowserRequestRequest struct {
	Method    string         `json:"method"`
	Path      string         `json:"path"`
	Query     map[string]any `json:"query,omitempty"`
	Body      any            `json:"body,omitempty"`
	TimeoutMS int            `json:"timeout_ms,omitempty"`
}

type VoicewakeGetRequest struct{}

type VoicewakeSetRequest struct {
	Triggers []string `json:"triggers"`
}

type TTSStatusRequest struct{}

type TTSProvidersRequest struct{}

type TTSSetProviderRequest struct {
	Provider string `json:"provider"`
}

type TTSEnableRequest struct{}

type TTSDisableRequest struct{}

type TTSConvertRequest struct {
	Text     string `json:"text"`
	Provider string `json:"provider,omitempty"`
	Voice    string `json:"voice,omitempty"`
}

func (r TalkConfigRequest) Normalize() (TalkConfigRequest, error) { return r, nil }

func (r TalkModeRequest) Normalize() (TalkModeRequest, error) {
	r.Mode = strings.TrimSpace(r.Mode)
	if r.Mode == "" {
		return r, fmt.Errorf("mode is required")
	}
	return r, nil
}

func (r BrowserRequestRequest) Normalize() (BrowserRequestRequest, error) {
	r.Method = strings.ToUpper(strings.TrimSpace(r.Method))
	r.Path = strings.TrimSpace(r.Path)
	if r.Method == "" || r.Path == "" {
		return r, fmt.Errorf("method and path are required")
	}
	switch r.Method {
	case "GET", "POST", "DELETE":
	default:
		return r, fmt.Errorf("method must be GET, POST, or DELETE")
	}
	if r.TimeoutMS < 0 {
		return r, fmt.Errorf("timeoutMs cannot be negative")
	}
	if r.TimeoutMS > 0 {
		r.TimeoutMS = normalizeLimit(r.TimeoutMS, 5_000, 120_000)
	}
	return r, nil
}

func (r VoicewakeGetRequest) Normalize() (VoicewakeGetRequest, error) { return r, nil }

func (r VoicewakeSetRequest) Normalize() (VoicewakeSetRequest, error) {
	clean := make([]string, 0, len(r.Triggers))
	for _, trigger := range r.Triggers {
		trigger = strings.TrimSpace(trigger)
		if trigger == "" {
			continue
		}
		clean = append(clean, trigger)
	}
	r.Triggers = clean
	return r, nil
}

func (r TTSStatusRequest) Normalize() (TTSStatusRequest, error) { return r, nil }

func (r TTSProvidersRequest) Normalize() (TTSProvidersRequest, error) { return r, nil }

func (r TTSSetProviderRequest) Normalize() (TTSSetProviderRequest, error) {
	r.Provider = strings.TrimSpace(r.Provider)
	if r.Provider == "" {
		return r, fmt.Errorf("provider is required")
	}
	return r, nil
}

func (r TTSEnableRequest) Normalize() (TTSEnableRequest, error) { return r, nil }

func (r TTSDisableRequest) Normalize() (TTSDisableRequest, error) { return r, nil }

func (r TTSConvertRequest) Normalize() (TTSConvertRequest, error) {
	r.Text = strings.TrimSpace(r.Text)
	r.Provider = strings.TrimSpace(r.Provider)
	r.Voice = strings.TrimSpace(r.Voice)
	if r.Text == "" {
		return r, fmt.Errorf("text is required")
	}
	return r, nil
}

func DecodeTalkModeParams(params json.RawMessage) (TalkModeRequest, error) {
	return decodeMethodParams[TalkModeRequest](params)
}

func DecodeBrowserRequestParams(params json.RawMessage) (BrowserRequestRequest, error) {
	return decodeMethodParams[BrowserRequestRequest](params)
}

func DecodeVoicewakeGetParams(params json.RawMessage) (VoicewakeGetRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return VoicewakeGetRequest{}, nil
	}
	return decodeMethodParams[VoicewakeGetRequest](params)
}

func DecodeVoicewakeSetParams(params json.RawMessage) (VoicewakeSetRequest, error) {
	return decodeMethodParams[VoicewakeSetRequest](params)
}

func DecodeTTSStatusParams(params json.RawMessage) (TTSStatusRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return TTSStatusRequest{}, nil
	}
	return decodeMethodParams[TTSStatusRequest](params)
}

func DecodeTTSProvidersParams(params json.RawMessage) (TTSProvidersRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return TTSProvidersRequest{}, nil
	}
	return decodeMethodParams[TTSProvidersRequest](params)
}

func DecodeTTSSetProviderParams(params json.RawMessage) (TTSSetProviderRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return TTSSetProviderRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return TTSSetProviderRequest{}, fmt.Errorf("invalid params")
		}
		provider, ok := arr[0].(string)
		if !ok {
			return TTSSetProviderRequest{}, fmt.Errorf("invalid params")
		}
		return TTSSetProviderRequest{Provider: provider}, nil
	}
	return decodeMethodParams[TTSSetProviderRequest](params)
}

func DecodeTTSEnableParams(params json.RawMessage) (TTSEnableRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return TTSEnableRequest{}, nil
	}
	return decodeMethodParams[TTSEnableRequest](params)
}

func DecodeTTSDisableParams(params json.RawMessage) (TTSDisableRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return TTSDisableRequest{}, nil
	}
	return decodeMethodParams[TTSDisableRequest](params)
}

func DecodeTTSConvertParams(params json.RawMessage) (TTSConvertRequest, error) {
	return decodeMethodParams[TTSConvertRequest](params)
}
