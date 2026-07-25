package methods

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Task suggestion method schemas. Params mirror the OpenClaw
// taskSuggestions.* wire contract
// (packages/gateway-protocol/src/schema/task-suggestions.ts); the suggestion
// registry is backed by internal/gateway/tasksuggestions.

const (
	maxTaskSuggestionIDLen         = 128
	maxTaskSuggestionTitleLen      = 60
	maxTaskSuggestionPromptLen     = 32_768
	maxTaskSuggestionTldrLen       = 1_024
	maxTaskSuggestionCwdLen        = 4_096
	maxTaskSuggestionSessionKeyLen = 512
	maxTaskSuggestionAgentIDLen    = 128
	maxTaskSuggestionReasonLen     = 1_024
)

type TaskSuggestionsListRequest struct {
	SessionKey string `json:"sessionKey,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
}

func (r TaskSuggestionsListRequest) Normalize() (TaskSuggestionsListRequest, error) {
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	r.AgentID = strings.TrimSpace(r.AgentID)
	if len(r.SessionKey) > maxTaskSuggestionSessionKeyLen {
		return r, fmt.Errorf("sessionKey exceeds %d characters", maxTaskSuggestionSessionKeyLen)
	}
	if len(r.AgentID) > maxTaskSuggestionAgentIDLen {
		return r, fmt.Errorf("agent_id exceeds %d characters", maxTaskSuggestionAgentIDLen)
	}
	return r, nil
}

type TaskSuggestionsCreateRequest struct {
	Title      string `json:"title"`
	Prompt     string `json:"prompt"`
	Tldr       string `json:"tldr"`
	CWD        string `json:"cwd"`
	SessionKey string `json:"sessionKey"`
	AgentID    string `json:"agent_id,omitempty"`
}

func (r TaskSuggestionsCreateRequest) Normalize() (TaskSuggestionsCreateRequest, error) {
	r.Title = strings.TrimSpace(r.Title)
	r.Prompt = strings.TrimSpace(r.Prompt)
	r.Tldr = strings.TrimSpace(r.Tldr)
	r.CWD = strings.TrimSpace(r.CWD)
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	r.AgentID = strings.TrimSpace(r.AgentID)
	if r.Title == "" || len(r.Title) > maxTaskSuggestionTitleLen {
		return r, fmt.Errorf("title is required and accepts at most %d characters", maxTaskSuggestionTitleLen)
	}
	if r.Prompt == "" || len(r.Prompt) > maxTaskSuggestionPromptLen {
		return r, fmt.Errorf("prompt is required and accepts at most %d characters", maxTaskSuggestionPromptLen)
	}
	if r.Tldr == "" || len(r.Tldr) > maxTaskSuggestionTldrLen {
		return r, fmt.Errorf("tldr is required and accepts at most %d characters", maxTaskSuggestionTldrLen)
	}
	if r.CWD == "" || len(r.CWD) > maxTaskSuggestionCwdLen {
		return r, fmt.Errorf("cwd is required and accepts at most %d characters", maxTaskSuggestionCwdLen)
	}
	if !filepath.IsAbs(r.CWD) {
		return r, fmt.Errorf("task suggestion cwd must be absolute")
	}
	if r.SessionKey == "" || len(r.SessionKey) > maxTaskSuggestionSessionKeyLen {
		return r, fmt.Errorf("sessionKey is required and accepts at most %d characters", maxTaskSuggestionSessionKeyLen)
	}
	if len(r.AgentID) > maxTaskSuggestionAgentIDLen {
		return r, fmt.Errorf("agent_id exceeds %d characters", maxTaskSuggestionAgentIDLen)
	}
	return r, nil
}

type TaskSuggestionsAcceptRequest struct {
	TaskID string `json:"task_id"`
}

func (r TaskSuggestionsAcceptRequest) Normalize() (TaskSuggestionsAcceptRequest, error) {
	r.TaskID = strings.TrimSpace(r.TaskID)
	if r.TaskID == "" || len(r.TaskID) > maxTaskSuggestionIDLen {
		return r, fmt.Errorf("task_id is required and accepts at most %d characters", maxTaskSuggestionIDLen)
	}
	return r, nil
}

type TaskSuggestionsDismissRequest struct {
	TaskID string `json:"task_id"`
	Reason string `json:"reason,omitempty"`
}

func (r TaskSuggestionsDismissRequest) Normalize() (TaskSuggestionsDismissRequest, error) {
	r.TaskID = strings.TrimSpace(r.TaskID)
	r.Reason = strings.TrimSpace(r.Reason)
	if r.TaskID == "" || len(r.TaskID) > maxTaskSuggestionIDLen {
		return r, fmt.Errorf("task_id is required and accepts at most %d characters", maxTaskSuggestionIDLen)
	}
	if len(r.Reason) > maxTaskSuggestionReasonLen {
		return r, fmt.Errorf("reason exceeds %d characters", maxTaskSuggestionReasonLen)
	}
	return r, nil
}

func DecodeTaskSuggestionsListParams(params json.RawMessage) (TaskSuggestionsListRequest, error) {
	return decodeMethodParams[TaskSuggestionsListRequest](params)
}

func DecodeTaskSuggestionsCreateParams(params json.RawMessage) (TaskSuggestionsCreateRequest, error) {
	return decodeMethodParams[TaskSuggestionsCreateRequest](params)
}

func DecodeTaskSuggestionsAcceptParams(params json.RawMessage) (TaskSuggestionsAcceptRequest, error) {
	return decodeMethodParams[TaskSuggestionsAcceptRequest](params)
}

func DecodeTaskSuggestionsDismissParams(params json.RawMessage) (TaskSuggestionsDismissRequest, error) {
	return decodeMethodParams[TaskSuggestionsDismissRequest](params)
}
