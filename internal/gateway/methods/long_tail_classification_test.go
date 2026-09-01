package methods

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type longTailFamily struct {
	Family  string   `json:"family"`
	Methods []string `json:"methods"`
	Issue   string   `json:"issue,omitempty"`
}

type longTailFixture struct {
	Implemented            []longTailFamily `json:"implemented"`
	Deferred               []longTailFamily `json:"deferred"`
	IntentionalDivergences []longTailFamily `json:"intentional_divergences"`
}

type triageDivergenceSummary struct {
	NonAdvertisedDivergences []struct {
		Family    string `json:"family"`
		Rationale string `json:"rationale"`
	} `json:"non_advertised_divergences"`
}

func parityDocPath(t *testing.T, name string) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	return filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "docs", "parity", name)
}

// The advertised-method triage matrix cannot classify OpenClaw methods that are
// hidden or absent from the pinned descriptor catalog, so gateway-triage.json
// mirrors those families as a prose summary. Keep that summary in lockstep with
// the authoritative per-method ledger.
func TestTriageDivergenceSummaryMatchesLongTailLedger(t *testing.T) {
	raw, err := os.ReadFile(parityDocPath(t, "gateway-long-tail-2026-08.json"))
	if err != nil {
		t.Fatalf("read long-tail fixture: %v", err)
	}
	var fixture longTailFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode long-tail fixture: %v", err)
	}
	triageRaw, err := os.ReadFile(parityDocPath(t, "gateway-triage.json"))
	if err != nil {
		t.Fatalf("read triage config: %v", err)
	}
	var summary triageDivergenceSummary
	if err := json.Unmarshal(triageRaw, &summary); err != nil {
		t.Fatalf("decode triage config: %v", err)
	}

	want := map[string]struct{}{}
	for _, family := range fixture.IntentionalDivergences {
		want[family.Family] = struct{}{}
	}
	got := map[string]struct{}{}
	for _, entry := range summary.NonAdvertisedDivergences {
		if entry.Rationale == "" {
			t.Errorf("triage divergence family %q has no rationale", entry.Family)
		}
		if _, dup := got[entry.Family]; dup {
			t.Errorf("triage divergence family %q listed twice", entry.Family)
		}
		got[entry.Family] = struct{}{}
	}
	for family := range want {
		if _, ok := got[family]; !ok {
			t.Errorf("gateway-triage.json is missing divergence family %q", family)
		}
	}
	for family := range got {
		if _, ok := want[family]; !ok {
			t.Errorf("gateway-triage.json has stale divergence family %q", family)
		}
	}
}

func TestLongTailClassificationMatchesAdvertisedCatalog(t *testing.T) {
	raw, err := os.ReadFile(parityDocPath(t, "gateway-long-tail-2026-08.json"))
	if err != nil {
		t.Fatalf("read long-tail fixture: %v", err)
	}
	var fixture longTailFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode long-tail fixture: %v", err)
	}

	supported := make(map[string]struct{}, len(SupportedMethods()))
	for _, method := range SupportedMethods() {
		supported[method] = struct{}{}
	}
	classified := map[string]string{}
	checkUnique := func(status string, families []longTailFamily) {
		t.Helper()
		for _, family := range families {
			if family.Family == "" || len(family.Methods) == 0 {
				t.Fatalf("%s contains empty family: %+v", status, family)
			}
			for _, method := range family.Methods {
				if prior, exists := classified[method]; exists {
					t.Fatalf("method %s classified as both %s and %s", method, prior, status)
				}
				classified[method] = status
			}
		}
	}
	checkUnique("implemented", fixture.Implemented)
	checkUnique("deferred", fixture.Deferred)
	checkUnique("intentional-divergence", fixture.IntentionalDivergences)

	for _, family := range fixture.Implemented {
		for _, method := range family.Methods {
			if _, ok := supported[method]; !ok {
				t.Errorf("implemented method %s is not advertised", method)
			}
		}
	}
	for _, families := range [][]longTailFamily{fixture.Deferred, fixture.IntentionalDivergences} {
		for _, family := range families {
			for _, method := range family.Methods {
				if _, ok := supported[method]; ok {
					t.Errorf("%s method %s must not be advertised", classified[method], method)
				}
			}
		}
	}
}
