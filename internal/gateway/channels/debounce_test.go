package channels

import (
	"sync"
	"testing"
	"time"
)

func TestDebouncer_SingleMessage(t *testing.T) {
	var mu sync.Mutex
	var got []string

	d := NewDebouncer(50*time.Millisecond, func(key string, msgs []string) {
		mu.Lock()
		got = append(got, msgs...)
		mu.Unlock()
	})

	d.Submit("ch:user", "hello")
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("expected [hello], got %v", got)
	}
}

func TestDebouncer_Coalesces(t *testing.T) {
	var mu sync.Mutex
	var calls [][]string

	d := NewDebouncer(80*time.Millisecond, func(key string, msgs []string) {
		mu.Lock()
		calls = append(calls, msgs)
		mu.Unlock()
	})

	// Submit three messages quickly; they should be coalesced into one flush.
	d.Submit("ch:user", "a")
	time.Sleep(20 * time.Millisecond)
	d.Submit("ch:user", "b")
	time.Sleep(20 * time.Millisecond)
	d.Submit("ch:user", "c")

	// Wait for debounce window to expire.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("expected 1 flush call, got %d", len(calls))
	}
	if len(calls[0]) != 3 {
		t.Fatalf("expected 3 messages in flush, got %v", calls[0])
	}
}

func TestDebouncer_IndependentKeys(t *testing.T) {
	var mu sync.Mutex
	flushed := map[string][]string{}

	d := NewDebouncer(50*time.Millisecond, func(key string, msgs []string) {
		mu.Lock()
		flushed[key] = msgs
		mu.Unlock()
	})

	d.Submit("ch:alice", "hello alice")
	d.Submit("ch:bob", "hello bob")

	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(flushed["ch:alice"]) != 1 {
		t.Fatalf("alice: expected 1 message, got %v", flushed["ch:alice"])
	}
	if len(flushed["ch:bob"]) != 1 {
		t.Fatalf("bob: expected 1 message, got %v", flushed["ch:bob"])
	}
}

func TestDebouncer_FlushAll(t *testing.T) {
	var mu sync.Mutex
	var got []string

	d := NewDebouncer(5*time.Second, func(key string, msgs []string) {
		mu.Lock()
		got = append(got, msgs...)
		mu.Unlock()
	})

	d.Submit("ch:user", "urgent")
	d.FlushAll() // should fire immediately without waiting 5s

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "urgent" {
		t.Fatalf("expected [urgent], got %v", got)
	}
}

func TestDebounceKey(t *testing.T) {
	k := DebounceKey("slack-general", "U12345")
	if k != "slack-general:U12345" {
		t.Fatalf("unexpected key: %s", k)
	}
}

func TestJoinMessages(t *testing.T) {
	joined := JoinMessages([]string{"hello", "world"})
	if joined != "hello\nworld" {
		t.Fatalf("unexpected join: %q", joined)
	}
}
