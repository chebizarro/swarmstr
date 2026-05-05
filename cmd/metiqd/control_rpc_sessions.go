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

	"metiq/internal/admin"
	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

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
	hooksMgr := h.deps.hooksMgr
	mediaTranscriber := h.deps.mediaTranscriber
	toolRegistry := h.deps.toolRegistry

	switch method {
	case methods.MethodChatSend:
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
	case methods.MethodChatAbort:
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
	case methods.MethodSessionGet:
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
			entries, err := transcriptRepo.ListSessionAll(ctx, req.SessionID)
			if err != nil {
				return err
			}
			dropped := len(entries) - req.Keep
			if dropped < 0 {
				dropped = 0
			}
			summaryGenerated := false
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
						summaryEntryID := fmt.Sprintf("compact-summary-%d", time.Now().UnixMilli())
						summaryEntry := state.TranscriptEntryDoc{Version: 1, SessionID: req.SessionID, EntryID: summaryEntryID, Role: "system", Text: "[Compact summary of " + strconv.Itoa(dropped) + " earlier messages]\n\n" + strings.TrimSpace(result.Text), Unix: time.Now().Unix(), Meta: map[string]any{"compact": true, "compact_from": dropped}}
						if _, putErr := transcriptRepo.PutEntry(ctx, summaryEntry); putErr != nil {
							log.Printf("sessions.compact: insert summary entry: %v", putErr)
						} else {
							summaryGenerated = true
						}
					} else if summaryErr != nil {
						log.Printf("sessions.compact: LLM summary skipped: %v", summaryErr)
					}
				}
			}
			deleteErrors := 0
			for i := 0; i < dropped; i++ {
				if delErr := transcriptRepo.DeleteEntry(ctx, req.SessionID, entries[i].EntryID); delErr != nil {
					log.Printf("sessions.compact: delete entry %s: %v", entries[i].EntryID, delErr)
					deleteErrors++
				}
			}
			if _, err := updateExistingSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
				session.Meta = mergeSessionMeta(session.Meta, map[string]any{"compacted_at": time.Now().Unix(), "compacted_keep": req.Keep, "compacted_from_entries": len(entries), "compacted_dropped_entries": dropped - deleteErrors, "compacted_summary": summaryGenerated})
				return nil
			}); err != nil {
				return err
			}
			compactResult = methods.ApplyCompatResponseAliases(map[string]any{"ok": true, "session_id": req.SessionID, "kept": req.Keep, "from_entries": len(entries), "dropped": dropped - deleteErrors, "summary_generated": summaryGenerated})
			recordSessionCompaction(sessionStore, req.SessionID, strings.TrimSpace(flushOutcome.Path) != "", time.Now())
			return nil
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: compactResult}, true, nil
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
