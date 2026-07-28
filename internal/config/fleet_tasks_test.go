package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metiq/internal/store/state"
)

func TestFleetTasksConfigLoadAndValidate(t *testing.T) {
	pubkey := strings.Repeat("a", 64)
	path := filepath.Join(t.TempDir(), "config.json")
	raw := `{
		"fleet_tasks":{
			"enabled":true,
			"trusted_pubkeys":["` + pubkey + `"],
			"trusted_collection_pubkeys":["` + pubkey + `"],
			"collection_sources":[{"author":"` + pubkey + `","type":"queue","id":"fleet"}],
			"relays":["wss://relay.example"],
			"max_future_skew_seconds":120,
			"claim_settlement_seconds":10
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	doc, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("LoadConfigFile: %v", err)
	}
	if !doc.FleetTasks.Enabled || len(doc.FleetTasks.TrustedPubKeys) != 1 ||
		len(doc.FleetTasks.CollectionSources) != 1 ||
		doc.FleetTasks.MaxFutureSkewSeconds != 120 ||
		doc.FleetTasks.ClaimSettlementSeconds != 10 {
		t.Fatalf("unexpected fleet config: %#v", doc.FleetTasks)
	}
	if errs := ValidateConfigDoc(doc); len(errs) != 0 {
		t.Fatalf("ValidateConfigDoc: %v", errs)
	}
}

func TestFleetTasksConfigFailsClosed(t *testing.T) {
	errs := ValidateConfigDoc(state.ConfigDoc{FleetTasks: state.FleetTasksConfig{
		Enabled:                  true,
		TrustedPubKeys:           []string{"not-a-pubkey"},
		TrustedCollectionPubKeys: []string{strings.Repeat("b", 64), strings.Repeat("b", 64)},
		Relays:                   []string{"https://not-a-relay"},
		MaxFutureSkewSeconds:     -1,
		ClaimSettlementSeconds:   -1,
		CollectionSources: []state.FleetTaskCollectionSource{{
			Author: strings.Repeat("c", 64), Type: "other",
		}},
	}})
	if len(errs) < 7 {
		t.Fatalf("expected fleet validation failures, got %v", errs)
	}
}
