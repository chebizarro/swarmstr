package methods

import (
	"errors"
	"testing"

	"swarmstr/internal/store/state"
)

func TestCheckBaseHash_emptyPassesThrough(t *testing.T) {
	doc := state.ConfigDoc{Version: 1, DM: state.DMPolicy{Policy: "open"}}
	// Empty base_hash = no check.
	if err := CheckBaseHash(doc, ""); err != nil {
		t.Errorf("empty baseHash should pass: %v", err)
	}
}

func TestCheckBaseHash_matchPasses(t *testing.T) {
	doc := state.ConfigDoc{Version: 1, DM: state.DMPolicy{Policy: "open"}}
	hash := doc.Hash()
	if err := CheckBaseHash(doc, hash); err != nil {
		t.Errorf("matching hash should pass: %v", err)
	}
}

func TestCheckBaseHash_mismatchFails(t *testing.T) {
	doc := state.ConfigDoc{Version: 1, DM: state.DMPolicy{Policy: "open"}}
	err := CheckBaseHash(doc, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err == nil {
		t.Error("mismatched hash should return error")
	}
	if !errors.Is(err, ErrConfigConflict) {
		t.Errorf("error should be ErrConfigConflict, got: %v", err)
	}
}

func TestCheckBaseHash_whitespaceStripped(t *testing.T) {
	doc := state.ConfigDoc{Version: 1}
	hash := doc.Hash()
	// Surrounding whitespace in the hash string should be stripped.
	if err := CheckBaseHash(doc, "  "+hash+"  "); err != nil {
		t.Errorf("hash with surrounding whitespace should pass: %v", err)
	}
}

func TestCheckBaseHash_staleAfterMutation(t *testing.T) {
	doc := state.ConfigDoc{Version: 1, DM: state.DMPolicy{Policy: "open"}}
	hashBefore := doc.Hash()
	// Mutate the document.
	doc.DM.Policy = "disabled"
	// Now the old hash is stale.
	err := CheckBaseHash(doc, hashBefore)
	if err == nil {
		t.Error("stale hash should fail after mutation")
	}
	if !errors.Is(err, ErrConfigConflict) {
		t.Errorf("expected ErrConfigConflict, got: %v", err)
	}
}
