package state

import (
	"strings"
	"testing"
)

func TestConfigDocHash_deterministic(t *testing.T) {
	doc := ConfigDoc{
		Version: 1,
		DM:      DMPolicy{Policy: "open"},
		Relays: RelayPolicy{
			Read:  []string{"wss://relay.example.com"},
			Write: []string{"wss://relay.example.com"},
		},
	}
	h1 := doc.Hash()
	h2 := doc.Hash()
	if h1 != h2 {
		t.Errorf("Hash() is not deterministic: %q != %q", h1, h2)
	}
	if h1 == "" {
		t.Error("Hash() returned empty string")
	}
	if len(h1) != 64 {
		t.Errorf("Hash() returned unexpected length %d (want 64 hex chars)", len(h1))
	}
}

func TestConfigDocHash_changesOnMutation(t *testing.T) {
	doc := ConfigDoc{Version: 1, DM: DMPolicy{Policy: "open"}}
	h1 := doc.Hash()
	doc.DM.Policy = "disabled"
	h2 := doc.Hash()
	if h1 == h2 {
		t.Error("Hash() should change when content changes")
	}
}

func TestConfigDocHash_format(t *testing.T) {
	doc := ConfigDoc{Version: 1}
	h := doc.Hash()
	if !strings.HasPrefix(h, "") {
		t.Errorf("unexpected hash format: %q", h)
	}
	// Should be lowercase hex.
	for _, c := range h {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("Hash() contains non-hex character %q in %q", c, h)
			break
		}
	}
}

func TestConfigDocHash_emptyDoc(t *testing.T) {
	doc := ConfigDoc{}
	h := doc.Hash()
	if h == "" {
		t.Error("Hash() of empty ConfigDoc returned empty string")
	}
}

func TestConfigDocHash_extraFields(t *testing.T) {
	doc := ConfigDoc{
		Version: 1,
		Extra:   map[string]any{"key": "value"},
	}
	h1 := doc.Hash()
	doc.Extra["key"] = "different"
	h2 := doc.Hash()
	if h1 == h2 {
		t.Error("Hash() should change when Extra content changes")
	}
}
