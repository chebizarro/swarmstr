package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

type AgentsListRequest struct {
	Limit int `json:"limit,omitempty"`
}

type AgentsCreateRequest struct {
	AgentID   string         `json:"agent_id"`
	Name      string         `json:"name,omitempty"`
	Workspace string         `json:"workspace,omitempty"`
	Model     string         `json:"model,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
}

type AgentsUpdateRequest struct {
	AgentID   string         `json:"agent_id"`
	Name      string         `json:"name,omitempty"`
	Workspace string         `json:"workspace,omitempty"`
	Model     string         `json:"model,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
}

type AgentsDeleteRequest struct {
	AgentID string `json:"agent_id"`
}

type AgentsFilesListRequest struct {
	AgentID string `json:"agent_id"`
	Limit   int    `json:"limit,omitempty"`
}

type AgentsFilesGetRequest struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
}

type AgentsFilesSetRequest struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// AgentsAssignRequest routes a session (peer pubkey or WS client ID) to a
// specific agent.  Subsequent DM/WS messages from that session are handled by
// the named agent's runtime.
type AgentsAssignRequest struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
}

// AgentsUnassignRequest removes a session→agent assignment; the session falls
// back to the default "main" agent.
type AgentsUnassignRequest struct {
	SessionID string `json:"session_id"`
}

// AgentsActiveRequest requests the list of active agent runtimes and their
// session assignments.
type AgentsActiveRequest struct {
	Limit int `json:"limit,omitempty"`
}

type ModelsListRequest struct{}

type ToolsCatalogRequest struct {
	AgentID        string  `json:"agent_id,omitempty"`
	IncludePlugins *bool   `json:"include_plugins,omitempty"`
	Profile        *string `json:"profile,omitempty"`
}

type ToolsProfileGetRequest struct {
	AgentID string `json:"agent_id,omitempty"`
}

type ToolsProfileSetRequest struct {
	AgentID string `json:"agent_id,omitempty"`
	Profile string `json:"profile"`
}

type SkillsStatusRequest struct {
	AgentID string `json:"agent_id,omitempty"`
}

type SkillsBinsRequest struct{}

type SkillsInstallRequest struct {
	AgentID   string `json:"agent_id,omitempty"`
	Name      string `json:"name"`
	InstallID string `json:"install_id"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type SkillsUpdateRequest struct {
	AgentID  string            `json:"agent_id,omitempty"`
	SkillKey string            `json:"skill_key"`
	Enabled  *bool             `json:"enabled,omitempty"`
	APIKey   *string           `json:"api_key,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
}

type PluginsInstallRequest struct {
	PluginID        string         `json:"plugin_id"`
	Install         map[string]any `json:"install"`
	EnableEntry     *bool          `json:"enable_entry,omitempty"`
	IncludeLoadPath *bool          `json:"include_load_path,omitempty"`
}

type PluginsUninstallRequest struct {
	PluginID string `json:"plugin_id"`
}

type PluginsUpdateRequest struct {
	PluginIDs []string `json:"plugin_ids,omitempty"`
	DryRun    bool     `json:"dry_run,omitempty"`
}

// PluginsRegistryListRequest fetches the full plugin index from a remote registry.
type PluginsRegistryListRequest struct {
	// RegistryURL overrides the registry URL configured in the daemon config.
	// If empty, the daemon's configured registry URL is used.
	RegistryURL string `json:"registry_url,omitempty"`
}

// PluginsRegistryGetRequest fetches details for a single plugin from the registry.
type PluginsRegistryGetRequest struct {
	// PluginID is the plugin identifier to look up.
	PluginID string `json:"plugin_id"`
	// RegistryURL overrides the configured registry URL.
	RegistryURL string `json:"registry_url,omitempty"`
}

// PluginsRegistrySearchRequest searches the remote registry by keyword or tag.
type PluginsRegistrySearchRequest struct {
	// Query is a keyword to match against plugin name, description, and tags.
	Query string `json:"query,omitempty"`
	// Tag filters results by a specific tag.
	Tag string `json:"tag,omitempty"`
	// RegistryURL overrides the configured registry URL.
	RegistryURL string `json:"registry_url,omitempty"`
}

func (r AgentsListRequest) Normalize() (AgentsListRequest, error) {
	r.Limit = normalizeLimit(r.Limit, 100, 500)
	return r, nil
}

func (r AgentsCreateRequest) Normalize() (AgentsCreateRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.Name = strings.TrimSpace(r.Name)
	r.Workspace = strings.TrimSpace(r.Workspace)
	r.Model = strings.TrimSpace(r.Model)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	if r.Meta == nil {
		r.Meta = map[string]any{}
	}
	return r, nil
}

func (r AgentsUpdateRequest) Normalize() (AgentsUpdateRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.Name = strings.TrimSpace(r.Name)
	r.Workspace = strings.TrimSpace(r.Workspace)
	r.Model = strings.TrimSpace(r.Model)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	if r.Meta == nil {
		r.Meta = map[string]any{}
	}
	return r, nil
}

func (r AgentsDeleteRequest) Normalize() (AgentsDeleteRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	return r, nil
}

func (r AgentsFilesListRequest) Normalize() (AgentsFilesListRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	r.Limit = normalizeLimit(r.Limit, 200, 1000)
	return r, nil
}

func (r AgentsFilesGetRequest) Normalize() (AgentsFilesGetRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.Name = strings.TrimSpace(r.Name)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	if !isSafeAgentFileName(r.Name) {
		return r, fmt.Errorf("invalid file name")
	}
	return r, nil
}

func (r AgentsFilesSetRequest) Normalize() (AgentsFilesSetRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.Name = strings.TrimSpace(r.Name)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	if !isSafeAgentFileName(r.Name) {
		return r, fmt.Errorf("invalid file name")
	}
	return r, nil
}

func (r AgentsAssignRequest) Normalize() (AgentsAssignRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	return r, nil
}

func (r AgentsUnassignRequest) Normalize() (AgentsUnassignRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	return r, nil
}

func (r AgentsActiveRequest) Normalize() (AgentsActiveRequest, error) {
	r.Limit = normalizeLimit(r.Limit, 100, 500)
	return r, nil
}

func (r ModelsListRequest) Normalize() (ModelsListRequest, error) {
	return r, nil
}

func (r ToolsCatalogRequest) Normalize() (ToolsCatalogRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	return r, nil
}

func (r ToolsProfileGetRequest) Normalize() (ToolsProfileGetRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	return r, nil
}

func (r ToolsProfileSetRequest) Normalize() (ToolsProfileSetRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.Profile = strings.TrimSpace(strings.ToLower(r.Profile))
	if r.Profile == "" {
		return r, fmt.Errorf("profile is required")
	}
	return r, nil
}

func (r SkillsStatusRequest) Normalize() (SkillsStatusRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	return r, nil
}

func (r SkillsBinsRequest) Normalize() (SkillsBinsRequest, error) {
	return r, nil
}

func (r SkillsInstallRequest) Normalize() (SkillsInstallRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.Name = strings.TrimSpace(r.Name)
	r.InstallID = strings.TrimSpace(r.InstallID)
	if r.Name == "" {
		return r, fmt.Errorf("name is required")
	}
	if r.InstallID == "" {
		return r, fmt.Errorf("install_id is required")
	}
	r.TimeoutMS = normalizeLimit(r.TimeoutMS, 120_000, 600_000)
	return r, nil
}

func (r SkillsUpdateRequest) Normalize() (SkillsUpdateRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.SkillKey = strings.ToLower(strings.TrimSpace(r.SkillKey))
	if r.SkillKey == "" {
		return r, fmt.Errorf("skill_key is required")
	}
	if r.Env == nil {
		r.Env = map[string]string{}
	} else {
		cleaned := make(map[string]string, len(r.Env))
		for key, value := range r.Env {
			trimmedKey := strings.TrimSpace(key)
			if trimmedKey == "" {
				continue
			}
			trimmedValue := strings.TrimSpace(value)
			if trimmedValue == "" {
				continue
			}
			cleaned[trimmedKey] = trimmedValue
		}
		r.Env = cleaned
	}
	if r.APIKey != nil {
		trimmed := strings.TrimSpace(*r.APIKey)
		r.APIKey = &trimmed
	}
	return r, nil
}

func (r PluginsInstallRequest) Normalize() (PluginsInstallRequest, error) {
	r.PluginID = strings.ToLower(strings.TrimSpace(r.PluginID))
	if r.PluginID == "" {
		return r, fmt.Errorf("plugin_id is required")
	}
	if !isSafePluginID(r.PluginID) {
		return r, fmt.Errorf("invalid plugin_id")
	}
	if r.Install == nil || len(r.Install) == 0 {
		return r, fmt.Errorf("install is required")
	}
	if r.EnableEntry == nil {
		v := true
		r.EnableEntry = &v
	}
	if r.IncludeLoadPath == nil {
		v := true
		r.IncludeLoadPath = &v
	}
	return r, nil
}

func (r PluginsUninstallRequest) Normalize() (PluginsUninstallRequest, error) {
	r.PluginID = strings.ToLower(strings.TrimSpace(r.PluginID))
	if r.PluginID == "" {
		return r, fmt.Errorf("plugin_id is required")
	}
	if !isSafePluginID(r.PluginID) {
		return r, fmt.Errorf("invalid plugin_id")
	}
	return r, nil
}

func (r PluginsUpdateRequest) Normalize() (PluginsUpdateRequest, error) {
	r.PluginIDs = compactStringSlice(r.PluginIDs)
	return r, nil
}

func DecodeAgentsListParams(params json.RawMessage) (AgentsListRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return AgentsListRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 1 {
			return AgentsListRequest{}, fmt.Errorf("invalid params")
		}
		req := AgentsListRequest{}
		if len(arr) == 1 {
			switch v := arr[0].(type) {
			case float64:
				if math.Trunc(v) != v {
					return AgentsListRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			default:
				return AgentsListRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	if len(bytes.TrimSpace(params)) == 0 {
		return AgentsListRequest{}, nil
	}
	return decodeMethodParams[AgentsListRequest](params)
}

func DecodeAgentsCreateParams(params json.RawMessage) (AgentsCreateRequest, error) {
	return decodeMethodParams[AgentsCreateRequest](params)
}

func DecodeAgentsUpdateParams(params json.RawMessage) (AgentsUpdateRequest, error) {
	return decodeMethodParams[AgentsUpdateRequest](params)
}

func DecodeAgentsDeleteParams(params json.RawMessage) (AgentsDeleteRequest, error) {
	return decodeMethodParams[AgentsDeleteRequest](params)
}

func DecodeAgentsAssignParams(params json.RawMessage) (AgentsAssignRequest, error) {
	return decodeMethodParams[AgentsAssignRequest](params)
}

func DecodeAgentsUnassignParams(params json.RawMessage) (AgentsUnassignRequest, error) {
	return decodeMethodParams[AgentsUnassignRequest](params)
}

func DecodeAgentsActiveParams(params json.RawMessage) (AgentsActiveRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return AgentsActiveRequest{}, nil
	}
	return decodeMethodParams[AgentsActiveRequest](params)
}

func DecodeAgentsFilesListParams(params json.RawMessage) (AgentsFilesListRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return AgentsFilesListRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return AgentsFilesListRequest{}, fmt.Errorf("invalid params")
		}
		agentID, ok := arr[0].(string)
		if !ok {
			return AgentsFilesListRequest{}, fmt.Errorf("invalid params")
		}
		req := AgentsFilesListRequest{AgentID: agentID}
		if len(arr) == 2 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return AgentsFilesListRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			default:
				return AgentsFilesListRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[AgentsFilesListRequest](params)
}

func DecodeAgentsFilesGetParams(params json.RawMessage) (AgentsFilesGetRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return AgentsFilesGetRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 2 {
			return AgentsFilesGetRequest{}, fmt.Errorf("invalid params")
		}
		agentID, ok := arr[0].(string)
		if !ok {
			return AgentsFilesGetRequest{}, fmt.Errorf("invalid params")
		}
		name, ok := arr[1].(string)
		if !ok {
			return AgentsFilesGetRequest{}, fmt.Errorf("invalid params")
		}
		return AgentsFilesGetRequest{AgentID: agentID, Name: name}, nil
	}
	return decodeMethodParams[AgentsFilesGetRequest](params)
}

func DecodeAgentsFilesSetParams(params json.RawMessage) (AgentsFilesSetRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return AgentsFilesSetRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 3 {
			return AgentsFilesSetRequest{}, fmt.Errorf("invalid params")
		}
		agentID, ok := arr[0].(string)
		if !ok {
			return AgentsFilesSetRequest{}, fmt.Errorf("invalid params")
		}
		name, ok := arr[1].(string)
		if !ok {
			return AgentsFilesSetRequest{}, fmt.Errorf("invalid params")
		}
		content, ok := arr[2].(string)
		if !ok {
			return AgentsFilesSetRequest{}, fmt.Errorf("invalid params")
		}
		return AgentsFilesSetRequest{AgentID: agentID, Name: name, Content: content}, nil
	}
	return decodeMethodParams[AgentsFilesSetRequest](params)
}

func DecodeModelsListParams(params json.RawMessage) (ModelsListRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return ModelsListRequest{}, nil
	}
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ModelsListRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 0 {
			return ModelsListRequest{}, fmt.Errorf("invalid params")
		}
		return ModelsListRequest{}, nil
	}
	return decodeMethodParams[ModelsListRequest](params)
}

func DecodeToolsCatalogParams(params json.RawMessage) (ToolsCatalogRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return ToolsCatalogRequest{}, nil
	}
	return decodeMethodParams[ToolsCatalogRequest](params)
}

func DecodeToolsProfileGetParams(params json.RawMessage) (ToolsProfileGetRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return ToolsProfileGetRequest{}, nil
	}
	return decodeMethodParams[ToolsProfileGetRequest](params)
}

func DecodeToolsProfileSetParams(params json.RawMessage) (ToolsProfileSetRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return ToolsProfileSetRequest{}, fmt.Errorf("profile is required")
	}
	return decodeMethodParams[ToolsProfileSetRequest](params)
}

func DecodeSkillsStatusParams(params json.RawMessage) (SkillsStatusRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return SkillsStatusRequest{}, nil
	}
	return decodeMethodParams[SkillsStatusRequest](params)
}

func DecodeSkillsBinsParams(params json.RawMessage) (SkillsBinsRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return SkillsBinsRequest{}, nil
	}
	return decodeMethodParams[SkillsBinsRequest](params)
}

func DecodeSkillsInstallParams(params json.RawMessage) (SkillsInstallRequest, error) {
	return decodeMethodParams[SkillsInstallRequest](params)
}

func DecodeSkillsUpdateParams(params json.RawMessage) (SkillsUpdateRequest, error) {
	return decodeMethodParams[SkillsUpdateRequest](params)
}

func DecodePluginsInstallParams(params json.RawMessage) (PluginsInstallRequest, error) {
	return decodeMethodParams[PluginsInstallRequest](params)
}

func DecodePluginsUninstallParams(params json.RawMessage) (PluginsUninstallRequest, error) {
	return decodeMethodParams[PluginsUninstallRequest](params)
}

func DecodePluginsUpdateParams(params json.RawMessage) (PluginsUpdateRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return PluginsUpdateRequest{}, nil
	}
	return decodeMethodParams[PluginsUpdateRequest](params)
}

func DecodePluginsRegistryListParams(params json.RawMessage) (PluginsRegistryListRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return PluginsRegistryListRequest{}, nil
	}
	return decodeMethodParams[PluginsRegistryListRequest](params)
}

func DecodePluginsRegistryGetParams(params json.RawMessage) (PluginsRegistryGetRequest, error) {
	return decodeMethodParams[PluginsRegistryGetRequest](params)
}

func DecodePluginsRegistrySearchParams(params json.RawMessage) (PluginsRegistrySearchRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return PluginsRegistrySearchRequest{}, nil
	}
	return decodeMethodParams[PluginsRegistrySearchRequest](params)
}
