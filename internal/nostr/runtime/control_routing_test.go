package runtime

import (
	"context"
	"testing"

	nostr "fiatjaf.com/nostr"
)

func TestControlRequestRelayCandidates_UseCallerWriteThenTargetRead(t *testing.T) {
	selector := NewRelaySelector([]string{"wss://fallback-read"}, []string{"wss://fallback-write"})
	selector.Put(&NIP65RelayList{
		PubKey: "caller",
		Entries: []NIP65RelayEntry{
			{URL: "wss://caller-write", Write: true},
			{URL: "wss://caller-read", Read: true},
		},
	})
	selector.Put(&NIP65RelayList{
		PubKey: "target",
		Entries: []NIP65RelayEntry{
			{URL: "wss://target-read", Read: true},
			{URL: "wss://target-write", Write: true},
		},
	})

	got := ControlRequestRelayCandidates(context.Background(), selector, &nostr.Pool{}, []string{"wss://query"}, "caller", "target")
	want := []string{"wss://caller-write", "wss://target-read"}
	if len(got) != len(want) {
		t.Fatalf("unexpected relay count: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected routing order: got %v want %v", got, want)
		}
	}
}

func TestControlRequestRelayCandidates_FallbackToConfiguredRelays(t *testing.T) {
	got := ControlRequestRelayCandidates(context.Background(), nil, nil, []string{" wss://one ", "wss://two", "wss://one"}, "caller", "target")
	want := []string{"wss://one", "wss://two"}
	if len(got) != len(want) {
		t.Fatalf("unexpected relay count: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected routing order: got %v want %v", got, want)
		}
	}
}

func TestControlResponseRelayCandidates_PreferRequestRelayThenResponderWriteThenRequesterRead(t *testing.T) {
	selector := NewRelaySelector([]string{"wss://fallback-read"}, []string{"wss://fallback-write"})
	selector.Put(&NIP65RelayList{
		PubKey: "responder",
		Entries: []NIP65RelayEntry{
			{URL: "wss://responder-write", Write: true},
		},
	})
	selector.Put(&NIP65RelayList{
		PubKey: "requester",
		Entries: []NIP65RelayEntry{
			{URL: "wss://requester-read", Read: true},
		},
	})

	got := ControlResponseRelayCandidates(context.Background(), selector, &nostr.Pool{}, []string{"wss://query"}, "responder", "requester", "wss://request-relay")
	want := []string{"wss://request-relay", "wss://responder-write", "wss://requester-read"}
	if len(got) != len(want) {
		t.Fatalf("unexpected relay count: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected routing order: got %v want %v", got, want)
		}
	}
}

func TestControlResponseListenRelayCandidates_IncludeRequestResponderAndRequesterRelays(t *testing.T) {
	selector := NewRelaySelector([]string{"wss://fallback-read"}, []string{"wss://fallback-write"})
	selector.Put(&NIP65RelayList{
		PubKey: "responder",
		Entries: []NIP65RelayEntry{
			{URL: "wss://responder-write", Write: true},
		},
	})
	selector.Put(&NIP65RelayList{
		PubKey: "requester",
		Entries: []NIP65RelayEntry{
			{URL: "wss://requester-read", Read: true},
		},
	})

	got := ControlResponseListenRelayCandidates(context.Background(), selector, &nostr.Pool{}, []string{"wss://query"}, "responder", "requester", []string{"wss://request-relay"})
	want := []string{"wss://request-relay", "wss://responder-write", "wss://requester-read"}
	if len(got) != len(want) {
		t.Fatalf("unexpected relay count: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected routing order: got %v want %v", got, want)
		}
	}
}
