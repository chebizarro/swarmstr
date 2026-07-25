package methods

// Param schemas for the node.* plugin/skills surface (swarmstr-kmhu, BUCKET 3):
// node.pluginSurface.refresh / node.pluginTools.update / node.skills.update.
// These mirror OpenClaw's node-scoped surface ops. In swarmstr the operation is
// delivered to a paired+active node over the durable node pending-command queue
// (internal/gateway/nodepending) — the same channel node.invoke /
// node.pending.enqueue use — and the node applies it when it next pulls.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// maxNodeSurfaceTTLMS bounds how long a queued node-surface command lives.
const maxNodeSurfaceTTLMS = 24 * 60 * 60 * 1000

// NodePluginSurfaceRefreshRequest asks a node to re-scan its plugin surface.
type NodePluginSurfaceRefreshRequest struct {
	NodeID         string `json:"node_id"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	TTLMS          int    `json:"ttl_ms,omitempty"`
}

func (r NodePluginSurfaceRefreshRequest) Normalize() (NodePluginSurfaceRefreshRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.IdempotencyKey = strings.TrimSpace(r.IdempotencyKey)
	if r.NodeID == "" {
		return r, fmt.Errorf("node.pluginSurface.refresh: node_id is required")
	}
	if r.TTLMS < 0 {
		return r, fmt.Errorf("node.pluginSurface.refresh: ttl_ms must not be negative")
	}
	if r.TTLMS > maxNodeSurfaceTTLMS {
		r.TTLMS = maxNodeSurfaceTTLMS
	}
	return r, nil
}

func DecodeNodePluginSurfaceRefreshParams(params json.RawMessage) (NodePluginSurfaceRefreshRequest, error) {
	return decodeMethodParams[NodePluginSurfaceRefreshRequest](params)
}

// NodePluginToolsUpdateRequest pushes an updated plugin-tool surface to a node.
type NodePluginToolsUpdateRequest struct {
	NodeID         string `json:"node_id"`
	Tools          []any  `json:"tools,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	TTLMS          int    `json:"ttl_ms,omitempty"`
}

func (r NodePluginToolsUpdateRequest) Normalize() (NodePluginToolsUpdateRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.IdempotencyKey = strings.TrimSpace(r.IdempotencyKey)
	if r.NodeID == "" {
		return r, fmt.Errorf("node.pluginTools.update: node_id is required")
	}
	if r.TTLMS < 0 {
		return r, fmt.Errorf("node.pluginTools.update: ttl_ms must not be negative")
	}
	if r.TTLMS > maxNodeSurfaceTTLMS {
		r.TTLMS = maxNodeSurfaceTTLMS
	}
	return r, nil
}

func DecodeNodePluginToolsUpdateParams(params json.RawMessage) (NodePluginToolsUpdateRequest, error) {
	return decodeMethodParams[NodePluginToolsUpdateRequest](params)
}

// NodeSkillsUpdateRequest pushes an updated skills surface to a node.
type NodeSkillsUpdateRequest struct {
	NodeID         string `json:"node_id"`
	Skills         []any  `json:"skills,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	TTLMS          int    `json:"ttl_ms,omitempty"`
}

func (r NodeSkillsUpdateRequest) Normalize() (NodeSkillsUpdateRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.IdempotencyKey = strings.TrimSpace(r.IdempotencyKey)
	if r.NodeID == "" {
		return r, fmt.Errorf("node.skills.update: node_id is required")
	}
	if r.TTLMS < 0 {
		return r, fmt.Errorf("node.skills.update: ttl_ms must not be negative")
	}
	if r.TTLMS > maxNodeSurfaceTTLMS {
		r.TTLMS = maxNodeSurfaceTTLMS
	}
	return r, nil
}

func DecodeNodeSkillsUpdateParams(params json.RawMessage) (NodeSkillsUpdateRequest, error) {
	return decodeMethodParams[NodeSkillsUpdateRequest](params)
}
