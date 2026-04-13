package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type MCPListRequest struct{}

type MCPGetRequest struct {
	Server string `json:"server"`
}

type MCPPutRequest struct {
	Server string         `json:"server"`
	Config map[string]any `json:"config"`
}

type MCPRemoveRequest struct {
	Server string `json:"server"`
}

type MCPTestRequest struct {
	Server    string         `json:"server"`
	Config    map[string]any `json:"config,omitempty"`
	TimeoutMS int            `json:"timeout_ms,omitempty"`
}

type MCPReconnectRequest struct {
	Server string `json:"server"`
}

type MCPAuthStartRequest struct {
	Server       string `json:"server"`
	ClientSecret string `json:"client_secret,omitempty"`
	TimeoutMS    int    `json:"timeout_ms,omitempty"`
}

type MCPAuthRefreshRequest struct {
	Server string `json:"server"`
}

type MCPAuthClearRequest struct {
	Server string `json:"server"`
}

type SecretsReloadRequest struct{}

type SecretsResolveRequest struct {
	CommandName string   `json:"commandName,omitempty"`
	TargetIDs   []string `json:"targetIds"`
}

func (r SecretsReloadRequest) Normalize() (SecretsReloadRequest, error) { return r, nil }

func (r SecretsResolveRequest) Normalize() (SecretsResolveRequest, error) {
	r.CommandName = strings.TrimSpace(r.CommandName)
	clean := make([]string, 0, len(r.TargetIDs))
	for _, id := range r.TargetIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		clean = append(clean, id)
	}
	r.TargetIDs = clean
	if len(r.TargetIDs) == 0 {
		return r, fmt.Errorf("targetIds is required")
	}
	return r, nil
}

func (r MCPListRequest) Normalize() (MCPListRequest, error) { return r, nil }

func (r MCPGetRequest) Normalize() (MCPGetRequest, error) {
	r.Server = strings.TrimSpace(r.Server)
	if r.Server == "" {
		return r, fmt.Errorf("server is required")
	}
	return r, nil
}

func (r MCPPutRequest) Normalize() (MCPPutRequest, error) {
	r.Server = strings.TrimSpace(r.Server)
	if r.Server == "" {
		return r, fmt.Errorf("server is required")
	}
	if len(r.Config) == 0 {
		return r, fmt.Errorf("config is required")
	}
	return r, nil
}

func (r MCPRemoveRequest) Normalize() (MCPRemoveRequest, error) {
	r.Server = strings.TrimSpace(r.Server)
	if r.Server == "" {
		return r, fmt.Errorf("server is required")
	}
	return r, nil
}

func (r MCPTestRequest) Normalize() (MCPTestRequest, error) {
	r.Server = strings.TrimSpace(r.Server)
	if r.Server == "" {
		return r, fmt.Errorf("server is required")
	}
	if r.TimeoutMS < 0 {
		return r, fmt.Errorf("timeout_ms must be >= 0")
	}
	return r, nil
}

func (r MCPReconnectRequest) Normalize() (MCPReconnectRequest, error) {
	r.Server = strings.TrimSpace(r.Server)
	if r.Server == "" {
		return r, fmt.Errorf("server is required")
	}
	return r, nil
}

func (r MCPAuthStartRequest) Normalize() (MCPAuthStartRequest, error) {
	r.Server = strings.TrimSpace(r.Server)
	r.ClientSecret = strings.TrimSpace(r.ClientSecret)
	if r.Server == "" {
		return r, fmt.Errorf("server is required")
	}
	if r.TimeoutMS < 0 {
		return r, fmt.Errorf("timeout_ms must be >= 0")
	}
	return r, nil
}

func (r MCPAuthRefreshRequest) Normalize() (MCPAuthRefreshRequest, error) {
	r.Server = strings.TrimSpace(r.Server)
	if r.Server == "" {
		return r, fmt.Errorf("server is required")
	}
	return r, nil
}

func (r MCPAuthClearRequest) Normalize() (MCPAuthClearRequest, error) {
	r.Server = strings.TrimSpace(r.Server)
	if r.Server == "" {
		return r, fmt.Errorf("server is required")
	}
	return r, nil
}

func DecodeMCPListParams(params json.RawMessage) (MCPListRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return MCPListRequest{}, nil
	}
	return decodeMethodParams[MCPListRequest](params)
}

func DecodeMCPGetParams(params json.RawMessage) (MCPGetRequest, error) {
	return decodeMethodParams[MCPGetRequest](params)
}

func DecodeMCPPutParams(params json.RawMessage) (MCPPutRequest, error) {
	return decodeMethodParams[MCPPutRequest](params)
}

func DecodeMCPRemoveParams(params json.RawMessage) (MCPRemoveRequest, error) {
	return decodeMethodParams[MCPRemoveRequest](params)
}

func DecodeMCPTestParams(params json.RawMessage) (MCPTestRequest, error) {
	return decodeMethodParams[MCPTestRequest](params)
}

func DecodeMCPReconnectParams(params json.RawMessage) (MCPReconnectRequest, error) {
	return decodeMethodParams[MCPReconnectRequest](params)
}

func DecodeMCPAuthStartParams(params json.RawMessage) (MCPAuthStartRequest, error) {
	return decodeMethodParams[MCPAuthStartRequest](params)
}

func DecodeMCPAuthRefreshParams(params json.RawMessage) (MCPAuthRefreshRequest, error) {
	return decodeMethodParams[MCPAuthRefreshRequest](params)
}

func DecodeMCPAuthClearParams(params json.RawMessage) (MCPAuthClearRequest, error) {
	return decodeMethodParams[MCPAuthClearRequest](params)
}

func DecodeSecretsReloadParams(params json.RawMessage) (SecretsReloadRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return SecretsReloadRequest{}, nil
	}
	return decodeMethodParams[SecretsReloadRequest](params)
}

func DecodeSecretsResolveParams(params json.RawMessage) (SecretsResolveRequest, error) {
	return decodeMethodParams[SecretsResolveRequest](params)
}
