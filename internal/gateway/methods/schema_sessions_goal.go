package methods

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	SessionGoalMaxObjectiveBytes = 16_000
	SessionGoalMaxNoteBytes      = 2_000
	SessionGoalMaxOperationBytes = 128
)

type SessionsGoalUpdateRequest struct {
	SessionKey  string `json:"sessionKey"`
	AgentID     string `json:"agent_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	GoalID      string `json:"goal_id"`
	OperationID string `json:"operationId"`
	IssuedAtMS  int64  `json:"issuedAtMs"`
	Action      string `json:"action"`
	Objective   string `json:"objective,omitempty"`
	Note        string `json:"note,omitempty"`
}

type SessionsGoalClearRequest struct {
	SessionKey  string `json:"sessionKey"`
	AgentID     string `json:"agent_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	GoalID      string `json:"goal_id"`
	OperationID string `json:"operationId"`
	IssuedAtMS  int64  `json:"issuedAtMs"`
}

func normalizeGoalIdentity(sessionKey, agentID, sessionID, goalID, operationID string, issuedAtMS int64) (string, string, string, string, string, error) {
	sessionKey = strings.TrimSpace(sessionKey)
	agentID = strings.TrimSpace(agentID)
	sessionID = strings.TrimSpace(sessionID)
	goalID = strings.TrimSpace(goalID)
	operationID = strings.TrimSpace(operationID)
	if sessionKey == "" {
		return "", "", "", "", "", fmt.Errorf("sessionKey is required")
	}
	if goalID == "" {
		return "", "", "", "", "", fmt.Errorf("goalId is required")
	}
	if operationID == "" || len(operationID) > SessionGoalMaxOperationBytes {
		return "", "", "", "", "", fmt.Errorf("operationId must contain 1-%d bytes", SessionGoalMaxOperationBytes)
	}
	now := time.Now().UnixMilli()
	if issuedAtMS < 0 || issuedAtMS > now+5*time.Minute.Milliseconds() {
		return "", "", "", "", "", fmt.Errorf("issuedAtMs is invalid")
	}
	if issuedAtMS+24*time.Hour.Milliseconds() <= now {
		return "", "", "", "", "", fmt.Errorf("goal operation expired")
	}
	return sessionKey, agentID, sessionID, goalID, operationID, nil
}

func (r SessionsGoalUpdateRequest) Normalize() (SessionsGoalUpdateRequest, error) {
	var err error
	r.SessionKey, r.AgentID, r.SessionID, r.GoalID, r.OperationID, err = normalizeGoalIdentity(r.SessionKey, r.AgentID, r.SessionID, r.GoalID, r.OperationID, r.IssuedAtMS)
	if err != nil {
		return r, fmt.Errorf("sessions.goal.update: %w", err)
	}
	r.Action = strings.TrimSpace(strings.ToLower(r.Action))
	r.Note = strings.TrimSpace(r.Note)
	switch r.Action {
	case "edit":
		r.Objective = strings.TrimSpace(r.Objective)
		if r.Objective == "" || len(r.Objective) > SessionGoalMaxObjectiveBytes {
			return r, fmt.Errorf("sessions.goal.update: objective must contain 1-%d bytes for edit", SessionGoalMaxObjectiveBytes)
		}
		if r.Note != "" {
			return r, fmt.Errorf("sessions.goal.update: note is not valid for edit")
		}
	case "pause", "resume", "complete", "block":
		if strings.TrimSpace(r.Objective) != "" {
			return r, fmt.Errorf("sessions.goal.update: objective is only valid for edit")
		}
		if len(r.Note) > SessionGoalMaxNoteBytes {
			return r, fmt.Errorf("sessions.goal.update: note exceeds %d bytes", SessionGoalMaxNoteBytes)
		}
	default:
		return r, fmt.Errorf("sessions.goal.update: action must be edit, pause, resume, complete, or block")
	}
	return r, nil
}

func (r SessionsGoalClearRequest) Normalize() (SessionsGoalClearRequest, error) {
	var err error
	r.SessionKey, r.AgentID, r.SessionID, r.GoalID, r.OperationID, err = normalizeGoalIdentity(r.SessionKey, r.AgentID, r.SessionID, r.GoalID, r.OperationID, r.IssuedAtMS)
	if err != nil {
		return r, fmt.Errorf("sessions.goal.clear: %w", err)
	}
	return r, nil
}

func DecodeSessionsGoalUpdateParams(params json.RawMessage) (SessionsGoalUpdateRequest, error) {
	return decodeMethodParams[SessionsGoalUpdateRequest](params)
}

func DecodeSessionsGoalClearParams(params json.RawMessage) (SessionsGoalClearRequest, error) {
	return decodeMethodParams[SessionsGoalClearRequest](params)
}
