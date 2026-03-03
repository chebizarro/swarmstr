package state

import "testing"

func TestIsNewerEvent_ByTimestamp(t *testing.T) {
	a := Event{ID: "a", CreatedAt: 10}
	b := Event{ID: "b", CreatedAt: 9}
	if !isNewerEvent(a, b) {
		t.Fatal("expected newer timestamp to win")
	}
	if isNewerEvent(b, a) {
		t.Fatal("older timestamp must not win")
	}
}

func TestIsNewerEvent_TieBreakByID(t *testing.T) {
	a := Event{ID: "bbb", CreatedAt: 10}
	b := Event{ID: "aaa", CreatedAt: 10}
	if !isNewerEvent(a, b) {
		t.Fatal("expected lexicographically larger id to win on tie")
	}
	if isNewerEvent(b, a) {
		t.Fatal("lexicographically smaller id must not win on tie")
	}
}
