package terminal

import (
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingEmitter struct {
	mu     sync.Mutex
	events []struct {
		conn    string
		event   string
		payload any
	}
}

func (e *recordingEmitter) EmitTo(connID, event string, payload any) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, struct {
		conn    string
		event   string
		payload any
	}{connID, event, payload})
	return true
}

func (e *recordingEmitter) drain() []struct {
	conn    string
	event   string
	payload any
} {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]struct {
		conn    string
		event   string
		payload any
	}{}, e.events...)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func TestManagerOpenStreamsDataToOwningConnection(t *testing.T) {
	em := &recordingEmitter{}
	m := NewManager(Options{Emitter: em})
	res, err := m.Open(OpenRequest{ConnID: "conn-1", Shell: "/bin/sh", Cwd: "/", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if res.SessionID == "" || res.Confined {
		t.Fatalf("unexpected open result: %+v", res)
	}
	if !m.Write("conn-1", res.SessionID, "echo hello-term\n") {
		t.Fatal("write returned false")
	}
	waitFor(t, func() bool {
		for _, ev := range em.drain() {
			if ev.event != EventData {
				continue
			}
			if d, ok := ev.payload.(DataEvent); ok && strings.Contains(d.Data, "hello-term") {
				return ev.conn == "conn-1"
			}
		}
		return false
	})
}

func TestManagerWriteRejectsNonOwner(t *testing.T) {
	m := NewManager(Options{Emitter: &recordingEmitter{}})
	res, err := m.Open(OpenRequest{ConnID: "owner", Shell: "/bin/sh", Cwd: "/", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if m.Write("intruder", res.SessionID, "id\n") {
		t.Fatal("non-owner write must be rejected")
	}
	if m.Resize("intruder", res.SessionID, 100, 40) {
		t.Fatal("non-owner resize must be rejected")
	}
	if m.Close("intruder", res.SessionID) {
		t.Fatal("non-owner close must be rejected")
	}
	m.Shutdown()
}

func TestManagerCloseEmitsExit(t *testing.T) {
	em := &recordingEmitter{}
	m := NewManager(Options{Emitter: em})
	res, err := m.Open(OpenRequest{ConnID: "conn", Shell: "/bin/sh", Cwd: "/", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !m.Close("conn", res.SessionID) {
		t.Fatal("close returned false")
	}
	waitFor(t, func() bool {
		for _, ev := range em.drain() {
			if ev.event == EventExit {
				if x, ok := ev.payload.(ExitEvent); ok {
					return x.SessionID == res.SessionID && x.Reason == ExitReasonClosed
				}
			}
		}
		return false
	})
	if m.Count() != 0 {
		t.Fatalf("expected no live sessions after close, got %d", m.Count())
	}
}

func TestManagerEnforcesSessionLimit(t *testing.T) {
	m := NewManager(Options{Emitter: &recordingEmitter{}, MaxSessions: 1})
	if _, err := m.Open(OpenRequest{ConnID: "c", Shell: "/bin/sh", Cwd: "/", Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, err := m.Open(OpenRequest{ConnID: "c", Shell: "/bin/sh", Cwd: "/", Cols: 80, Rows: 24}); err == nil {
		t.Fatal("expected limit error on second open")
	}
	m.Shutdown()
}

func TestManagerDropConnectionClosesOwnedSessions(t *testing.T) {
	em := &recordingEmitter{}
	m := NewManager(Options{Emitter: em})
	res, err := m.Open(OpenRequest{ConnID: "gone", Shell: "/bin/sh", Cwd: "/", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	m.DropConnection("gone")
	waitFor(t, func() bool { return m.Count() == 0 })
	sawDisconnect := false
	for _, ev := range em.drain() {
		if ev.event == EventExit {
			if x, ok := ev.payload.(ExitEvent); ok && x.SessionID == res.SessionID && x.Reason == ExitReasonDisconnected {
				sawDisconnect = true
			}
		}
	}
	if !sawDisconnect {
		t.Fatal("expected disconnected exit event")
	}
}

func TestUTF16Len(t *testing.T) {
	if got := utf16Len("abc"); got != 3 {
		t.Fatalf("ascii len: got %d", got)
	}
	if got := utf16Len("😀"); got != 2 {
		t.Fatalf("astral len: got %d want 2", got)
	}
}
