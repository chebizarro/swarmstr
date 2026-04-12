package state

import (
	"testing"
)

func TestSessionActivityUnix(t *testing.T) {
	tests := []struct {
		name        string
		doc         SessionDoc
		wantActivity int64
	}{
		{"reply newer", SessionDoc{LastReplyAt: 200, LastInboundAt: 100}, 200},
		{"inbound newer", SessionDoc{LastReplyAt: 100, LastInboundAt: 200}, 200},
		{"equal", SessionDoc{LastReplyAt: 100, LastInboundAt: 100}, 100},
		{"both zero", SessionDoc{}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionActivityUnix(tc.doc)
			if got != tc.wantActivity {
				t.Errorf("sessionActivityUnix() = %d, want %d", got, tc.wantActivity)
			}
		})
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "  ", "hello"); got != "hello" {
		t.Errorf("expected hello, got %q", got)
	}
	if got := firstNonEmpty("first", "second"); got != "first" {
		t.Errorf("expected first, got %q", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	if got := firstNonEmpty(); got != "" {
		t.Errorf("expected empty for no args, got %q", got)
	}
}
