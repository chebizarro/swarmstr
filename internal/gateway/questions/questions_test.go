package questions

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func testQuestions() []Question {
	return []Question{
		{
			QuestionID: "approach",
			Header:     "Approach",
			Question:   "Which approach should I take?",
			Options:    []Option{{Label: "Refactor"}, {Label: "Rewrite"}},
		},
	}
}

func TestRequestGetListLifecycle(t *testing.T) {
	m := NewManager()
	rec, err := m.Request(RequestParams{Questions: testQuestions(), SessionKey: "sess", AgentID: "main"})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if rec.Status != StatusPending || rec.ID == "" || rec.ExpiresAtMs <= rec.CreatedAtMs {
		t.Fatalf("unexpected record: %+v", rec)
	}
	got, err := m.Get(rec.ID)
	if err != nil || got.ID != rec.ID || got.SessionKey != "sess" {
		t.Fatalf("get: %+v err=%v", got, err)
	}
	list := m.List()
	if len(list) != 1 || list[0].ID != rec.ID {
		t.Fatalf("list: %+v", list)
	}
}

func TestRequestExplicitIDConflict(t *testing.T) {
	m := NewManager()
	if _, err := m.Request(RequestParams{ID: "q-1", Questions: testQuestions()}); err != nil {
		t.Fatalf("request: %v", err)
	}
	_, err := m.Request(RequestParams{ID: "q-1", Questions: testQuestions()})
	qErr, ok := err.(*Error)
	if !ok || qErr.Code != ErrCodeIDInUse {
		t.Fatalf("expected ID_IN_USE, got %v", err)
	}
}

func TestResolveCanonicalizesAnswers(t *testing.T) {
	m := NewManager()
	rec, err := m.Request(RequestParams{Questions: testQuestions()})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	// Trim-insensitive match maps back to the declared option label.
	_, result, err := m.Resolve(rec.ID, AnswerSet{Answers: map[string][]string{"approach": {"  Refactor "}}}, "operator")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if result.Status != StatusAnswered || result.Answers.Answers["approach"][0] != "Refactor" {
		t.Fatalf("unexpected resolve result: %+v", result)
	}
	got, err := m.Get(rec.ID)
	if err != nil || got.Status != StatusAnswered || got.ResolvedBy != "operator" {
		t.Fatalf("resolved record: %+v err=%v", got, err)
	}
	// A second resolve reports the terminal state.
	if _, _, err := m.Resolve(rec.ID, AnswerSet{Answers: map[string][]string{"approach": {"Refactor"}}}, ""); err == nil {
		t.Fatal("expected already-terminal error")
	}
}

func TestResolveRejectsInvalidAnswers(t *testing.T) {
	m := NewManager()
	rec, _ := m.Request(RequestParams{Questions: testQuestions()})
	cases := []AnswerSet{
		{Answers: map[string][]string{"unknown": {"x"}}},                       // not part of request
		{Answers: map[string][]string{}},                                       // missing answer
		{Answers: map[string][]string{"approach": {""}}},                       // empty answer
		{Answers: map[string][]string{"approach": {"Refactor", "Rewrite"}}},    // multi on single-select
		{Answers: map[string][]string{"approach": {"Something else"}}},         // unknown option
	}
	for i, answers := range cases {
		if _, _, err := m.Resolve(rec.ID, answers, ""); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
	// Question must still be pending after failed resolves.
	got, err := m.Get(rec.ID)
	if err != nil || got.Status != StatusPending {
		t.Fatalf("record no longer pending: %+v err=%v", got, err)
	}
}

func TestResolveAllowsFreeTextWhenIsOther(t *testing.T) {
	m := NewManager()
	qs := testQuestions()
	qs[0].IsOther = true
	rec, _ := m.Request(RequestParams{Questions: qs})
	_, result, err := m.Resolve(rec.ID, AnswerSet{Answers: map[string][]string{"approach": {"Do nothing"}}}, "")
	if err != nil || result.Answers.Answers["approach"][0] != "Do nothing" {
		t.Fatalf("isOther free text rejected: %+v err=%v", result, err)
	}
}

func TestCancelReleasesWaiters(t *testing.T) {
	m := NewManager()
	rec, _ := m.Request(RequestParams{Questions: testQuestions()})
	done := make(chan WaitResult, 1)
	go func() {
		result, _ := m.WaitAnswer(context.Background(), rec.ID, 0)
		done <- result
	}()
	// Give the waiter a moment to register.
	time.Sleep(20 * time.Millisecond)
	if _, result, err := m.Cancel(rec.ID, "operator"); err != nil || result.Status != StatusCancelled {
		t.Fatalf("cancel: %+v err=%v", result, err)
	}
	select {
	case result := <-done:
		if result.Status != StatusCancelled {
			t.Fatalf("waiter saw %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter was not released")
	}
}

func TestWaitAnswerTimeoutReturnsPending(t *testing.T) {
	m := NewManager()
	rec, _ := m.Request(RequestParams{Questions: testQuestions()})
	result, err := m.WaitAnswer(context.Background(), rec.ID, 20)
	if err != nil || result.Status != StatusPending {
		t.Fatalf("expected pending on wait timeout: %+v err=%v", result, err)
	}
}

func TestWaitAnswerOnResolvedReturnsImmediately(t *testing.T) {
	m := NewManager()
	rec, _ := m.Request(RequestParams{Questions: testQuestions()})
	if _, _, err := m.Resolve(rec.ID, AnswerSet{Answers: map[string][]string{"approach": {"Rewrite"}}}, ""); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	result, err := m.WaitAnswer(context.Background(), rec.ID, 0)
	if err != nil || result.Status != StatusAnswered || result.Answers.Answers["approach"][0] != "Rewrite" {
		t.Fatalf("unexpected wait result: %+v err=%v", result, err)
	}
}

func TestExpiryTransitionsAndNotifiesHook(t *testing.T) {
	m := NewManager()
	expired := make(chan Record, 1)
	m.SetExpiryHook(func(rec Record) { expired <- rec })
	rec, _ := m.Request(RequestParams{Questions: testQuestions(), TimeoutMS: 1})
	time.Sleep(10 * time.Millisecond)
	got, err := m.Get(rec.ID)
	if err != nil || got.Status != StatusExpired {
		t.Fatalf("expected expired record: %+v err=%v", got, err)
	}
	select {
	case hookRec := <-expired:
		if hookRec.ID != rec.ID || hookRec.Status != StatusExpired {
			t.Fatalf("hook saw %+v", hookRec)
		}
	default:
		t.Fatal("expiry hook did not fire")
	}
	if list := m.List(); len(list) != 0 {
		t.Fatalf("expired question still listed: %+v", list)
	}
}

func TestDurableLedgerSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "question-ledger.json")
	m1, err := NewManagerAt(path)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	rec, err := m1.Request(RequestParams{Questions: testQuestions(), SessionKey: "sess"})
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	// Reconnect recovery: a fresh manager over the same ledger still knows
	// the pending question and can resolve it.
	m2, err := NewManagerAt(path)
	if err != nil {
		t.Fatalf("reopen ledger: %v", err)
	}
	got, err := m2.Get(rec.ID)
	if err != nil || got.Status != StatusPending || got.SessionKey != "sess" {
		t.Fatalf("pending question lost across restart: %+v err=%v", got, err)
	}
	if _, result, err := m2.Resolve(rec.ID, AnswerSet{Answers: map[string][]string{"approach": {"Refactor"}}}, "op"); err != nil || result.Status != StatusAnswered {
		t.Fatalf("resolve after restart: %+v err=%v", result, err)
	}
}
