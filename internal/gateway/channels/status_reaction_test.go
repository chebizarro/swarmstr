package channels

import (
	"context"
	"sync"
	"testing"
	"time"
)

// mockReactionHandle records AddReaction/RemoveReaction calls.
type mockReactionHandle struct {
	mu      sync.Mutex
	added   []string
	removed []string
}

func (m *mockReactionHandle) ID() string                          { return "mock" }
func (m *mockReactionHandle) Send(_ context.Context, _ string) error { return nil }
func (m *mockReactionHandle) Close()                               {}

func (m *mockReactionHandle) AddReaction(_ context.Context, _, emoji string) error {
	m.mu.Lock()
	m.added = append(m.added, emoji)
	m.mu.Unlock()
	return nil
}

func (m *mockReactionHandle) RemoveReaction(_ context.Context, _, emoji string) error {
	m.mu.Lock()
	m.removed = append(m.removed, emoji)
	m.mu.Unlock()
	return nil
}

func (m *mockReactionHandle) counts() (added, removed int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.added), len(m.removed)
}

func (m *mockReactionHandle) lastAdded() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.added) == 0 {
		return ""
	}
	return m.added[len(m.added)-1]
}

func TestStatusReactionController_SetDone(t *testing.T) {
	rh := &mockReactionHandle{}
	ctrl := NewStatusReactionController(context.Background(), rh, "evt1")

	ctrl.SetQueued()
	ctrl.SetThinking()
	ctrl.SetDone()
	ctrl.Close()

	// Give a moment for async ops to settle.
	time.Sleep(50 * time.Millisecond)

	if rh.lastAdded() != EmojiDone {
		t.Fatalf("last added emoji: got %q want %q", rh.lastAdded(), EmojiDone)
	}
}

func TestStatusReactionController_SetError(t *testing.T) {
	rh := &mockReactionHandle{}
	ctrl := NewStatusReactionController(context.Background(), rh, "evt1")

	ctrl.SetQueued()
	ctrl.SetError()
	ctrl.Close()

	time.Sleep(50 * time.Millisecond)

	if rh.lastAdded() != EmojiError {
		t.Fatalf("got %q want %q", rh.lastAdded(), EmojiError)
	}
}

func TestStatusReactionController_SetTool_Classification(t *testing.T) {
	cases := []struct {
		tool  string
		emoji string
	}{
		{"web_search", EmojiToolWeb},
		{"bash_exec", EmojiToolCode},
		{"memory_store", EmojiToolFire},
	}
	for _, tc := range cases {
		rh := &mockReactionHandle{}
		ctrl := NewStatusReactionController(context.Background(), rh, "e")
		ctrl.SetTool(tc.tool)
		ctrl.Close()
		time.Sleep(50 * time.Millisecond)
		if got := rh.lastAdded(); got != tc.emoji {
			t.Errorf("tool=%q: got %q want %q", tc.tool, got, tc.emoji)
		}
	}
}

func TestStatusReactionController_ThinkingDebounce(t *testing.T) {
	rh := &mockReactionHandle{}
	ctrl := NewStatusReactionController(context.Background(), rh, "evt1")

	ctrl.SetThinking()
	// SetDone before debounce fires should cancel thinking emoji.
	ctrl.SetDone()
	ctrl.Close()
	time.Sleep(200 * time.Millisecond)

	for _, e := range rh.added {
		if e == EmojiThinking {
			t.Fatalf("thinking emoji should not have been added (debounce should have been cancelled)")
		}
	}
}

func TestStatusReactionController_Clear(t *testing.T) {
	rh := &mockReactionHandle{}
	ctrl := NewStatusReactionController(context.Background(), rh, "evt1")

	ctrl.SetQueued()
	ctrl.Clear()
	ctrl.Close()
	time.Sleep(50 * time.Millisecond)

	added, removed := rh.counts()
	if added == 0 {
		t.Fatal("expected at least one add")
	}
	if removed == 0 {
		t.Fatal("expected remove after clear")
	}
	_ = added
}
