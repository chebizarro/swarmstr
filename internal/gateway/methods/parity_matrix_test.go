package methods

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type methodParitySnapshot struct {
	Source  string `json:"source"`
	Summary struct {
		OpenClawMethodCount int `json:"openclaw_method_count"`
		Implemented         int `json:"implemented"`
		Partial             int `json:"partial"`
		Missing             int `json:"missing"`
	} `json:"summary"`
	Entries []methodParityEntry `json:"entries"`
}

type methodParityEntry struct {
	Method         string `json:"method"`
	Status         string `json:"status"`
	SwarmstrMethod string `json:"swarmstr_method"`
	Notes          string `json:"notes"`
}

func TestGatewayMethodParityMatrixIsConsistent(t *testing.T) {
	path := filepath.Join("testdata", "parity", "gateway-method-parity.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read parity matrix: %v", err)
	}
	var snap methodParitySnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("parse parity matrix: %v", err)
	}
	if len(snap.Entries) == 0 {
		t.Fatal("parity matrix has no entries")
	}
	if snap.Summary.OpenClawMethodCount != len(snap.Entries) {
		t.Fatalf("summary method count mismatch: summary=%d entries=%d", snap.Summary.OpenClawMethodCount, len(snap.Entries))
	}

	supported := map[string]struct{}{}
	for _, method := range SupportedMethods() {
		supported[method] = struct{}{}
	}

	seen := map[string]struct{}{}
	implemented := 0
	partial := 0
	missing := 0
	for _, entry := range snap.Entries {
		if _, ok := seen[entry.Method]; ok {
			t.Fatalf("duplicate method entry in parity matrix: %s", entry.Method)
		}
		seen[entry.Method] = struct{}{}
		switch entry.Status {
		case "implemented":
			implemented++
			if _, ok := supported[entry.Method]; !ok {
				t.Fatalf("implemented method not supported in swarmstr: %s", entry.Method)
			}
		case "partial":
			partial++
			if entry.SwarmstrMethod == "" {
				t.Fatalf("partial method must define swarmstr_method: %s", entry.Method)
			}
			if _, ok := supported[entry.SwarmstrMethod]; !ok {
				t.Fatalf("partial mapping target not supported in swarmstr: %s -> %s", entry.Method, entry.SwarmstrMethod)
			}
		case "missing":
			missing++
			if _, ok := supported[entry.Method]; ok {
				t.Fatalf("method marked missing but supported in swarmstr: %s", entry.Method)
			}
		default:
			t.Fatalf("unknown status %q for method %s", entry.Status, entry.Method)
		}
	}

	if implemented != snap.Summary.Implemented || partial != snap.Summary.Partial || missing != snap.Summary.Missing {
		t.Fatalf("summary status mismatch: got implemented=%d partial=%d missing=%d; summary implemented=%d partial=%d missing=%d",
			implemented, partial, missing,
			snap.Summary.Implemented, snap.Summary.Partial, snap.Summary.Missing,
		)
	}
}
