package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"metiq/internal/autoreply"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

const (
	sessionGoalMetaKey     = "goal"
	sessionGoalReceiptsKey = "goal_operation_receipts"
	maxSessionGoalReceipts = 128
)

type durableSessionGoal struct {
	SchemaVersion     int    `json:"schemaVersion"`
	ID                string `json:"id"`
	Objective         string `json:"objective"`
	Status            string `json:"status"`
	CreatedAt         int64  `json:"createdAt"`
	UpdatedAt         int64  `json:"updatedAt"`
	TokenStart        int64  `json:"tokenStart"`
	TokenStartFresh   bool   `json:"tokenStartFresh,omitempty"`
	TokensUsed        int64  `json:"tokensUsed"`
	ContinuationTurns int64  `json:"continuationTurns"`
	LastStatusNote    string `json:"lastStatusNote,omitempty"`
	PausedAt          int64  `json:"pausedAt,omitempty"`
	BlockedAt         int64  `json:"blockedAt,omitempty"`
	CompletedAt       int64  `json:"completedAt,omitempty"`
}

type sessionGoalMutationResult struct {
	OperationID string              `json:"operationId"`
	Action      string              `json:"action"`
	SessionID   string              `json:"sessionId"`
	GoalID      string              `json:"goalId"`
	Goal        *durableSessionGoal `json:"goal,omitempty"`
	RunID       string              `json:"runId,omitempty"`
	Replayed    bool                `json:"replayed,omitempty"`
	Status      string              `json:"status"`
}

type sessionGoalReceipt struct {
	OperationID string                    `json:"operation_id"`
	Fingerprint string                    `json:"fingerprint"`
	ExpiresAtMS int64                     `json:"expires_at_ms"`
	Result      sessionGoalMutationResult `json:"result"`
}

type sessionGoalOperation struct {
	SessionKey  string
	SessionID   string
	GoalID      string
	OperationID string
	IssuedAtMS  int64
	Action      string
	Objective   string
	Note        string
}

func decodeSessionGoalMeta(raw any) (*durableSessionGoal, error) {
	if raw == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var goal durableSessionGoal
	if err := json.Unmarshal(encoded, &goal); err != nil {
		return nil, err
	}
	if goal.SchemaVersion != 1 || strings.TrimSpace(goal.ID) == "" || strings.TrimSpace(goal.Objective) == "" {
		return nil, fmt.Errorf("stored session goal is invalid")
	}
	return &goal, nil
}

func decodeSessionGoalReceipts(raw any) ([]sessionGoalReceipt, error) {
	if raw == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var receipts []sessionGoalReceipt
	if err := json.Unmarshal(encoded, &receipts); err != nil {
		return nil, err
	}
	return receipts, nil
}

func sessionGoalFingerprint(op sessionGoalOperation) string {
	raw, _ := json.Marshal([]any{op.IssuedAtMS, op.SessionKey, op.SessionID, op.GoalID, op.Action, op.Objective, op.Note})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func applySessionGoalOperation(doc *state.SessionDoc, op sessionGoalOperation, nowMS int64) (sessionGoalMutationResult, bool, error) {
	if op.SessionID != "" && op.SessionID != doc.SessionID {
		return sessionGoalMutationResult{}, false, fmt.Errorf("session changed; refresh before changing its goal")
	}
	if doc.Meta == nil {
		doc.Meta = map[string]any{}
	}
	receipts, err := decodeSessionGoalReceipts(doc.Meta[sessionGoalReceiptsKey])
	if err != nil {
		return sessionGoalMutationResult{}, false, fmt.Errorf("decode goal receipts: %w", err)
	}
	fingerprint := sessionGoalFingerprint(op)
	kept := receipts[:0]
	for _, receipt := range receipts {
		if receipt.ExpiresAtMS <= nowMS {
			continue
		}
		kept = append(kept, receipt)
		if receipt.OperationID == op.OperationID {
			if receipt.Fingerprint != fingerprint {
				return sessionGoalMutationResult{}, false, fmt.Errorf("goal operation ID was already used for a different request")
			}
			replayed := receipt.Result
			replayed.Replayed = true
			return replayed, true, nil
		}
	}
	receipts = kept
	if len(receipts) >= maxSessionGoalReceipts {
		return sessionGoalMutationResult{}, false, fmt.Errorf("too many recent goal operations; wait for older requests to expire")
	}

	goal, err := decodeSessionGoalMeta(doc.Meta[sessionGoalMetaKey])
	if err != nil {
		return sessionGoalMutationResult{}, false, err
	}
	if goal == nil {
		// Metiq has no separate chat goal-start RPC. Treat the first edit as the
		// explicit creation transition so the lifecycle is usable without
		// fabricating goal state from unrelated session metadata.
		if op.Action != "edit" {
			return sessionGoalMutationResult{}, false, fmt.Errorf("goal not found")
		}
		goal = &durableSessionGoal{
			SchemaVersion: 1, ID: op.GoalID, Objective: op.Objective, Status: "active",
			CreatedAt: nowMS, UpdatedAt: nowMS, TokenStartFresh: true,
		}
	} else {
		if goal.ID != op.GoalID {
			return sessionGoalMutationResult{}, false, fmt.Errorf("goal changed or was cleared; refresh before trying again")
		}
		if goal.Status == "complete" && op.Action != "clear" && op.Action != "complete" {
			return sessionGoalMutationResult{}, false, fmt.Errorf("goal is already complete")
		}
		switch op.Action {
		case "edit":
			goal.Objective = op.Objective
		case "pause":
			goal.Status = "paused"
			goal.PausedAt = nowMS
		case "resume":
			goal.Status = "active"
			goal.ContinuationTurns++
		case "block":
			goal.Status = "blocked"
			goal.BlockedAt = nowMS
		case "complete":
			goal.Status = "complete"
			goal.CompletedAt = nowMS
		case "clear":
			goal = nil
		default:
			return sessionGoalMutationResult{}, false, fmt.Errorf("unsupported goal action %q", op.Action)
		}
		if goal != nil {
			goal.UpdatedAt = nowMS
			if op.Note != "" {
				goal.LastStatusNote = op.Note
			}
		}
	}

	result := sessionGoalMutationResult{
		OperationID: op.OperationID, Action: op.Action, SessionID: doc.SessionID,
		GoalID: op.GoalID, Goal: goal, Status: "updated",
	}
	if op.Action == "clear" {
		result.Status = "cleared"
		delete(doc.Meta, sessionGoalMetaKey)
	} else {
		doc.Meta[sessionGoalMetaKey] = goal
	}
	receipts = append(receipts, sessionGoalReceipt{
		OperationID: op.OperationID, Fingerprint: fingerprint,
		ExpiresAtMS: op.IssuedAtMS + 24*time.Hour.Milliseconds(), Result: result,
	})
	sort.Slice(receipts, func(i, j int) bool { return receipts[i].ExpiresAtMS < receipts[j].ExpiresAtMS })
	doc.Meta[sessionGoalReceiptsKey] = receipts
	return result, false, nil
}

func goalMutationResultMap(result sessionGoalMutationResult) (map[string]any, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (h controlRPCHandler) handleSessionGoalRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	if method != methods.MethodSessionsGoalUpdate && method != methods.MethodSessionsGoalClear {
		return nostruntime.ControlRPCResult{}, false, nil
	}
	if err := h.authorizeSessionMutationVisibility(ctx, in, method); err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	var op sessionGoalOperation
	switch method {
	case methods.MethodSessionsGoalUpdate:
		req, err := methods.DecodeSessionsGoalUpdateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		op = sessionGoalOperation{SessionKey: req.SessionKey, SessionID: req.SessionID, GoalID: req.GoalID, OperationID: req.OperationID, IssuedAtMS: req.IssuedAtMS, Action: req.Action, Objective: req.Objective, Note: req.Note}
	case methods.MethodSessionsGoalClear:
		req, err := methods.DecodeSessionsGoalClearParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		op = sessionGoalOperation{SessionKey: req.SessionKey, SessionID: req.SessionID, GoalID: req.GoalID, OperationID: req.OperationID, IssuedAtMS: req.IssuedAtMS, Action: "clear"}
	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}
	if h.deps.docsRepo == nil {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("session repository unavailable")
	}
	var mutation sessionGoalMutationResult
	var replayed bool
	_, err := updateExistingSessionDoc(ctx, h.deps.docsRepo, op.SessionKey, "", func(doc *state.SessionDoc) error {
		var applyErr error
		mutation, replayed, applyErr = applySessionGoalOperation(doc, op, time.Now().UnixMilli())
		return applyErr
	})
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}

	if op.Action == "resume" && !replayed {
		message := "Continue pursuing the current goal."
		if op.Note != "" {
			message += "\nOperator note: " + op.Note
		}
		queued := false
		if h.deps.steeringMailboxes != nil {
			if mailbox := h.deps.steeringMailboxes.GetIfExists(op.SessionKey); mailbox != nil {
				outcome := mailbox.EnqueueWithOutcome(autoreply.SteeringMessage{Text: message, EventID: op.OperationID, SenderID: in.FromPubKey, CreatedAt: time.Now().Unix(), Source: "gateway", Priority: autoreply.SteeringPriorityUrgent})
				if outcome.Overflowed && !outcome.Accepted {
					return nostruntime.ControlRPCResult{}, true, fmt.Errorf("session steering mailbox is full")
				}
				queued = outcome.Accepted || outcome.Deduped
			}
		}
		if !queued {
			params, marshalErr := json.Marshal(methods.AgentRequest{SessionID: op.SessionKey, Message: message, IdempotencyKey: op.OperationID})
			if marshalErr != nil {
				return nostruntime.ControlRPCResult{}, true, marshalErr
			}
			agentResult, handled, dispatchErr := h.handleAgentRPC(ctx, nostruntime.ControlRPCInbound{Method: methods.MethodAgent, Params: params, FromPubKey: in.FromPubKey, Internal: in.Internal}, methods.MethodAgent, cfg)
			if dispatchErr != nil {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("resume goal runtime: %w", dispatchErr)
			}
			if !handled {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("resume goal runtime unavailable")
			}
			if payload, ok := agentResult.Result.(map[string]any); ok {
				if runID, ok := payload["run_id"].(string); ok {
					mutation.RunID = runID
					mutation.Status = "started"
				}
			}
		}
	}
	result, err := goalMutationResultMap(mutation)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: result}, true, nil
}
