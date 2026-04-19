// Package workspace provides the single canonical workspace directory resolver
// used by all subsystems (prompt bootstrap, filesystem tools, /context list,
// FLEET.md writer, memory scope, skills, hooks, etc.).
//
// Resolution order:
//
//  1. agents[].workspace_dir  — per-agent config (most specific)
//  2. METIQ_WORKSPACE env var — environment override (trumps config file)
//  3. extra.workspace.dir     — legacy global override
//  4. extra.workspace_dir     — alternate legacy key
//  5. extra.skills.workspace  — another legacy key
//  6. ~/.metiq/workspace      — canonical fallback
//
// All callers MUST use ResolveWorkspaceDir to avoid split-brain runtime.
package workspace

import (
	"os"
	"path/filepath"
	"strings"

	"metiq/internal/store/state"
)

// ResolveWorkspaceDir returns the canonical workspace directory for the given
// agent within the provided config.  agentID may be empty, in which case it
// is treated as "main".
func ResolveWorkspaceDir(cfg state.ConfigDoc, agentID string) string {
	if agentID == "" {
		agentID = "main"
	}

	// 1. Per-agent workspace_dir (highest priority).
	for _, ac := range cfg.Agents {
		// normalizeAgentID handles blank IDs in config (treated as "main").
		if normalizeAgentID(ac.ID) == agentID {
			if ws := strings.TrimSpace(ac.WorkspaceDir); ws != "" {
				return ws
			}
			break
		}
	}

	// 2. Environment variable override (trumps config-file keys per 12-factor).
	if ws := strings.TrimSpace(os.Getenv("METIQ_WORKSPACE")); ws != "" {
		return ws
	}

	// 3–5. Legacy extra config keys (checked for backward compat).
	if ws := extraWorkspaceDir(cfg.Extra); ws != "" {
		return ws
	}

	// 6. Canonical fallback.
	return defaultWorkspaceDir()
}

// extraWorkspaceDir checks the legacy extra config keys for a workspace dir.
func extraWorkspaceDir(extra map[string]any) string {
	if extra == nil {
		return ""
	}

	// extra.workspace.dir
	if wsMap, ok := extra["workspace"].(map[string]any); ok {
		if d, ok := wsMap["dir"].(string); ok && strings.TrimSpace(d) != "" {
			return strings.TrimSpace(d)
		}
	}

	// extra.workspace_dir
	if d, ok := extra["workspace_dir"].(string); ok && strings.TrimSpace(d) != "" {
		return strings.TrimSpace(d)
	}

	// extra.skills.workspace
	if rawSkills, ok := extra["skills"].(map[string]any); ok {
		if ws, ok := rawSkills["workspace"].(string); ok && strings.TrimSpace(ws) != "" {
			return strings.TrimSpace(ws)
		}
	}

	return ""
}

// defaultWorkspaceDir returns the canonical fallback workspace directory.
// Returns ~/.metiq/workspace, or /tmp/.metiq/workspace if HOME cannot be
// determined (should not happen in practice).
func defaultWorkspaceDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".metiq", "workspace")
	}
	return filepath.Join(os.TempDir(), ".metiq", "workspace")
}

// normalizeAgentID returns "main" for empty agent IDs.
func normalizeAgentID(id string) string {
	if s := strings.TrimSpace(id); s != "" {
		return s
	}
	return "main"
}
