package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	acppkg "metiq/internal/acp"
	"metiq/internal/store/state"
)

const acpWorkerTaskMetaKey = "acp_worker_task"

func acpWorkerTaskMeta(agentID, peerPubKey string, payload acppkg.TaskPayload, startedAt time.Time) map[string]any {
	meta := map[string]any{
		"task_id":       strings.TrimSpace(payload.ReplyTo),
		"agent_id":      strings.TrimSpace(agentID),
		"peer_pubkey":   strings.TrimSpace(peerPubKey),
		"started_at_ms": startedAt.UnixMilli(),
		"status":        "running",
	}
	if payload.TimeoutMS > 0 {
		meta["timeout_ms"] = payload.TimeoutMS
	}
	if parent := payload.ParentContext; parent != nil {
		parentMeta := map[string]any{}
		if sessionID := strings.TrimSpace(parent.SessionID); sessionID != "" {
			parentMeta["session_id"] = sessionID
		}
		if agentID := strings.TrimSpace(parent.AgentID); agentID != "" {
			parentMeta["agent_id"] = agentID
		}
		if len(parentMeta) > 0 {
			meta["parent_context"] = parentMeta
		}
	}
	return meta
}

func beginACPWorkerTask(ctx context.Context, docsRepo *state.DocsRepository, sessionID, peerPubKey, agentID, taskID string, payload acppkg.TaskPayload, startedAt time.Time) (func(), error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session id is empty")
	}
	if docsRepo == nil {
		return nil, fmt.Errorf("docs repository is nil")
	}
	if docsRepo != nil && sessionID != "" {
		taskMeta := acpWorkerTaskMeta(agentID, peerPubKey, payload, startedAt)
		taskMeta["task_id"] = strings.TrimSpace(taskID)
		if err := updateSessionDoc(ctx, docsRepo, sessionID, peerPubKey, func(session *state.SessionDoc) error {
			session.Meta = mergeSessionMeta(session.Meta, map[string]any{
				"active_turn":        true,
				acpWorkerTaskMetaKey: taskMeta,
			})
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return func() {
		if docsRepo == nil || sessionID == "" {
			return
		}
		clearCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := updateSessionDoc(clearCtx, docsRepo, sessionID, peerPubKey, func(session *state.SessionDoc) error {
			session.Meta = mergeSessionMeta(session.Meta, map[string]any{
				"active_turn":        false,
				acpWorkerTaskMetaKey: nil,
			})
			return nil
		}); err != nil {
			log.Printf("acp worker task cleanup failed session=%s task_id=%s err=%v", sessionID, taskID, err)
		}
	}, nil
}
