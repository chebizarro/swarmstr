package methods

import (
	"encoding/json"
	"fmt"
	"strings"
)

// mcp.app.* method schemas. Every method addresses one minted MCP-App view
// ({sessionKey, viewId}); views are process-local capability handles minted
// by internal/gateway/mcpapp when an agent MCP tool call returns a ui://
// resource. Params mirror the OpenClaw mcp.app.* wire contract.

const maxMcpAppModelContextBytes = 32 * 1024

// McpAppViewRef addresses one minted view.
type McpAppViewRef struct {
	SessionKey string `json:"sessionKey"`
	ViewID     string `json:"viewId"`
}

func (r McpAppViewRef) normalizeRef(method string) (McpAppViewRef, error) {
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	r.ViewID = strings.TrimSpace(r.ViewID)
	if r.SessionKey == "" {
		return r, fmt.Errorf("invalid %s params: sessionKey is required", method)
	}
	if r.ViewID == "" {
		return r, fmt.Errorf("invalid %s params: viewId is required", method)
	}
	return r, nil
}

type McpAppViewRequest struct {
	McpAppViewRef
}

func (r McpAppViewRequest) Normalize() (McpAppViewRequest, error) {
	ref, err := r.normalizeRef(MethodMcpAppView)
	r.McpAppViewRef = ref
	return r, err
}

// McpAppListRequest covers the three cursor-paginated list methods.
type McpAppListRequest struct {
	McpAppViewRef
	Cursor string `json:"cursor,omitempty"`
}

func (r McpAppListRequest) normalizeFor(method string) (McpAppListRequest, error) {
	ref, err := r.normalizeRef(method)
	r.McpAppViewRef = ref
	r.Cursor = strings.TrimSpace(r.Cursor)
	return r, err
}

type McpAppCallToolRequest struct {
	McpAppViewRef
	ToolName  string         `json:"toolName"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

func (r McpAppCallToolRequest) Normalize() (McpAppCallToolRequest, error) {
	ref, err := r.normalizeRef(MethodMcpAppCallTool)
	if err != nil {
		return r, err
	}
	r.McpAppViewRef = ref
	r.ToolName = strings.TrimSpace(r.ToolName)
	if r.ToolName == "" {
		return r, fmt.Errorf("invalid %s params: toolName is required", MethodMcpAppCallTool)
	}
	return r, nil
}

type McpAppReadResourceRequest struct {
	McpAppViewRef
	URI string `json:"uri"`
}

func (r McpAppReadResourceRequest) Normalize() (McpAppReadResourceRequest, error) {
	ref, err := r.normalizeRef(MethodMcpAppReadResource)
	if err != nil {
		return r, err
	}
	r.McpAppViewRef = ref
	r.URI = strings.TrimSpace(r.URI)
	if r.URI == "" {
		return r, fmt.Errorf("invalid %s params: uri is required", MethodMcpAppReadResource)
	}
	return r, nil
}

// McpAppUpdateModelContextRequest carries widget-supplied context for the
// owning session's model.
type McpAppUpdateModelContextRequest struct {
	McpAppViewRef
	Content json.RawMessage `json:"content,omitempty"`
}

func (r McpAppUpdateModelContextRequest) Normalize() (McpAppUpdateModelContextRequest, error) {
	ref, err := r.normalizeRef(MethodMcpAppUpdateModelContext)
	if err != nil {
		return r, err
	}
	r.McpAppViewRef = ref
	if len(r.Content) > maxMcpAppModelContextBytes {
		return r, fmt.Errorf("invalid %s params: content exceeds %d UTF-8 bytes", MethodMcpAppUpdateModelContext, maxMcpAppModelContextBytes)
	}
	return r, nil
}

func DecodeMcpAppViewParams(params json.RawMessage) (McpAppViewRequest, error) {
	return decodeMethodParams[McpAppViewRequest](params)
}

func DecodeMcpAppListParams(params json.RawMessage, method string) (McpAppListRequest, error) {
	req, err := decodeMethodParams[McpAppListRequest](params)
	if err != nil {
		return req, err
	}
	return req.normalizeFor(method)
}

func DecodeMcpAppCallToolParams(params json.RawMessage) (McpAppCallToolRequest, error) {
	return decodeMethodParams[McpAppCallToolRequest](params)
}

func DecodeMcpAppReadResourceParams(params json.RawMessage) (McpAppReadResourceRequest, error) {
	return decodeMethodParams[McpAppReadResourceRequest](params)
}

func DecodeMcpAppUpdateModelContextParams(params json.RawMessage) (McpAppUpdateModelContextRequest, error) {
	return decodeMethodParams[McpAppUpdateModelContextRequest](params)
}
