package terminal

import (
	"strings"
	"testing"
)

func TestManagerAttachTakesOverOwnershipWithReplay(t *testing.T) {
	em := &recordingEmitter{}
	m := NewManager(Options{Emitter: em})
	res, err := m.Open(OpenRequest{ConnID: "conn-a", Shell: "/bin/sh", Cwd: "/", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Shutdown()
	if !m.Write("conn-a", res.SessionID, "echo replay-marker\n") {
		t.Fatal("write returned false")
	}
	waitFor(t, func() bool {
		snap, ok := m.Snapshot(res.SessionID)
		return ok && strings.Contains(snap, "replay-marker")
	})

	attached, ok := m.Attach("conn-b", res.SessionID)
	if !ok {
		t.Fatal("attach returned false")
	}
	if attached.SessionID != res.SessionID || attached.Shell != res.Shell || attached.Cwd != res.Cwd {
		t.Fatalf("unexpected attach result: %+v", attached)
	}
	if !strings.Contains(attached.Buffer, "replay-marker") {
		t.Fatalf("attach buffer missing replay: %q", attached.Buffer)
	}
	if attached.Seq <= 0 {
		t.Fatalf("attach seq not advanced: %d", attached.Seq)
	}

	// The previous owner is notified with a detached exit and loses access.
	waitFor(t, func() bool {
		for _, ev := range em.drain() {
			if ev.conn != "conn-a" || ev.event != EventExit {
				continue
			}
			if exit, ok := ev.payload.(ExitEvent); ok && exit.Reason == ExitReasonDetached && exit.SessionID == res.SessionID {
				return true
			}
		}
		return false
	})
	if m.Write("conn-a", res.SessionID, "echo old-owner\n") {
		t.Fatal("previous owner can still write after take-over")
	}

	// The new owner interacts and receives the live stream.
	if !m.Write("conn-b", res.SessionID, "echo new-owner-stream\n") {
		t.Fatal("new owner write returned false")
	}
	waitFor(t, func() bool {
		for _, ev := range em.drain() {
			if ev.event != EventData || ev.conn != "conn-b" {
				continue
			}
			if d, ok := ev.payload.(DataEvent); ok && strings.Contains(d.Data, "new-owner-stream") {
				return d.Seq > attached.Seq
			}
		}
		return false
	})
}

func TestManagerAttachSameConnectionEmitsNoDetach(t *testing.T) {
	em := &recordingEmitter{}
	m := NewManager(Options{Emitter: em})
	res, err := m.Open(OpenRequest{ConnID: "conn-a", Shell: "/bin/sh", Cwd: "/", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Shutdown()
	if _, ok := m.Attach("conn-a", res.SessionID); !ok {
		t.Fatal("re-attach by owner returned false")
	}
	for _, ev := range em.drain() {
		if ev.event == EventExit {
			t.Fatalf("unexpected exit event on self-attach: %+v", ev.payload)
		}
	}
	if !m.Write("conn-a", res.SessionID, "echo still-mine\n") {
		t.Fatal("owner lost access after self-attach")
	}
}

func TestManagerAttachUnknownSession(t *testing.T) {
	m := NewManager(Options{Emitter: &recordingEmitter{}})
	if _, ok := m.Attach("conn-a", "nope"); ok {
		t.Fatal("attach to unknown session succeeded")
	}
	if _, ok := m.Snapshot("nope"); ok {
		t.Fatal("snapshot of unknown session succeeded")
	}
	if _, ok := m.SessionCwd("conn-a", "nope"); ok {
		t.Fatal("cwd of unknown session succeeded")
	}
}

func TestManagerListReportsOwnershipMetadata(t *testing.T) {
	em := &recordingEmitter{}
	m := NewManager(Options{Emitter: em})
	res, err := m.Open(OpenRequest{ConnID: "conn-a", AgentID: "agent-1", Shell: "/bin/sh", Cwd: "/", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Shutdown()
	list := m.List()
	if len(list) != 1 {
		t.Fatalf("unexpected list length: %d", len(list))
	}
	info := list[0]
	if info.SessionID != res.SessionID || info.AgentID != "agent-1" || !info.Attached || info.Owner != "conn" || info.CreatedAtMs <= 0 {
		t.Fatalf("unexpected session info: %+v", info)
	}
}

func TestManagerSessionCwdRequiresOwnership(t *testing.T) {
	m := NewManager(Options{Emitter: &recordingEmitter{}})
	res, err := m.Open(OpenRequest{ConnID: "conn-a", Shell: "/bin/sh", Cwd: "/", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Shutdown()
	if cwd, ok := m.SessionCwd("conn-a", res.SessionID); !ok || cwd != "/" {
		t.Fatalf("owner cwd lookup failed: %q %v", cwd, ok)
	}
	if _, ok := m.SessionCwd("conn-b", res.SessionID); ok {
		t.Fatal("non-owner cwd lookup succeeded")
	}
}
