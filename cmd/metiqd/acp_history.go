package main

import (
	"context"
	"fmt"
	"log"
	"time"

	acppkg "metiq/internal/acp"
	"metiq/internal/agent"
	ctxengine "metiq/internal/context"
	"metiq/internal/store/state"
)

func buildACPTranscriptMeta(taskID, fromPubKey string, parent *acppkg.ParentContext) map[string]any {
	meta := map[string]any{
		"request_event_id": taskID,
		"acp_task_id":      taskID,
	}
	if fromPubKey != "" {
		meta["acp_sender_pubkey"] = fromPubKey
	}
	if parent != nil {
		if parent.SessionID != "" {
			meta["acp_parent_session_id"] = parent.SessionID
		}
		if parent.AgentID != "" {
			meta["acp_parent_agent_id"] = parent.AgentID
		}
	}
	return meta
}

func cloneTranscriptMeta(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]any, len(meta))
	for k, v := range meta {
		out[k] = v
	}
	return out
}

func cloneACPStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func cloneACPWorkerMetadata(worker *acppkg.WorkerMetadata) *acppkg.WorkerMetadata {
	if worker == nil {
		return nil
	}
	out := &acppkg.WorkerMetadata{
		SessionID:       worker.SessionID,
		AgentID:         worker.AgentID,
		HistoryEntryIDs: cloneACPStringSlice(worker.HistoryEntryIDs),
	}
	if worker.ParentContext != nil {
		out.ParentContext = &acppkg.ParentContext{
			SessionID: worker.ParentContext.SessionID,
			AgentID:   worker.ParentContext.AgentID,
		}
	}
	if worker.TurnResult != nil {
		turnResult := *worker.TurnResult
		out.TurnResult = &turnResult
	}
	return out
}

func persistACPTranscriptMessages(
	ctx context.Context,
	transcriptRepo *state.TranscriptRepository,
	contextEngine ctxengine.Engine,
	sessionID string,
	messages []agent.ConversationMessage,
	entryIDFor func(int, agent.ConversationMessage) string,
	metaFor func(int, agent.ConversationMessage) map[string]any,
) []string {
	if len(messages) == 0 {
		return nil
	}
	nowUnix := time.Now().Unix()
	entryIDs := make([]string, 0, len(messages))
	for i, m := range messages {
		entryID := entryIDFor(i, m)
		if entryID == "" {
			continue
		}
		meta := metaFor(i, m)
		if len(m.ToolCalls) > 0 {
			meta["message_kind"] = "tool_call"
			tcRefs := make([]map[string]any, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				tcRefs[j] = map[string]any{"id": tc.ID, "name": tc.Name}
				if tc.ArgsJSON != "" {
					tcRefs[j]["args_json"] = tc.ArgsJSON
				}
			}
			meta["tool_calls"] = tcRefs
		}
		if m.ToolCallID != "" {
			meta["message_kind"] = "tool_result"
			meta["tool_call_id"] = m.ToolCallID
		}
		if transcriptRepo != nil {
			if _, err := transcriptRepo.PutEntry(ctx, state.TranscriptEntryDoc{
				Version:   1,
				SessionID: sessionID,
				EntryID:   entryID,
				Role:      m.Role,
				Text:      m.Content,
				Unix:      nowUnix,
				Meta:      meta,
			}); err != nil {
				log.Printf("persist acp transcript entry=%s err=%v", entryID, err)
			}
		}
		if contextEngine != nil {
			ctxMsg := ctxengine.Message{
				Role:       m.Role,
				Content:    m.Content,
				ToolCallID: m.ToolCallID,
				ID:         entryID,
				Unix:       nowUnix,
			}
			for _, tc := range m.ToolCalls {
				ctxMsg.ToolCalls = append(ctxMsg.ToolCalls, ctxengine.ToolCallRef{
					ID:       tc.ID,
					Name:     tc.Name,
					ArgsJSON: tc.ArgsJSON,
				})
			}
			if _, err := contextEngine.Ingest(ctx, sessionID, ctxMsg); err != nil {
				log.Printf("context engine ingest acp session=%s entry=%s err=%v", sessionID, entryID, err)
			}
		}
		entryIDs = append(entryIDs, entryID)
	}
	return entryIDs
}

func persistACPContextHistory(
	ctx context.Context,
	transcriptRepo *state.TranscriptRepository,
	contextEngine ctxengine.Engine,
	sessionID string,
	taskID string,
	fromPubKey string,
	parent *acppkg.ParentContext,
	messages []agent.ConversationMessage,
) []string {
	baseMeta := buildACPTranscriptMeta(taskID, fromPubKey, parent)
	return persistACPTranscriptMessages(
		ctx,
		transcriptRepo,
		contextEngine,
		sessionID,
		messages,
		func(i int, _ agent.ConversationMessage) string {
			return fmt.Sprintf("acp:%s:seed:%d", taskID, i)
		},
		func(_ int, _ agent.ConversationMessage) map[string]any {
			meta := cloneTranscriptMeta(baseMeta)
			meta["message_kind"] = "context_seed"
			return meta
		},
	)
}

func persistACPTurnHistory(
	ctx context.Context,
	transcriptRepo *state.TranscriptRepository,
	contextEngine ctxengine.Engine,
	sessionID string,
	taskID string,
	fromPubKey string,
	parent *acppkg.ParentContext,
	delta []agent.ConversationMessage,
	turnResultMeta *agent.TurnResultMetadata,
) []string {
	if len(delta) == 0 {
		return nil
	}
	baseMeta := buildACPTranscriptMeta(taskID, fromPubKey, parent)
	persistedTurnResultMeta := transcriptTurnResultMeta(turnResultMeta)
	return persistACPTranscriptMessages(
		ctx,
		transcriptRepo,
		contextEngine,
		sessionID,
		delta,
		func(i int, m agent.ConversationMessage) string {
			switch {
			case m.Role == "assistant" && len(m.ToolCalls) > 0:
				return fmt.Sprintf("turn:%s:toolcall:%d", taskID, i)
			case m.Role == "tool" && m.ToolCallID != "":
				return fmt.Sprintf("turn:%s:tool:%s", taskID, m.ToolCallID)
			case m.Role == "assistant":
				return fmt.Sprintf("turn:%s:assistant:%d", taskID, i)
			default:
				return fmt.Sprintf("turn:%s:msg:%d", taskID, i)
			}
		},
		func(i int, _ agent.ConversationMessage) map[string]any {
			meta := cloneTranscriptMeta(baseMeta)
			if persistedTurnResultMeta != nil && i == len(delta)-1 {
				meta["turn_result"] = persistedTurnResultMeta
			}
			return meta
		},
	)
}
