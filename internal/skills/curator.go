package skills

// curator.go — Skill curator lifecycle state (skills.curator.* gateway methods).
//
// The curator tracks a lightweight lifecycle for every skill in the merged
// catalog: an active/stale/archived status, a pinned flag, and coarse use
// counters. State is persisted as JSON keyed by skillKey under the agent
// workspace so it survives restarts and is derived on demand over the current
// merged catalog.
//
// Metiq deviation vs OpenClaw: there is no ClawHub. Curator status is computed
// from the local merged catalog + persisted lifecycle state, and overlap
// scoring is not yet implemented (overlaps is always an empty list).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"metiq/internal/store/state"
)

const (
	// curatorStateVersion identifies the persisted curator state schema.
	curatorStateVersion = 1
	// curatorStateFile is the JSON state file relative to the workspace .metiq dir.
	curatorStateFile = "skill-curator.json"
	// CuratorStaleAfterMs marks an un-pinned, idle skill as stale after this long.
	CuratorStaleAfterMs int64 = 30 * 24 * 60 * 60 * 1000
	// CuratorArchiveAfterMs auto-archives an un-pinned skill idle this long. A
	// skills.curator.restore call clears the flag and refreshes the idle clock.
	CuratorArchiveAfterMs int64 = 90 * 24 * 60 * 60 * 1000
)

// curatorSkillState is the persisted per-skill lifecycle record.
type curatorSkillState struct {
	Pinned        bool  `json:"pinned"`
	Archived      bool  `json:"archived"`
	UseCount      int64 `json:"useCount"`
	LastUsedAtMs  int64 `json:"lastUsedAtMs"`
	FirstSeenAtMs int64 `json:"firstSeenAtMs"`
}

// curatorReferenceMs is the timestamp idle time is measured from: the most
// recent use, falling back to when the curator first observed the skill.
func curatorReferenceMs(st curatorSkillState) int64 {
	if st.LastUsedAtMs > 0 {
		return st.LastUsedAtMs
	}
	return st.FirstSeenAtMs
}

// curatorState is the persisted curator state document.
type curatorState struct {
	Version         int                          `json:"version"`
	LastAttemptAtMs int64                        `json:"lastAttemptAtMs"`
	LastSuccessAtMs int64                        `json:"lastSuccessAtMs"`
	LastError       string                       `json:"lastError"`
	Skills          map[string]curatorSkillState `json:"skills"`
}

func nowMillis() int64 { return time.Now().UnixMilli() }

func curatorStatePath(workspaceDir string) string {
	return filepath.Join(workspaceDir, ".metiq", curatorStateFile)
}

func loadCuratorState(path string) curatorState {
	st := curatorState{Version: curatorStateVersion, Skills: map[string]curatorSkillState{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	var parsed curatorState
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return st
	}
	if parsed.Skills == nil {
		parsed.Skills = map[string]curatorSkillState{}
	}
	parsed.Version = curatorStateVersion
	return parsed
}

func saveCuratorState(path string, st curatorState) error {
	st.Version = curatorStateVersion
	if st.Skills == nil {
		st.Skills = map[string]curatorSkillState{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// deriveCuratorStatus resolves the lifecycle status for one skill. It returns
// the status plus whether the skill should be auto-archived (idle past the
// archive threshold), so callers can persist the transition.
func deriveCuratorStatus(st curatorSkillState, nowMs int64) (status string, autoArchive bool) {
	if st.Archived {
		return "archived", false
	}
	if st.Pinned {
		return "active", false
	}
	ref := curatorReferenceMs(st)
	if ref <= 0 {
		// Just observed; treat as freshly active until idle accrues.
		return "active", false
	}
	idle := nowMs - ref
	if idle > CuratorArchiveAfterMs {
		return "archived", true
	}
	if idle > CuratorStaleAfterMs {
		return "stale", false
	}
	return "active", false
}

// catalogSkillIndex returns the merged catalog skills keyed by normalized key,
// preserving a deterministic ordering for iteration.
func catalogSkillIndex(catalog *SkillCatalog) (map[string]*ResolvedSkill, []string) {
	index := map[string]*ResolvedSkill{}
	keys := make([]string, 0)
	if catalog == nil {
		return index, keys
	}
	for _, resolved := range catalog.Skills {
		if resolved == nil || resolved.Skill == nil {
			continue
		}
		key := normalizedSkillKey(resolved.Skill.SkillKey)
		if key == "" {
			continue
		}
		if _, ok := index[key]; ok {
			continue
		}
		index[key] = resolved
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return index, keys
}

func curatorSkillName(resolved *ResolvedSkill) string {
	if resolved == nil || resolved.Skill == nil {
		return ""
	}
	name := strings.TrimSpace(resolved.Skill.Manifest.Name)
	if name == "" {
		name = resolved.Skill.SkillKey
	}
	return name
}

// curatorEntry builds one SkillCuratorEntry result map for a skill.
func curatorEntry(key string, resolved *ResolvedSkill, st curatorSkillState, nowMs int64) map[string]any {
	status, _ := deriveCuratorStatus(st, nowMs)
	entry := map[string]any{
		"skill":        key,
		"skillKey":     key,
		"status":       status,
		"pinned":       st.Pinned,
		"archived":     st.Archived || status == "archived",
		"useCount":     st.UseCount,
		"lastUsedAtMs": st.LastUsedAtMs,
	}
	if resolved != nil && resolved.Skill != nil {
		entry["name"] = curatorSkillName(resolved)
		entry["description"] = strings.TrimSpace(resolved.Skill.Manifest.Description)
		entry["source"] = string(resolved.SourceKind)
		entry["filePath"] = strings.TrimSpace(resolved.Skill.FilePath)
	} else {
		entry["name"] = key
		entry["description"] = ""
		entry["source"] = ""
		entry["filePath"] = ""
	}
	return entry
}

// BuildCuratorStatus derives the curator status report over the merged catalog
// and persisted lifecycle state, recording the attempt/success timestamps.
func BuildCuratorStatus(cfg state.ConfigDoc, agentID string) (map[string]any, error) {
	workspaceDir := ResolveAgentWorkspaceDir(cfg, agentID)
	statePath := curatorStatePath(workspaceDir)
	st := loadCuratorState(statePath)
	now := nowMillis()
	st.LastAttemptAtMs = now

	catalog, err := BuildSkillCatalog(cfg, agentID)
	if err != nil {
		st.LastError = err.Error()
		_ = saveCuratorState(statePath, st)
		return nil, err
	}

	if st.Skills == nil {
		st.Skills = map[string]curatorSkillState{}
	}
	index, keys := catalogSkillIndex(catalog)
	counts := map[string]int{"active": 0, "stale": 0, "archived": 0}
	skillsList := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		skillState, seen := st.Skills[key]
		if !seen || curatorReferenceMs(skillState) <= 0 {
			// Record first observation so idle time accrues from now.
			if skillState.FirstSeenAtMs <= 0 {
				skillState.FirstSeenAtMs = now
			}
		}
		status, autoArchive := deriveCuratorStatus(skillState, now)
		if autoArchive && !skillState.Archived {
			skillState.Archived = true
		}
		st.Skills[key] = skillState
		entry := curatorEntry(key, index[key], skillState, now)
		if s, ok := entry["status"].(string); ok {
			status = s
		}
		counts[status]++
		skillsList = append(skillsList, entry)
	}

	st.LastSuccessAtMs = now
	st.LastError = ""
	_ = saveCuratorState(statePath, st)

	return map[string]any{
		"lastAttemptAtMs": st.LastAttemptAtMs,
		"lastSuccessAtMs": st.LastSuccessAtMs,
		"lastError":       st.LastError,
		"counts": map[string]any{
			"active":   counts["active"],
			"stale":    counts["stale"],
			"archived": counts["archived"],
		},
		"skills":   skillsList,
		"overlaps": []any{},
	}, nil
}

// mutateCuratorSkill validates the skill exists in the merged catalog, applies
// mutate to its lifecycle state, persists, and returns the updated entry.
func mutateCuratorSkill(cfg state.ConfigDoc, agentID, skill string, mutate func(*curatorSkillState)) (map[string]any, error) {
	key := normalizedSkillKey(strings.TrimSpace(skill))
	if key == "" {
		return nil, fmt.Errorf("skill is required")
	}
	catalog, err := BuildSkillCatalog(cfg, agentID)
	if err != nil {
		return nil, err
	}
	index, _ := catalogSkillIndex(catalog)
	resolved, ok := index[key]
	if !ok {
		return nil, state.ErrNotFound
	}

	workspaceDir := ResolveAgentWorkspaceDir(cfg, agentID)
	statePath := curatorStatePath(workspaceDir)
	st := loadCuratorState(statePath)
	entryState := st.Skills[key]
	if entryState.FirstSeenAtMs <= 0 {
		entryState.FirstSeenAtMs = nowMillis()
	}
	mutate(&entryState)
	if st.Skills == nil {
		st.Skills = map[string]curatorSkillState{}
	}
	st.Skills[key] = entryState
	if err := saveCuratorState(statePath, st); err != nil {
		return nil, err
	}
	return curatorEntry(key, resolved, entryState, nowMillis()), nil
}

// SetCuratorPin sets or clears the pinned flag for one skill.
func SetCuratorPin(cfg state.ConfigDoc, agentID, skill string, pinned bool) (map[string]any, error) {
	return mutateCuratorSkill(cfg, agentID, skill, func(st *curatorSkillState) {
		st.Pinned = pinned
	})
}

// RestoreCuratorSkill clears the archived flag and refreshes the idle clock,
// moving an archived skill back to active.
func RestoreCuratorSkill(cfg state.ConfigDoc, agentID, skill string) (map[string]any, error) {
	return mutateCuratorSkill(cfg, agentID, skill, func(st *curatorSkillState) {
		st.Archived = false
		st.LastUsedAtMs = nowMillis()
	})
}
