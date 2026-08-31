package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"metiq/internal/admin"
	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	"metiq/internal/gateway/sessioncoord"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func sessionGroupRows(names []string) []map[string]any {
	rows := make([]map[string]any, 0, len(names))
	for position, name := range names {
		rows = append(rows, map[string]any{"name": name, "position": position})
	}
	return rows
}

func (h controlRPCHandler) handleSessionRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	dmBus := h.deps.dmBus
	chatCancels := h.deps.chatCancels
	steeringMailboxes := h.deps.steeringMailboxes
	usageState := h.deps.usageState
	docsRepo := h.deps.docsRepo
	transcriptRepo := h.deps.transcriptRepo
	memoryIndex := h.deps.memoryIndex
	configState := h.deps.configState
	sessionStore := h.deps.sessionStore
	sessionCoordinator := h.deps.sessionCoordinator
	hooksMgr := h.deps.hooksMgr
	mediaTranscriber := h.deps.mediaTranscriber
	toolRegistry := h.deps.toolRegistry
	if transcriptRepo != nil && sessionStore != nil {
		transcriptRepo.BindSessionStore(sessionStore)
	}
	if err := h.authorizeSessionMutationVisibility(ctx, in, method); err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}

	switch method {
	case methods.MethodChatSend, methods.MethodSessionsSend:
		req, err := methods.DecodeChatSendParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		msgText := req.Text
		if len(req.Attachments) > 0 {
			var preprocessErr error
			msgText, _, preprocessErr = preprocessAttachments(ctx, req.Text, req.Attachments, mediaTranscriber)
			if preprocessErr != nil {
				log.Printf("chat.send: attachment preprocess error: %v", preprocessErr)
			}
		}
		if msgText == "" {
			msgText = req.Text
		}
		sendCtx := ctx
		release := func() {}
		if chatCancels != nil {
			sendCtx, release = chatCancels.Begin(req.To, ctx)
			defer release()
		}
		if err := dmBus.SendDM(sendCtx, req.To, msgText); err != nil {
			if errors.Is(err, context.Canceled) {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("chat aborted")
			}
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true}}, true, nil
	case methods.MethodChatHistory:
		req, err := methods.DecodeChatHistoryParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := methods.GetChatHistory(ctx, docsRepo, transcriptRepo, req.SessionID, req.Limit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodChatAbort, methods.MethodSessionsAbort:
		req, err := methods.DecodeChatAbortParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		aborted := 0
		if chatCancels != nil {
			if strings.TrimSpace(req.SessionID) == "" {
				aborted = chatCancels.AbortAll()
			} else if chatCancels.Abort(req.SessionID) {
				aborted = 1
			}
		}
		if usageState != nil {
			usageState.RecordAbort(aborted)
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"ok": true, "session_id": req.SessionID, "aborted": aborted > 0, "aborted_count": aborted})}, true, nil
	case methods.MethodSessionGet, methods.MethodSessionsDescribe:
		req, err := methods.DecodeSessionGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := methods.GetSessionWithTranscript(ctx, docsRepo, transcriptRepo, req.SessionID, req.Limit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsCreate:
		req, err := methods.DecodeSessionsCreateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if docsRepo == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("session repository unavailable")
		}
		key := req.Key
		if key == "" {
			key = fmt.Sprintf("session-%d", time.Now().UnixNano())
		}
		created := false
		session, getErr := docsRepo.GetSession(ctx, key)
		if getErr != nil {
			requirement, err := sessionSandboxRequirement(cfg, "operator")
			if err != nil {
				return nostruntime.ControlRPCResult{}, true, err
			}
			meta := map[string]any{"agent_id": req.AgentID, "label": req.Label, "model": req.Model, "thinking_level": req.ThinkingLevel, "incognito": req.Incognito, "visibility": req.Visibility, "catalog_id": req.CatalogID, "parent_session_key": req.ParentSessionKey, "spawn_depth": req.SpawnDepth, "forked": req.Fork, "task": req.Task}
			session = state.SessionDoc{Version: 1, SessionID: key, LastInboundAt: time.Now().Unix(), SandboxRequirement: requirement, Meta: meta}
			if _, err := docsRepo.PutSession(ctx, key, session); err != nil {
				return nostruntime.ControlRPCResult{}, true, err
			}
			created = true
			if sessionStore != nil {
				entry := state.SessionEntry{SessionID: key, AgentID: req.AgentID, Label: req.Label, ModelOverride: req.Model, ThinkingLevel: req.ThinkingLevel, SpawnedBy: req.ParentSessionKey, ForkedFromParent: req.Fork, CreatedAt: time.Now(), UpdatedAt: time.Now()}
				if err := sessionStore.Put(key, entry); err != nil {
					return nostruntime.ControlRPCResult{}, true, err
				}
			}
		}
		result := map[string]any{"ok": true, "key": key, "sessionId": key, "entry": session, "created": created, "runStarted": false}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsList:
		req, err := methods.DecodeSessionsListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := admin.BuildSessionsListResponse(ctx, req, admin.SessionsListResponseOptions{
			Config:         cfg,
			SessionStore:   sessionStore,
			ListSessions:   docsRepo.ListSessions,
			ListTranscript: transcriptRepo.ListSession,
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsPreview:
		req, err := methods.DecodeSessionsPreviewParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := methods.PreviewSessions(ctx, docsRepo, transcriptRepo, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsPatch:
		req, err := methods.DecodeSessionsPatchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		session, err := updateExistingSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
			session.Meta = mergeSessionMeta(session.Meta, req.Meta)
			return nil
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "session": session}}, true, nil
	case methods.MethodSessionsReset:
		req, err := methods.DecodeSessionsResetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		var session state.SessionDoc
		if chatCancels != nil {
			chatCancels.Abort(req.SessionID)
		}
		clearTransientSessionSteering(steeringMailboxes, req.SessionID)
		err = withExclusiveSessionTurn(ctx, req.SessionID, 15*time.Second, func() error {
			clearTransientSessionSteering(steeringMailboxes, req.SessionID)
			var innerErr error
			session, innerErr = updateExistingSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
				session.LastInboundAt = 0
				session.LastReplyAt = 0
				session.Meta = map[string]any{}
				return nil
			})
			return innerErr
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if hooksMgr != nil {
			go hooksMgr.Fire("command:reset", req.SessionID, map[string]any{})
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "session": session}}, true, nil
	case methods.MethodSessionsDelete:
		req, err := methods.DecodeSessionsDeleteParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if chatCancels != nil {
			chatCancels.Abort(req.SessionID)
		}
		clearTransientSessionSteering(steeringMailboxes, req.SessionID)
		err = withExclusiveSessionTurn(ctx, req.SessionID, 15*time.Second, func() error {
			clearTransientSessionSteering(steeringMailboxes, req.SessionID)
			_, innerErr := updateExistingSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
				session.Meta = mergeSessionMeta(session.Meta, map[string]any{"deleted": true, "deleted_at": time.Now().Unix()})
				return nil
			})
			return innerErr
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"ok": true, "session_id": req.SessionID, "deleted": true})}, true, nil
	case methods.MethodSessionsCompact:
		req, err := methods.DecodeSessionsCompactParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		var compactResult map[string]any
		if chatCancels != nil {
			chatCancels.Abort(req.SessionID)
		}
		err = withExclusiveSessionTurn(ctx, req.SessionID, 15*time.Second, func() error {
			if _, err := docsRepo.GetSession(ctx, req.SessionID); err != nil {
				return err
			}
			flushOutcome, err := ensureSessionMemoryCurrent(ctx, configState.Get(), req.SessionID, sessionStore)
			if err != nil {
				return fmt.Errorf("sessions.compact session memory flush: %w", err)
			}
			_ = flushOutcome
			graph, err := transcriptRepo.EnsureGraph(ctx, req.SessionID, req.SessionID)
			if err != nil {
				return err
			}
			entries, err := transcriptRepo.ListSessionAll(ctx, req.SessionID)
			if err != nil {
				return err
			}
			dropped := len(entries) - req.Keep
			if dropped < 0 {
				dropped = 0
			}
			checkpointID := uuid.NewString()
			snapshotID := "snapshot-" + checkpointID
			if err := transcriptRepo.WriteSnapshot(ctx, snapshotID, req.SessionID, entries); err != nil {
				return fmt.Errorf("sessions.compact snapshot: %w", err)
			}
			summaryGenerated := false
			summaryText := ""
			activeAgentID, summaryRuntime := resolveInboundChannelRuntime("", req.SessionID)
			if dropped > 0 && summaryRuntime != nil {
				compactedEntries := entries[:dropped]
				var sb strings.Builder
				for _, e := range compactedEntries {
					if e.Role == "deleted" {
						continue
					}
					sb.WriteString(e.Role)
					sb.WriteString(": ")
					text := e.Text
					if len(text) > 400 {
						text = text[:400] + "…"
					}
					sb.WriteString(text)
					sb.WriteString("\n")
				}
				snippet := sb.String()
				if len(snippet) > 6000 {
					snippet = snippet[:6000] + "…"
				}
				if snippet != "" {
					summaryPrompt := "You are a session-memory assistant. Summarize the following conversation history concisely in 2-4 sentences, capturing the key topics, decisions, and context needed to continue the conversation later. Do NOT include greetings or meta-commentary; only output the summary.\n\n" + snippet
					selectedRuntime := summaryRuntime
					usedAuxiliaryRuntime := false
					if agCfg, ok := resolveAgentConfigByID(cfg, activeAgentID); ok {
						if auxiliaryModel := resolveAuxiliaryModelForAgent(agCfg, auxiliaryModelUseCaseCompaction); auxiliaryModel != "" {
							lightRuntime, lightErr := buildRuntimeForAgentModel(cfg, agCfg, auxiliaryModel, toolRegistry)
							if lightErr != nil {
								log.Printf("sessions.compact: light summary runtime unavailable agent=%s model=%q err=%v", activeAgentID, auxiliaryModel, lightErr)
							} else if lightRuntime != nil {
								selectedRuntime = lightRuntime
								usedAuxiliaryRuntime = true
							}
						}
					}
					runSummary := func(rt agent.Runtime) (agent.TurnResult, error) {
						summaryCtx, summaryCancel := context.WithTimeout(ctx, 30*time.Second)
						defer summaryCancel()
						return rt.ProcessTurn(summaryCtx, agent.Turn{SessionID: req.SessionID + ":compact", UserText: summaryPrompt, ContextWindowTokens: maxContextTokensForAgent(configState.Get(), activeAgentID)})
					}
					result, summaryErr := runSummary(selectedRuntime)
					if summaryErr != nil && usedAuxiliaryRuntime && summaryRuntime != nil {
						log.Printf("sessions.compact: light summary failed agent=%s err=%v — retrying primary runtime", activeAgentID, summaryErr)
						result, summaryErr = runSummary(summaryRuntime)
					}
					if summaryErr == nil && strings.TrimSpace(result.Text) != "" {
						summaryText = "[Compact summary of " + strconv.Itoa(dropped) + " earlier messages]\n\n" + strings.TrimSpace(result.Text)
						summaryGenerated = true
					} else if summaryErr != nil {
						log.Printf("sessions.compact: LLM summary skipped: %v", summaryErr)
					}
				}
			}
			newLeaf := graph.ActiveLeafID
			firstKept := ""
			if dropped > 0 {
				if summaryText == "" {
					summaryText = "[Compaction boundary for " + strconv.Itoa(dropped) + " earlier messages]"
				}
				parent := ""
				summaryEntry := state.TranscriptEntryDoc{Version: 1, SessionID: req.SessionID, EntryID: "compact-summary-" + checkpointID, ParentEntryID: parent, Role: "system", Text: summaryText, Unix: time.Now().Unix(), Meta: map[string]any{"compact": true, "compact_from": dropped, "checkpoint_id": checkpointID}}
				if _, err := transcriptRepo.PutDetachedEntry(ctx, summaryEntry); err != nil {
					return err
				}
				parent = summaryEntry.EntryID
				for i := dropped; i < len(entries); i++ {
					copy := entries[i]
					copy.EntryID = fmt.Sprintf("compact-%s-%04d", checkpointID, i-dropped)
					copy.ParentEntryID = parent
					if firstKept == "" {
						firstKept = copy.EntryID
					}
					if _, err := transcriptRepo.PutDetachedEntry(ctx, copy); err != nil {
						return err
					}
					parent = copy.EntryID
				}
				newLeaf = parent
			}
			nowMS := time.Now().UnixMilli()
			checkpoint := state.CompactionCheckpointRef{
				CheckpointID: checkpointID, SessionKey: req.SessionID, SessionID: req.SessionID,
				CreatedAt: nowMS, Reason: "manual", Summary: summaryText, FirstKeptEntry: firstKept,
				DroppedEntries: dropped, KeptEntries: len(entries) - dropped, SnapshotID: snapshotID,
				PreCompaction:  map[string]any{"session_id": req.SessionID, "leaf_id": graph.ActiveLeafID},
				PostCompaction: map[string]any{"session_id": req.SessionID, "leaf_id": newLeaf},
			}
			heads := state.ReplaceTranscriptHead(graph.BranchHeads, graph.ActiveLeafID, newLeaf, dropped > 0)
			if _, err := sessionStore.CommitTranscriptGraph(req.SessionID, graph.Revision, state.TranscriptGraphMutation{ActiveLeafID: newLeaf, BranchHeads: heads, Checkpoint: &checkpoint, CompactionDelta: 1}); err != nil {
				return err
			}
			if _, err := updateExistingSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
				session.Meta = mergeSessionMeta(session.Meta, map[string]any{"compacted_at": time.Now().Unix(), "compacted_keep": req.Keep, "compacted_from_entries": len(entries), "compacted_dropped_entries": dropped, "compacted_summary": summaryGenerated, "compaction_checkpoint_id": checkpointID})
				return nil
			}); err != nil {
				log.Printf("sessions.compact: session metadata projection failed: %v", err)
			}
			compactResult = methods.ApplyCompatResponseAliases(map[string]any{"ok": true, "session_id": req.SessionID, "kept": req.Keep, "from_entries": len(entries), "dropped": dropped, "summary_generated": summaryGenerated})
			return nil
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: compactResult}, true, nil
	case methods.MethodSessionsFilesList:
		req, err := methods.DecodeSessionsFilesListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := handleSessionsFilesList(ctx, cfg, docsRepo, transcriptRepo, sessionStore, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsFilesGet:
		req, err := methods.DecodeSessionsFilesGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := handleSessionsFilesGet(ctx, cfg, docsRepo, sessionStore, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsFilesSet:
		req, err := methods.DecodeSessionsFilesSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := handleSessionsFilesSet(ctx, cfg, docsRepo, sessionStore, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsFilesReveal:
		req, err := methods.DecodeSessionsFilesRevealParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: handleSessionsFilesReveal(ctx, cfg, docsRepo, sessionStore, req)}, true, nil
	case methods.MethodSessionsCatalogList:
		req, err := methods.DecodeSessionsCatalogListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := handleSessionsCatalogList(ctx, cfg, docsRepo, sessionStore, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsCatalogRead:
		req, err := methods.DecodeSessionsCatalogReadParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := handleSessionsCatalogRead(ctx, docsRepo, transcriptRepo, sessionStore, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsCatalogContinue:
		req, err := methods.DecodeSessionsCatalogContinueParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := handleSessionsCatalogContinue(ctx, docsRepo, sessionStore, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsCatalogArchive:
		req, err := methods.DecodeSessionsCatalogArchiveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := handleSessionsCatalogArchive(ctx, docsRepo, sessionStore, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsCompactionList:
		req, err := methods.DecodeSessionsCompactionListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := listSessionCompactionCheckpoints(sessionStore, req.Key)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsCompactionGet:
		req, err := methods.DecodeSessionsCompactionGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := getSessionCompactionCheckpoint(sessionStore, req.Key, req.CheckpointID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsCompactionBranch:
		req, err := methods.DecodeSessionsCompactionBranchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		var result map[string]any
		err = withExclusiveSessionTurn(ctx, req.Key, 15*time.Second, func() error {
			var innerErr error
			result, innerErr = branchSessionAtCheckpoint(ctx, docsRepo, transcriptRepo, sessionStore, req.Key, req.AgentID, req.CheckpointID)
			return innerErr
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsCompactionRestore:
		req, err := methods.DecodeSessionsCompactionRestoreParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if chatCancels != nil {
			chatCancels.Abort(req.Key)
		}
		clearTransientSessionSteering(steeringMailboxes, req.Key)
		var result map[string]any
		err = withExclusiveSessionTurn(ctx, req.Key, 15*time.Second, func() error {
			var innerErr error
			result, innerErr = restoreSessionCheckpoint(ctx, transcriptRepo, sessionStore, req.Key, req.AgentID, req.CheckpointID)
			return innerErr
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsBranchesList:
		req, err := methods.DecodeSessionsBranchesListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := listSessionBranches(ctx, transcriptRepo, sessionStore, req.SessionKey, req.AgentID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsBranchesSwitch:
		req, err := methods.DecodeSessionsBranchesSwitchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		var result map[string]any
		err = withExclusiveSessionTurn(ctx, req.SessionKey, 15*time.Second, func() error {
			var innerErr error
			result, innerErr = switchSessionBranch(ctx, transcriptRepo, sessionStore, req.SessionKey, req.AgentID, req.LeafEntryID)
			return innerErr
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsRewind:
		req, err := methods.DecodeSessionsRewindParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		var result map[string]any
		err = withExclusiveSessionTurn(ctx, req.SessionKey, 15*time.Second, func() error {
			var innerErr error
			result, innerErr = rewindSessionAtEntry(ctx, transcriptRepo, sessionStore, req.SessionKey, req.AgentID, req.EntryID)
			return innerErr
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsFork:
		req, err := methods.DecodeSessionsForkParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		var result map[string]any
		err = withExclusiveSessionTurn(ctx, req.SessionKey, 15*time.Second, func() error {
			var innerErr error
			result, innerErr = forkSessionAtEntry(ctx, docsRepo, transcriptRepo, sessionStore, req.SessionKey, req.AgentID, req.EntryID)
			return innerErr
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsSearch:
		req, err := methods.DecodeSessionsSearchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := searchSessionTranscripts(ctx, docsRepo, transcriptRepo, sessionStore, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsDispatch:
		if sessionCoordinator == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("session placement service unavailable")
		}
		req, err := methods.DecodeSessionsDispatchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		principal, _ := gatewayws.PrincipalFromContext(ctx)
		connectionID, _ := gatewayws.ConnectionIDFromContext(ctx)
		placement, err := sessionCoordinator.Dispatch(ctx, sessioncoord.DispatchRequest{Key: req.Key, AgentID: req.AgentID, Backend: req.Backend, ConnectionID: connectionID, Subject: principal.Subject})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "key": req.Key, "sessionId": req.Key, "placement": placement}}, true, nil
	case methods.MethodSessionsReclaim:
		if sessionCoordinator == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("session placement service unavailable")
		}
		req, err := methods.DecodeSessionsReclaimParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		connectionID, _ := gatewayws.ConnectionIDFromContext(ctx)
		placement, err := sessionCoordinator.Reclaim(ctx, sessioncoord.ReclaimRequest{Key: req.Key, ConnectionID: connectionID, Reason: "operator reclaim", Force: true})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "key": req.Key, "sessionId": req.Key, "placement": placement}}, true, nil
	case methods.MethodSessionsGroupsList:
		if sessionCoordinator == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("session group service unavailable")
		}
		if _, err := methods.DecodeSessionsGroupsListParams(in.Params); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		groups, err := sessionCoordinator.ListGroups(ctx)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"groups": sessionGroupRows(groups)}}, true, nil
	case methods.MethodSessionsGroupsPut:
		if sessionCoordinator == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("session group service unavailable")
		}
		req, err := methods.DecodeSessionsGroupsPutParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		groups, err := sessionCoordinator.PutGroups(ctx, req.Names)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "groups": sessionGroupRows(groups)}}, true, nil
	case methods.MethodSessionsGroupsRename:
		if sessionCoordinator == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("session group service unavailable")
		}
		req, err := methods.DecodeSessionsGroupsRenameParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		updated, err := sessionCoordinator.RenameGroup(ctx, req.Name, req.To)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "updatedSessions": updated}}, true, nil
	case methods.MethodSessionsGroupsDelete:
		if sessionCoordinator == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("session group service unavailable")
		}
		req, err := methods.DecodeSessionsGroupsDeleteParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		updated, err := sessionCoordinator.DeleteGroup(ctx, req.Name)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "updatedSessions": updated}}, true, nil
	case methods.MethodSessionsExport:
		exportReq, err := methods.DecodeSessionsExportParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("sessions.export: invalid params: %w", err)
		}
		exportReq, err = exportReq.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("sessions.export: %w", err)
		}
		exportResult, err := methods.ExportSessionHTML(ctx, docsRepo, transcriptRepo, exportReq.SessionID, in.FromPubKey)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: exportResult}, true, nil
	case methods.MethodSessionsPrune:
		var pruneReq methods.SessionsPruneRequest
		if len(in.Params) > 0 {
			_ = json.Unmarshal(in.Params, &pruneReq)
		}
		result, err := runSessionsPrune(ctx, docsRepo, transcriptRepo, pruneReq, "manual")
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodSessionsSpawn:
		req, err := methods.DecodeSessionsSpawnParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applySessionsSpawn(ctx, req, cfg, docsRepo, memoryIndex)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}
}
