package main

import (
	"context"
	"log"
	"time"

	"metiq/internal/agent"
	ctxengine "metiq/internal/context"
	"metiq/internal/memory"
	"metiq/internal/store/state"
)

// postTurnPersistenceServices bundles the long-lived stores and engines the
// post-turn persistence pipeline writes to. It is assembled once (all fields
// are process-lifetime singletons) and reused across every turn.
type postTurnPersistenceServices struct {
	transcriptRepo       *state.TranscriptRepository
	contextEngine        ctxengine.Engine
	sessionStore         *state.SessionStore
	sessionMemoryRuntime *sessionMemoryRuntime
	docsRepo             *state.DocsRepository
	memoryRepo           *state.MemoryRepository
	memoryIndex          memory.Store
	memoryTracker        *memoryIndexTracker
}

// postTurnPersistenceParams carries the per-turn inputs for persistPostTurn.
type postTurnPersistenceParams struct {
	Ctx       context.Context
	Config    state.ConfigDoc
	Scope     memory.ScopedContext
	Runtime   agent.Runtime
	SessionID string
	EventID   string
	AgentID   string
	Result    agent.TurnResult
}

// persistPostTurn runs the shared post-turn persistence pipeline that the DM
// path performs after a successful turn:
//
//  1. persist tool traces to the transcript,
//  2. persist + ingest the turn history (so future turns see prior tool usage),
//  3. observe the turn for session-memory extraction,
//  4. distill structured episodic memory (async, mirroring the DM path), and
//  5. update the session task-state used for prompt rehydration.
//
// Auto-joined channel turns (NIP29/NIP28/chat) previously called ProcessTurn and
// replied but only committed memory-recall artifacts, silently dropping all of
// the above — losing history, memory, and session continuity (swarmstr-nibw).
// They now call this helper so their persistence is equivalent to the DM path.
//
// The []string entry IDs returned by persistAndIngestTurnHistory are
// intentionally discarded here (swarmstr-sxq0): unlike the ACP task path
// (main.go) and the task_runner path (task_runner.go) — which link the entry
// IDs into a task-result record (TaskResultRef / result_history_entry_id) on
// both success and partial-failure turns — DM and channel turns have no such
// record to wire them to, so ignoring the IDs matches the DM path.
func persistPostTurn(svc postTurnPersistenceServices, p postTurnPersistenceParams) {
	ctx := p.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	if err := persistToolTraces(ctx, svc.transcriptRepo, p.SessionID, p.EventID, p.Result.ToolTraces); err != nil {
		log.Printf("persist tool traces failed session=%s err=%v", p.SessionID, err)
	}

	// Persist the full tool-call/tool-result history so future turns can see
	// prior tool usage — fixes the "announce and forget" behaviour.
	persistAndIngestTurnHistory(ctx, svc.transcriptRepo, svc.contextEngine, p.SessionID, p.EventID, p.Result.HistoryDelta, turnResultMetadataPtr(p.Result, nil))

	// Session-memory runtime updates feed extraction thresholds and may be read
	// by the next same-session turn.
	svc.sessionMemoryRuntime.ObserveTurn(
		p.Config,
		runtimeSessionMemoryGenerator{runtime: p.Runtime},
		p.SessionID,
		p.AgentID,
		sessionMemoryWorkspaceDir(p.Scope, workspaceDirForAgent(p.Config, p.AgentID)),
		resolveAgentContextWindow(p.Config, p.AgentID),
		p.Result.HistoryDelta,
	)

	// Distill structured episodic memory from the completed turn.
	if turnStateDocs := scopedMemoryDocs(distillTurnState(p.SessionID, p.EventID, p.Result.ToolTraces, p.Result.HistoryDelta, false), p.Scope); len(turnStateDocs) > 0 {
		go func(docs []state.MemoryDoc) {
			pCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			persistMemories(pCtx, svc.docsRepo, svc.memoryRepo, svc.memoryIndex, svc.memoryTracker, docs)
		}(turnStateDocs)
	}

	updateSessionTaskState(svc.sessionStore, p.SessionID, p.Result.ToolTraces, p.Result.HistoryDelta, false)
}
