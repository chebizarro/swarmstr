package methods

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Question method schemas. Params mirror the OpenClaw question.* wire
// contract (packages/gateway-protocol/src/schema/questions.ts); lifecycle
// state is backed by internal/gateway/questions.

const (
	maxQuestionHeaderLen   = 12
	maxQuestionsPerRequest = 3
	maxQuestionOptions     = 4
)

var questionIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// QuestionOptionParam is one selectable answer on the wire.
type QuestionOptionParam struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// QuestionInputParam is one unnormalized question accepted by
// question.request.
type QuestionInputParam struct {
	QuestionID  string                `json:"questionId"`
	Header      string                `json:"header"`
	Question    string                `json:"question"`
	Options     []QuestionOptionParam `json:"options"`
	MultiSelect bool                  `json:"multiSelect,omitempty"`
	IsOther     bool                  `json:"isOther,omitempty"`
	IsSecret    bool                  `json:"isSecret,omitempty"`
}

type QuestionRequestRequest struct {
	ID         string               `json:"id,omitempty"`
	Questions  []QuestionInputParam `json:"questions"`
	AgentID    string               `json:"agent_id,omitempty"`
	SessionKey string               `json:"sessionKey,omitempty"`
	TimeoutMS  int                  `json:"timeout_ms,omitempty"`
}

func (r QuestionRequestRequest) Normalize() (QuestionRequestRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	if r.TimeoutMS < 0 {
		return r, fmt.Errorf("timeout_ms must be positive")
	}
	if len(r.Questions) == 0 {
		return r, fmt.Errorf("questions requires at least one question")
	}
	if len(r.Questions) > maxQuestionsPerRequest {
		return r, fmt.Errorf("questions accepts at most %d questions", maxQuestionsPerRequest)
	}
	seen := map[string]struct{}{}
	for i, q := range r.Questions {
		q.QuestionID = strings.TrimSpace(q.QuestionID)
		q.Question = strings.TrimSpace(q.Question)
		if !questionIDPattern.MatchString(q.QuestionID) {
			return r, fmt.Errorf("questionId must match %s", questionIDPattern.String())
		}
		if _, dup := seen[q.QuestionID]; dup {
			return r, fmt.Errorf("duplicate question id %q", q.QuestionID)
		}
		seen[q.QuestionID] = struct{}{}
		if len(q.Header) > maxQuestionHeaderLen {
			return r, fmt.Errorf("question %q header exceeds %d characters", q.QuestionID, maxQuestionHeaderLen)
		}
		if q.Question == "" {
			return r, fmt.Errorf("question %q requires question text", q.QuestionID)
		}
		if len(q.Options) > maxQuestionOptions {
			return r, fmt.Errorf("question %q accepts at most %d options", q.QuestionID, maxQuestionOptions)
		}
		if len(q.Options) == 1 {
			return r, fmt.Errorf("question %q must have either no options or 2 to %d options", q.QuestionID, maxQuestionOptions)
		}
		if q.IsSecret {
			return r, fmt.Errorf("question %q: secret questions are not supported yet", q.QuestionID)
		}
		labels := map[string]struct{}{}
		for j, option := range q.Options {
			option.Label = strings.TrimSpace(option.Label)
			if option.Label == "" {
				return r, fmt.Errorf("question %q option requires a label", q.QuestionID)
			}
			normalized := strings.ToLower(option.Label)
			if _, dup := labels[normalized]; dup {
				return r, fmt.Errorf("question %q has duplicate option label %q", q.QuestionID, option.Label)
			}
			labels[normalized] = struct{}{}
			q.Options[j] = option
		}
		r.Questions[i] = q
	}
	return r, nil
}

type QuestionWaitAnswerRequest struct {
	ID        string `json:"id"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

func (r QuestionWaitAnswerRequest) Normalize() (QuestionWaitAnswerRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		return r, fmt.Errorf("id is required")
	}
	if r.TimeoutMS < 0 {
		return r, fmt.Errorf("timeout_ms must be positive")
	}
	return r, nil
}

// QuestionAnswersParam is the wire answers wrapper {"answers":{...}}.
type QuestionAnswersParam struct {
	Answers map[string][]string `json:"answers"`
}

type QuestionResolveRequest struct {
	ID         string                `json:"id"`
	Answers    *QuestionAnswersParam `json:"answers,omitempty"`
	Cancel     bool                  `json:"cancel,omitempty"`
	ResolvedBy string                `json:"resolvedBy,omitempty"`
}

func (r QuestionResolveRequest) Normalize() (QuestionResolveRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	r.ResolvedBy = strings.TrimSpace(r.ResolvedBy)
	if r.ID == "" {
		return r, fmt.Errorf("id is required")
	}
	if r.Cancel {
		if r.Answers != nil {
			return r, fmt.Errorf("cancel and answers are mutually exclusive")
		}
		return r, nil
	}
	if r.Answers == nil || r.Answers.Answers == nil {
		return r, fmt.Errorf("answers is required unless cancel is true")
	}
	return r, nil
}

type QuestionGetRequest struct {
	ID string `json:"id"`
}

func (r QuestionGetRequest) Normalize() (QuestionGetRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		return r, fmt.Errorf("id is required")
	}
	return r, nil
}

type QuestionListRequest struct{}

func DecodeQuestionRequestParams(params json.RawMessage) (QuestionRequestRequest, error) {
	return decodeMethodParams[QuestionRequestRequest](params)
}

func DecodeQuestionWaitAnswerParams(params json.RawMessage) (QuestionWaitAnswerRequest, error) {
	return decodeMethodParams[QuestionWaitAnswerRequest](params)
}

func DecodeQuestionResolveParams(params json.RawMessage) (QuestionResolveRequest, error) {
	return decodeMethodParams[QuestionResolveRequest](params)
}

func DecodeQuestionGetParams(params json.RawMessage) (QuestionGetRequest, error) {
	return decodeMethodParams[QuestionGetRequest](params)
}

func DecodeQuestionListParams(params json.RawMessage) (QuestionListRequest, error) {
	return decodeMethodParams[QuestionListRequest](params)
}
