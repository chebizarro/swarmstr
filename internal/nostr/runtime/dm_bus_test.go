package runtime

import (
	"strings"
	"testing"
)

func TestDMBusSetRelays(t *testing.T) {
	b := &DMBus{relays: []string{"wss://one"}}
	in := []string{"wss://two", "wss://two", " wss://three "}
	if err := b.SetRelays(in); err != nil {
		t.Fatalf("set relays error: %v", err)
	}
	in[0] = "wss://mutated"
	got := b.currentRelays()
	if len(got) != 2 {
		t.Fatalf("unexpected relay count: %v", got)
	}
	if got[0] != "wss://two" || got[1] != "wss://three" {
		t.Fatalf("unexpected relays: %v", got)
	}
}

func TestSanitizeDMText(t *testing.T) {
	text, err := sanitizeDMText("  hello ")
	if err != nil {
		t.Fatalf("unexpected sanitize error: %v", err)
	}
	if text != "hello" {
		t.Fatalf("unexpected sanitized text: %q", text)
	}
}

func TestSanitizeDMTextRejectsTooLong(t *testing.T) {
	_, err := sanitizeDMText(strings.Repeat("a", maxDMPlaintextRunes+1))
	if err == nil {
		t.Fatal("expected too long error")
	}
}
