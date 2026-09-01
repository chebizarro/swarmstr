package methods

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type methodParitySnapshot struct {
	SchemaVersion int `json:"schema_version"`
	Summary       struct {
		OpenClawMethodCount  int            `json:"openclaw_method_count"`
		Implemented          int            `json:"implemented"`
		Partial              int            `json:"partial"`
		Missing              int            `json:"missing"`
		TriageCategoryCounts map[string]int `json:"triage_category_counts"`
	} `json:"summary"`
	Entries []methodParityEntry `json:"entries"`
}

type methodParityEntry struct {
	Method      string `json:"method"`
	Status      string `json:"status"`
	MetiqMethod string `json:"metiq_method"`
	Notes       string `json:"notes"`
	Triage      string `json:"triage"`
	TriageGroup string `json:"triage_group"`
	Workstream  string `json:"workstream"`
}

type gatewayTriageConfig struct {
	CategoryValues    []string          `json:"category_values"`
	MethodEquivalents map[string]string `json:"method_equivalents"`
	Groups            []struct {
		ID         string   `json:"id"`
		Prefixes   []string `json:"prefixes"`
		Category   string   `json:"category"`
		Workstream string   `json:"workstream"`
		Rationale  string   `json:"rationale"`
	} `json:"groups"`
}

func TestGatewayMethodParityMatrixIsConsistent(t *testing.T) {
	path := filepath.Join("..", "..", "..", "docs", "parity", "gateway-method-parity.json")
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
	if snap.SchemaVersion != 2 {
		t.Fatalf("unexpected parity matrix schema version: %d", snap.SchemaVersion)
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
	triageCounts := map[string]int{}
	for _, entry := range snap.Entries {
		if _, ok := seen[entry.Method]; ok {
			t.Fatalf("duplicate method entry in parity matrix: %s", entry.Method)
		}
		seen[entry.Method] = struct{}{}
		switch entry.Status {
		case "implemented":
			implemented++
			target := entry.Method
			if entry.MetiqMethod != "" {
				target = entry.MetiqMethod
			}
			if _, ok := supported[target]; !ok {
				t.Fatalf("implemented method target not supported in metiq: %s -> %s", entry.Method, target)
			}
		case "partial":
			partial++
			if entry.MetiqMethod == "" {
				t.Fatalf("partial method must define metiq_method: %s", entry.Method)
			}
			if _, ok := supported[entry.MetiqMethod]; !ok {
				t.Fatalf("partial mapping target not supported in metiq: %s -> %s", entry.Method, entry.MetiqMethod)
			}
		case "missing":
			missing++
			if _, ok := supported[entry.Method]; ok {
				t.Fatalf("method marked missing but supported in metiq: %s", entry.Method)
			}
		default:
			t.Fatalf("unknown status %q for method %s", entry.Status, entry.Method)
		}
		if entry.Triage == "" || entry.TriageGroup == "" || entry.Workstream == "" {
			t.Fatalf("method %s has incomplete triage metadata", entry.Method)
		}
		triageCounts[entry.Triage]++
	}

	if implemented != snap.Summary.Implemented || partial != snap.Summary.Partial || missing != snap.Summary.Missing {
		t.Fatalf("summary status mismatch: got implemented=%d partial=%d missing=%d; summary implemented=%d partial=%d missing=%d",
			implemented, partial, missing,
			snap.Summary.Implemented, snap.Summary.Partial, snap.Summary.Missing,
		)
	}
	if len(triageCounts) != len(snap.Summary.TriageCategoryCounts) {
		t.Fatalf("triage summary category mismatch: got=%v summary=%v", triageCounts, snap.Summary.TriageCategoryCounts)
	}
	for category, count := range triageCounts {
		if snap.Summary.TriageCategoryCounts[category] != count {
			t.Fatalf("triage summary mismatch for %s: got=%d summary=%d", category, count, snap.Summary.TriageCategoryCounts[category])
		}
	}
}

func TestGatewayMethodParityTriageMatchesSourceRules(t *testing.T) {
	docsPath := filepath.Join("..", "..", "..", "docs", "parity")
	var snap methodParitySnapshot
	readParityJSON(t, filepath.Join(docsPath, "gateway-method-parity.json"), &snap)
	var cfg gatewayTriageConfig
	readParityJSON(t, filepath.Join(docsPath, "gateway-triage.json"), &cfg)

	validCategories := map[string]struct{}{}
	for _, category := range cfg.CategoryValues {
		validCategories[category] = struct{}{}
	}
	type expectedTriage struct {
		category   string
		group      string
		workstream string
	}
	byPrefix := map[string]expectedTriage{}
	for _, group := range cfg.Groups {
		if _, ok := validCategories[group.Category]; !ok {
			t.Fatalf("triage group %s has unknown category %q", group.ID, group.Category)
		}
		if group.Rationale == "" {
			t.Fatalf("triage group %s has no rationale", group.ID)
		}
		for _, prefix := range group.Prefixes {
			if _, exists := byPrefix[prefix]; exists {
				t.Fatalf("triage prefix %q is classified more than once", prefix)
			}
			byPrefix[prefix] = expectedTriage{
				category: group.Category, group: group.ID,
				workstream: group.Workstream,
			}
		}
	}
	used := map[string]struct{}{}
	for _, entry := range snap.Entries {
		prefix := entry.Method
		if dot := strings.IndexByte(prefix, '.'); dot >= 0 {
			prefix = prefix[:dot]
		}
		want, ok := byPrefix[prefix]
		if !ok {
			t.Fatalf("method %s has no source triage rule", entry.Method)
		}
		used[prefix] = struct{}{}
		if nativeMethod := strings.TrimSpace(cfg.MethodEquivalents[entry.Method]); nativeMethod != "" {
			want.category = "implemented-native-equivalent"
			if entry.MetiqMethod != nativeMethod || entry.Status != "implemented" {
				t.Fatalf("native equivalent drift for %s: got=%+v want_target=%s", entry.Method, entry, nativeMethod)
			}
		}
		if entry.Triage != want.category || entry.TriageGroup != want.group ||
			entry.Workstream != want.workstream {
			t.Fatalf("generated triage drift for %s: got=%+v want=%+v", entry.Method, entry, want)
		}
	}
	for prefix := range byPrefix {
		if _, ok := used[prefix]; !ok {
			t.Fatalf("stale source triage prefix %q matches no generated method", prefix)
		}
	}
}

func readParityJSON(t *testing.T, path string, out any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read parity file %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("parse parity file %s: %v", path, err)
	}
}
