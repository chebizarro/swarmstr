package methods

import (
	"encoding/json"
	"strings"
)

// FSListDirRequest lists directories for the new-session folder picker.
// Metiq deviation from OpenClaw: listing is rooted at the agent workspace
// (os.Root containment) instead of the whole gateway host filesystem.
type FSListDirRequest struct {
	// Path is the directory to list. Both workspace-relative paths and
	// absolute paths inside the workspace root are accepted; omitted means
	// the workspace root itself.
	Path string `json:"path,omitempty"`
	// NodeID selects a connected node host to browse. Metiq does not proxy
	// directory listings to nodes yet; a non-empty value is rejected.
	NodeID string `json:"node_id,omitempty"`
	// AgentID selects the agent workspace to browse; omitted means the
	// default workspace.
	AgentID string `json:"agent_id,omitempty"`
}

// FSDirEntry is one directory in a listing.
type FSDirEntry struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Hidden bool   `json:"hidden,omitempty"`
}

// FSListDirResult mirrors the OpenClaw fs.listDir result shape. Paths are
// workspace-rooted ("/" is the workspace root).
type FSListDirResult struct {
	Path    string       `json:"path"`
	Parent  string       `json:"parent,omitempty"`
	Home    string       `json:"home"`
	Entries []FSDirEntry `json:"entries"`
}

func (r FSListDirRequest) Normalize() (FSListDirRequest, error) {
	r.Path = strings.TrimSpace(r.Path)
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.AgentID = strings.TrimSpace(r.AgentID)
	return r, nil
}

func DecodeFSListDirParams(params json.RawMessage) (FSListDirRequest, error) {
	return decodeMethodParams[FSListDirRequest](params)
}
