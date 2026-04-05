package toolbuiltin

import (
	"strings"
	"testing"
)

func TestResolveFleetTargetEntryRejectsAmbiguousName(t *testing.T) {
	_, _, err := ResolveFleetTargetEntry("wizard", func() []FleetEntry {
		return []FleetEntry{
			{Pubkey: "1111111111111111111111111111111111111111111111111111111111111111", Name: "Wizard"},
			{Pubkey: "2222222222222222222222222222222222222222222222222222222222222222", Name: "Wizard", Runtime: "metiq", ACPVersion: 1},
		}
	})
	if err == nil || !strings.Contains(err.Error(), "multiple fleet agents named") {
		t.Fatalf("err = %v, want ambiguous-name failure", err)
	}
}
