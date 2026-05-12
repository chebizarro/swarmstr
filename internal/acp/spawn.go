package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// SpawnSessionInput creates a managed child ACP runtime session.
type SpawnSessionInput struct {
	ParentSessionKey string            `json:"parent_session_key"`
	ChildSessionKey  string            `json:"child_session_key,omitempty"`
	Agent            string            `json:"agent,omitempty"`
	Backend          string            `json:"backend,omitempty"`
	Mode             SessionMode       `json:"mode,omitempty"`
	CWD              string            `json:"cwd,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	ThreadID         string            `json:"thread_id,omitempty"`
	Controls         []RuntimeControl  `json:"controls,omitempty"`
}

// SpawnSessionResult describes the accepted child session.
type SpawnSessionResult struct {
	ParentSessionKey string        `json:"parent_session_key"`
	ChildSessionKey  string        `json:"child_session_key"`
	Agent            string        `json:"agent,omitempty"`
	Backend          string        `json:"backend,omitempty"`
	Mode             SessionMode   `json:"mode,omitempty"`
	Depth            int           `json:"depth"`
	ThreadID         string        `json:"thread_id,omitempty"`
	Handle           RuntimeHandle `json:"handle"`
}

// SpawnSession creates a child ACP runtime session with ancestry, depth, child-count,
// and thread metadata persisted alongside manager session state.
func (m *Manager) SpawnSession(ctx context.Context, input SpawnSessionInput) (SpawnSessionResult, error) {
	parentKey := canonicalSessionKey(input.ParentSessionKey)
	if parentKey == "" {
		return SpawnSessionResult{}, fmt.Errorf("acp spawn: parent session key required")
	}
	childKey := canonicalSessionKey(input.ChildSessionKey)
	if childKey == "" {
		childKey = fmt.Sprintf("%s/child/%d", parentKey, m.now().UnixNano())
	}
	if childKey == parentKey {
		return SpawnSessionResult{}, fmt.Errorf("acp spawn: child session key must differ from parent")
	}

	parentUnlock := m.lockSession(parentKey)
	defer parentUnlock()
	if active := m.getActive(parentKey); active != nil {
		return SpawnSessionResult{}, fmt.Errorf("%w: %s", ErrTurnActive, parentKey)
	}
	parentRec, err := m.loadRecord(ctx, parentKey)
	if err != nil {
		return SpawnSessionResult{}, err
	}
	if parentRec == nil && m.getCached(parentKey) == nil {
		return SpawnSessionResult{}, ErrSessionNotFound
	}
	parentMeta := decodeSessionRuntimeMeta(parentRec)
	if cached := m.getCached(parentKey); cached != nil {
		parentMeta.Backend = firstNonEmpty(parentMeta.Backend, cached.Backend)
		parentMeta.Agent = firstNonEmpty(parentMeta.Agent, cached.Agent)
		parentMeta.Mode = cached.Mode
		parentMeta.CWD = firstNonEmpty(parentMeta.CWD, cached.CWD)
	}

	agentID := normalizeAgentID(firstNonEmpty(input.Agent, parentMeta.Agent, "main"))
	maxDepth, maxChildren := m.spawnLimits(agentID)
	depth := parentMeta.SpawnDepth + 1
	if depth > maxDepth {
		return SpawnSessionResult{}, fmt.Errorf("acp spawn: depth limit %d exceeded", maxDepth)
	}
	children, err := m.directChildCount(ctx, parentKey)
	if err != nil {
		return SpawnSessionResult{}, err
	}
	if children >= maxChildren {
		return SpawnSessionResult{}, fmt.Errorf("acp spawn: child limit %d exceeded for %q", maxChildren, parentKey)
	}
	if existing, err := m.loadRecord(ctx, childKey); err != nil {
		return SpawnSessionResult{}, err
	} else if existing != nil || m.getCached(childKey) != nil {
		return SpawnSessionResult{}, fmt.Errorf("acp spawn: child session %q already exists", childKey)
	}

	handle, err := m.InitializeSession(ctx, InitializeSessionInput{
		SessionKey: childKey,
		Agent:      agentID,
		Backend:    firstNonEmpty(input.Backend, parentMeta.Backend),
		Mode:       firstNonEmptyMode(input.Mode, parentMeta.Mode, SessionModePersistent),
		CWD:        firstNonEmpty(input.CWD, parentMeta.CWD),
		Env:        input.Env,
		Controls:   input.Controls,
	})
	if err != nil {
		_ = m.sessionsDelete(ctx, childKey)
		m.clearCached(childKey)
		return SpawnSessionResult{}, err
	}
	threadID := strings.TrimSpace(input.ThreadID)
	if threadID == "" {
		threadID = parentMeta.ThreadID
	}
	if err := m.saveSpawnChildMeta(ctx, childKey, parentKey, agentID, firstNonEmptyMode(input.Mode, parentMeta.Mode, SessionModePersistent), handle, depth, threadID); err != nil {
		_ = m.CloseSession(ctx, CloseSessionInput{SessionKey: childKey, Reason: "spawn-metadata-failed", DiscardPersistentState: true})
		return SpawnSessionResult{}, err
	}
	if err := m.saveSpawnParentMeta(ctx, parentKey, parentMeta, childKey); err != nil {
		_ = m.CloseSession(ctx, CloseSessionInput{SessionKey: childKey, Reason: "spawn-parent-link-failed", DiscardPersistentState: true})
		return SpawnSessionResult{}, err
	}
	m.mu.Lock()
	m.counters.SessionsSpawned++
	m.mu.Unlock()
	return SpawnSessionResult{ParentSessionKey: parentKey, ChildSessionKey: childKey, Agent: agentID, Backend: handle.Backend, Mode: firstNonEmptyMode(input.Mode, parentMeta.Mode, SessionModePersistent), Depth: depth, ThreadID: threadID, Handle: handle}, nil
}

func (m *Manager) spawnLimits(agentID string) (int, int) {
	maxDepth := m.opts.MaxSpawnDepth
	maxChildren := m.opts.MaxChildrenPerSession
	if m.agents != nil {
		if entry, ok := m.agents.Resolve(agentID); ok {
			if entry.MaxSpawnDepth > 0 {
				maxDepth = entry.MaxSpawnDepth
			}
			if entry.MaxChildren > 0 {
				maxChildren = entry.MaxChildren
			}
		}
	}
	return maxDepth, maxChildren
}

func (m *Manager) directChildCount(ctx context.Context, parentKey string) (int, error) {
	if m.sessions == nil {
		return 0, nil
	}
	records, err := m.sessions.List(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, rec := range records {
		meta := decodeSessionRuntimeMeta(rec)
		if meta.ParentSessionKey == parentKey && meta.State != "closed" {
			count++
		}
	}
	return count, nil
}

func (m *Manager) saveSpawnChildMeta(ctx context.Context, childKey, parentKey, agent string, mode SessionMode, handle RuntimeHandle, depth int, threadID string) error {
	meta := SessionRuntimeMeta{Backend: handle.Backend, Agent: agent, Mode: mode, RuntimeSessionName: handle.RuntimeSessionName, CWD: handle.CWD, AcpxRecordID: handle.AcpxRecordID, State: "idle", LastActivityAt: m.now().Unix(), ParentSessionKey: parentKey, SpawnDepth: depth, ThreadID: threadID, SpawnedBy: "acp.spawn"}
	return m.saveMetaRecord(ctx, childKey, meta)
}

func (m *Manager) saveSpawnParentMeta(ctx context.Context, parentKey string, meta SessionRuntimeMeta, childKey string) error {
	meta.ChildSessionKeys = appendUniqueString(meta.ChildSessionKeys, childKey)
	if meta.State == "" {
		meta.State = "idle"
	}
	meta.LastActivityAt = m.now().Unix()
	return m.saveMetaRecord(ctx, parentKey, meta)
}

func (m *Manager) saveMetaRecord(ctx context.Context, key string, meta SessionRuntimeMeta) error {
	if m.sessions == nil {
		return nil
	}
	existing, _ := m.sessions.Load(ctx, key)
	rec := &SessionRecord{SessionKey: key, AgentID: meta.Agent}
	if existing != nil {
		rec.ID = existing.ID
		rec.CreatedAt = existing.CreatedAt
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	rec.State = raw
	return m.sessions.Save(ctx, rec)
}

func (m *Manager) sessionsDelete(ctx context.Context, key string) error {
	if m.sessions == nil {
		return nil
	}
	return m.sessions.Delete(ctx, key)
}

func firstNonEmptyMode(values ...SessionMode) SessionMode {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
