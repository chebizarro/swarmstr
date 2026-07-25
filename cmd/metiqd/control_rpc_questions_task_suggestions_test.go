package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/gateway/methods"
	questionspkg "metiq/internal/gateway/questions"
	tasksuggestionspkg "metiq/internal/gateway/tasksuggestions"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func questionSurfaceCall(t *testing.T, h controlRPCHandler, method string, params string) (nostruntime.ControlRPCResult, error) {
	t.Helper()
	result, handled, err := h.handleWorkspaceSurfaceRPC(context.Background(), nostruntime.ControlRPCInbound{
		Method: method,
		Params: json.RawMessage(params),
	}, method, state.ConfigDoc{})
	if !handled {
		t.Fatalf("method %s was not handled by workspace surface dispatch", method)
	}
	return result, err
}

func newQuestionTestHandler() controlRPCHandler {
	return newControlRPCHandler(controlRPCDeps{questions: questionspkg.NewManager()})
}

func TestQuestionRPCLifecycle(t *testing.T) {
	h := newQuestionTestHandler()

	result, err := questionSurfaceCall(t, h, methods.MethodQuestionRequest,
		`{"questions":[{"questionId":"approach","header":"Approach","question":"Which?","options":[{"label":"A"},{"label":"B"}]}],"sessionKey":"sess"}`)
	if err != nil {
		t.Fatalf("question.request: %v", err)
	}
	payload := result.Result.(map[string]any)
	id, _ := payload["id"].(string)
	if id == "" || payload["expiresAtMs"].(int64) <= 0 {
		t.Fatalf("unexpected request result: %#v", payload)
	}

	result, err = questionSurfaceCall(t, h, methods.MethodQuestionList, `{}`)
	if err != nil {
		t.Fatalf("question.list: %v", err)
	}
	questions := result.Result.(map[string]any)["questions"].([]questionspkg.Record)
	if len(questions) != 1 || questions[0].ID != id {
		t.Fatalf("unexpected list: %+v", questions)
	}

	result, err = questionSurfaceCall(t, h, methods.MethodQuestionGet, `{"id":"`+id+`"}`)
	if err != nil {
		t.Fatalf("question.get: %v", err)
	}
	got := result.Result.(map[string]any)["question"].(questionspkg.Record)
	if got.Status != questionspkg.StatusPending || got.SessionKey != "sess" {
		t.Fatalf("unexpected record: %+v", got)
	}

	result, err = questionSurfaceCall(t, h, methods.MethodQuestionResolve,
		`{"id":"`+id+`","answers":{"answers":{"approach":["A"]}},"resolvedBy":"op"}`)
	if err != nil {
		t.Fatalf("question.resolve: %v", err)
	}
	resolved := result.Result.(questionspkg.ResolveResult)
	if resolved.Status != questionspkg.StatusAnswered || resolved.Answers.Answers["approach"][0] != "A" {
		t.Fatalf("unexpected resolve result: %+v", resolved)
	}

	// waitAnswer after resolution returns immediately with the answer.
	result, err = questionSurfaceCall(t, h, methods.MethodQuestionWaitAnswer, `{"id":"`+id+`"}`)
	if err != nil {
		t.Fatalf("question.waitAnswer: %v", err)
	}
	wait := result.Result.(questionspkg.WaitResult)
	if wait.Status != questionspkg.StatusAnswered {
		t.Fatalf("unexpected wait result: %+v", wait)
	}
}

func TestQuestionRPCCancelAndErrors(t *testing.T) {
	h := newQuestionTestHandler()

	result, err := questionSurfaceCall(t, h, methods.MethodQuestionRequest,
		`{"id":"q_cancel","questions":[{"questionId":"go","header":"Go","question":"Proceed?","options":[]}]}`)
	if err != nil {
		t.Fatalf("question.request: %v", err)
	}
	if result.Result.(map[string]any)["id"].(string) != "q_cancel" {
		t.Fatalf("explicit id lost: %#v", result.Result)
	}

	result, err = questionSurfaceCall(t, h, methods.MethodQuestionResolve, `{"id":"q_cancel","cancel":true}`)
	if err != nil {
		t.Fatalf("question.resolve cancel: %v", err)
	}
	if result.Result.(questionspkg.ResolveResult).Status != questionspkg.StatusCancelled {
		t.Fatalf("unexpected cancel result: %#v", result.Result)
	}

	if _, err = questionSurfaceCall(t, h, methods.MethodQuestionGet, `{"id":"missing"}`); err == nil {
		t.Fatal("expected not-found error")
	}
	if _, err = questionSurfaceCall(t, h, methods.MethodQuestionRequest,
		`{"questions":[{"questionId":"secret","header":"S","question":"x","options":[],"isSecret":true}]}`); err == nil {
		t.Fatal("expected secret rejection")
	}

	// Surface disabled without a manager.
	bare := newControlRPCHandler(controlRPCDeps{})
	if _, err = questionSurfaceCall(t, bare, methods.MethodQuestionList, `{}`); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("expected unavailable error, got %v", err)
	}
}

func newTaskSuggestionTestHandler(t *testing.T) controlRPCHandler {
	t.Helper()
	backend := newTestStore()
	docs := state.NewDocsRepository(backend, "author")
	store, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	return newControlRPCHandler(controlRPCDeps{
		docsRepo:        docs,
		sessionStore:    store,
		taskSuggestions: tasksuggestionspkg.NewRegistry(),
	})
}

func TestTaskSuggestionsRPCLifecycle(t *testing.T) {
	h := newTaskSuggestionTestHandler(t)

	result, err := questionSurfaceCall(t, h, methods.MethodTaskSuggestionsCreate,
		`{"title":"Follow up","prompt":"Do the thing","tldr":"short","cwd":"/tmp/project","sessionKey":"sess","agentId":"main"}`)
	if err != nil {
		t.Fatalf("taskSuggestions.create: %v", err)
	}
	payload := result.Result.(map[string]any)
	taskID, _ := payload["taskId"].(string)
	if taskID == "" {
		t.Fatalf("missing taskId: %#v", payload)
	}

	result, err = questionSurfaceCall(t, h, methods.MethodTaskSuggestionsList, `{"sessionKey":"sess"}`)
	if err != nil {
		t.Fatalf("taskSuggestions.list: %v", err)
	}
	suggestions := result.Result.(map[string]any)["suggestions"].([]tasksuggestionspkg.Suggestion)
	if len(suggestions) != 1 || suggestions[0].ID != taskID {
		t.Fatalf("unexpected list: %+v", suggestions)
	}

	result, err = questionSurfaceCall(t, h, methods.MethodTaskSuggestionsAccept, `{"taskId":"`+taskID+`"}`)
	if err != nil {
		t.Fatalf("taskSuggestions.accept: %v", err)
	}
	accepted := result.Result.(map[string]any)
	key, _ := accepted["key"].(string)
	if key == "" || accepted["taskId"].(string) != taskID {
		t.Fatalf("unexpected accept result: %#v", accepted)
	}

	// The suggested session exists with the suggestion's metadata.
	session, err := h.deps.docsRepo.GetSession(context.Background(), key)
	if err != nil {
		t.Fatalf("suggested session missing: %v", err)
	}
	if session.Meta["label"] != "Follow up" || session.Meta["task"] != "Do the thing" || session.Meta["parent_session_key"] != "sess" {
		t.Fatalf("unexpected session meta: %#v", session.Meta)
	}

	// Retried accept returns the same idempotent result.
	result, err = questionSurfaceCall(t, h, methods.MethodTaskSuggestionsAccept, `{"taskId":"`+taskID+`"}`)
	if err != nil {
		t.Fatalf("retried accept: %v", err)
	}
	if result.Result.(map[string]any)["key"].(string) != key {
		t.Fatalf("retried accept returned different key: %#v", result.Result)
	}
}

func TestTaskSuggestionsRPCDismissAndValidation(t *testing.T) {
	h := newTaskSuggestionTestHandler(t)

	result, err := questionSurfaceCall(t, h, methods.MethodTaskSuggestionsCreate,
		`{"title":"Follow up","prompt":"p","tldr":"s","cwd":"/tmp","sessionKey":"sess"}`)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	taskID := result.Result.(map[string]any)["taskId"].(string)

	result, err = questionSurfaceCall(t, h, methods.MethodTaskSuggestionsDismiss, `{"taskId":"`+taskID+`"}`)
	if err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if dismissed := result.Result.(map[string]any)["dismissed"].(bool); !dismissed {
		t.Fatal("expected dismissed=true")
	}

	// Dismissed suggestions cannot be accepted.
	if _, err = questionSurfaceCall(t, h, methods.MethodTaskSuggestionsAccept, `{"taskId":"`+taskID+`"}`); err == nil {
		t.Fatal("expected accept failure for dismissed suggestion")
	}

	// Relative cwd is rejected at the schema layer.
	if _, err = questionSurfaceCall(t, h, methods.MethodTaskSuggestionsCreate,
		`{"title":"t","prompt":"p","tldr":"s","cwd":"relative","sessionKey":"sess"}`); err == nil {
		t.Fatal("expected relative cwd rejection")
	}
}
