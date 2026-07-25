package pluginapproval

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestRequestListGetAndResolveApprove(t *testing.T) {
	m := NewManager()
	rec, err := m.Request(RequestParams{
		PluginID: "weather",
		Action:   "network.fetch",
		Reason:   "fetch forecast",
		Detail:   map[string]any{"host": "example.com"},
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if rec.ID == "" || rec.Status != StatusPending || rec.ExpiresAtMs <= rec.CreatedAtMs {
		t.Fatalf("unexpected record: %+v", rec)
	}

	list := m.List()
	if len(list) != 1 || list[0].ID != rec.ID {
		t.Fatalf("unexpected list: %+v", list)
	}

	got, err := m.Get(rec.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Action != "network.fetch" || got.Detail["host"] != "example.com" {
		t.Fatalf("unexpected record fields: %+v", got)
	}

	resolved, err := m.Resolve(rec.ID, DecisionApprove, "operator", "ok")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Status != StatusApproved || resolved.Decision != DecisionApprove || resolved.DecidedBy != "operator" || resolved.Note != "ok" {
		t.Fatalf("unexpected resolved record: %+v", resolved)
	}
	if len(m.List()) != 0 {
		t.Fatalf("resolved approval should not remain pending")
	}

	// WaitDecision after resolution returns immediately with the decision.
	wr, err := m.WaitDecision(context.Background(), rec.ID, 0)
	if err != nil {
		t.Fatalf("WaitDecision: %v", err)
	}
	if wr.Status != StatusApproved || wr.Decision != DecisionApprove {
		t.Fatalf("unexpected wait result: %+v", wr)
	}
}

func TestResolveDenyAndDoubleResolve(t *testing.T) {
	m := NewManager()
	rec, err := m.Request(RequestParams{Action: "storage.write"})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	denied, err := m.Resolve(rec.ID, DecisionDeny, "op", "nope")
	if err != nil {
		t.Fatalf("Resolve deny: %v", err)
	}
	if denied.Status != StatusDenied || denied.Decision != DecisionDeny {
		t.Fatalf("unexpected denied record: %+v", denied)
	}
	// Second resolve is rejected as already terminal.
	_, err = m.Resolve(rec.ID, DecisionApprove, "op", "")
	var perr *Error
	if !errors.As(err, &perr) || perr.Code != ErrCodeAlreadyTerminal {
		t.Fatalf("expected already-terminal error, got %v", err)
	}
}

func TestRequestValidation(t *testing.T) {
	m := NewManager()
	if _, err := m.Request(RequestParams{Action: "   "}); err == nil {
		t.Fatal("expected error for empty action")
	}
	if _, err := m.Resolve("missing", DecisionApprove, "op", ""); err == nil {
		t.Fatal("expected not-found error")
	}
	rec, _ := m.Request(RequestParams{Action: "x"})
	if _, err := m.Resolve(rec.ID, "maybe", "op", ""); err == nil {
		t.Fatal("expected invalid-decision error")
	}
}

func TestWaitDecisionBlocksUntilResolved(t *testing.T) {
	m := NewManager()
	rec, err := m.Request(RequestParams{Action: "network.fetch", TimeoutMS: 60_000})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	done := make(chan WaitResult, 1)
	go func() {
		wr, waitErr := m.WaitDecision(context.Background(), rec.ID, 5_000)
		if waitErr != nil {
			t.Errorf("WaitDecision: %v", waitErr)
		}
		done <- wr
	}()
	// Give the waiter a moment to register, then resolve.
	time.Sleep(20 * time.Millisecond)
	if _, err := m.Resolve(rec.ID, DecisionApprove, "op", "granted"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	select {
	case wr := <-done:
		if wr.Status != StatusApproved || wr.Decision != DecisionApprove || wr.Note != "granted" {
			t.Fatalf("unexpected wait result: %+v", wr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitDecision did not wake on resolution")
	}
}

func TestWaitDecisionWaitTimeoutReturnsPending(t *testing.T) {
	m := NewManager()
	rec, err := m.Request(RequestParams{Action: "x", TimeoutMS: 60_000})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	wr, err := m.WaitDecision(context.Background(), rec.ID, 30)
	if err != nil {
		t.Fatalf("WaitDecision: %v", err)
	}
	if wr.Status != StatusPending {
		t.Fatalf("expected pending on wait timeout, got %+v", wr)
	}
}

func TestExpiryTransitionsToExpired(t *testing.T) {
	m := NewManager()
	rec, err := m.Request(RequestParams{Action: "x", TimeoutMS: 1})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	got, err := m.Get(rec.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusExpired {
		t.Fatalf("expected expired, got %+v", got)
	}
	if len(m.List()) != 0 {
		t.Fatalf("expired approval should not remain pending")
	}
}

func TestDurableLedgerSurvivesReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugin-approval-ledger.json")
	m1, err := NewManagerAt(path)
	if err != nil {
		t.Fatalf("NewManagerAt: %v", err)
	}
	rec, err := m1.Request(RequestParams{ID: "appr-1", Action: "network.fetch", TimeoutMS: 600_000})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}

	m2, err := NewManagerAt(path)
	if err != nil {
		t.Fatalf("reload NewManagerAt: %v", err)
	}
	got, err := m2.Get(rec.ID)
	if err != nil {
		t.Fatalf("reload Get: %v", err)
	}
	if got.Status != StatusPending || got.ID != "appr-1" {
		t.Fatalf("reloaded record mismatch: %+v", got)
	}
	// Resolving on the reloaded manager persists too.
	if _, err := m2.Resolve("appr-1", DecisionApprove, "op", ""); err != nil {
		t.Fatalf("Resolve after reload: %v", err)
	}
	m3, err := NewManagerAt(path)
	if err != nil {
		t.Fatalf("second reload: %v", err)
	}
	got, err = m3.Get("appr-1")
	if err != nil {
		t.Fatalf("second reload Get: %v", err)
	}
	if got.Status != StatusApproved {
		t.Fatalf("decision not persisted across reload: %+v", got)
	}
}

func TestDuplicateExplicitID(t *testing.T) {
	m := NewManager()
	if _, err := m.Request(RequestParams{ID: "dup", Action: "x"}); err != nil {
		t.Fatalf("first Request: %v", err)
	}
	_, err := m.Request(RequestParams{ID: "dup", Action: "y"})
	var perr *Error
	if !errors.As(err, &perr) || perr.Code != ErrCodeIDInUse {
		t.Fatalf("expected id-in-use error, got %v", err)
	}
}
