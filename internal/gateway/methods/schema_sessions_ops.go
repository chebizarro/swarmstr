package methods

// Param schemas for the sessions.* operational long tail (swarmstr-kmhu,
// BUCKET 2): sessions.pluginPatch / sessions.cleanup / sessions.diff. Shapes
// mirror OpenClaw src/gateway/server-methods/sessions*.ts, adapted to
// swarmstr's session subsystem (docs/transcript repositories + the durable
// compaction-checkpoint DAG).

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SessionsPluginPatchRequest applies a plugin-provided mutation to one session.
// Unlike sessions.patch (which merges caller-supplied keys into the top-level
// session meta), pluginPatch validates the owning plugin and writes the patch
// into a plugin-namespaced meta subtree (meta.plugins.<pluginId>), so plugin
// state can never collide with core session meta.
type SessionsPluginPatchRequest struct {
	SessionID string         `json:"session_id"`
	PluginID  string         `json:"plugin_id"`
	Patch     map[string]any `json:"patch"`
	// Replace, when true, overwrites the plugin's namespaced subtree instead of
	// shallow-merging the patch into it.
	Replace bool `json:"replace,omitempty"`
}

func (r SessionsPluginPatchRequest) Normalize() (SessionsPluginPatchRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.PluginID = strings.TrimSpace(r.PluginID)
	if r.SessionID == "" {
		return r, fmt.Errorf("sessions.pluginPatch: session_id is required")
	}
	if r.PluginID == "" {
		return r, fmt.Errorf("sessions.pluginPatch: plugin_id is required")
	}
	if r.Patch == nil {
		return r, fmt.Errorf("sessions.pluginPatch: patch is required")
	}
	return r, nil
}

func DecodeSessionsPluginPatchParams(params json.RawMessage) (SessionsPluginPatchRequest, error) {
	return decodeMethodParams[SessionsPluginPatchRequest](params)
}

// SessionsCleanupRequest GCs terminal/stale sessions. This complements
// sessions.prune (which selects by transcript age and tombstones): cleanup
// sweeps sessions already in a terminal state — tombstoned (meta.deleted) past
// an optional grace period, and optionally long-idle sessions — and removes
// residual transcript entries, reporting what was collected.
type SessionsCleanupRequest struct {
	// OlderThanDays is a grace period applied to tombstoned sessions: only
	// sessions whose deleted_at is older than this are collected. 0 = no grace.
	OlderThanDays int `json:"older_than_days,omitempty"`
	// IncludeIdle also collects sessions with no inbound activity for IdleDays.
	IncludeIdle bool `json:"include_idle,omitempty"`
	IdleDays    int  `json:"idle_days,omitempty"`
	// DryRun reports what would be collected without mutating anything.
	DryRun bool `json:"dry_run,omitempty"`
}

func (r SessionsCleanupRequest) Normalize() (SessionsCleanupRequest, error) {
	if r.OlderThanDays < 0 {
		return r, fmt.Errorf("sessions.cleanup: older_than_days must not be negative")
	}
	if r.IdleDays < 0 {
		return r, fmt.Errorf("sessions.cleanup: idle_days must not be negative")
	}
	if r.IncludeIdle && r.IdleDays <= 0 {
		return r, fmt.Errorf("sessions.cleanup: idle_days must be > 0 when include_idle is set")
	}
	return r, nil
}

func DecodeSessionsCleanupParams(params json.RawMessage) (SessionsCleanupRequest, error) {
	return decodeMethodParams[SessionsCleanupRequest](params)
}

// SessionsDiffRequest diffs two durable snapshots of one session — compaction
// checkpoints on the session's DAG, addressed by checkpoint id.
type SessionsDiffRequest struct {
	SessionID string `json:"session_id"`
	From      string `json:"from"`
	To        string `json:"to"`
}

func (r SessionsDiffRequest) Normalize() (SessionsDiffRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.From = strings.TrimSpace(r.From)
	r.To = strings.TrimSpace(r.To)
	if r.SessionID == "" {
		return r, fmt.Errorf("sessions.diff: session_id is required")
	}
	if r.From == "" || r.To == "" {
		return r, fmt.Errorf("sessions.diff: both from and to checkpoint ids are required")
	}
	return r, nil
}

func DecodeSessionsDiffParams(params json.RawMessage) (SessionsDiffRequest, error) {
	return decodeMethodParams[SessionsDiffRequest](params)
}
