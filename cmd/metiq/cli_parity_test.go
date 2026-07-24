package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type cliParitySnapshot struct {
	SchemaVersion int `json:"schema_version"`
	Summary       struct {
		OpenClawCommandCount int            `json:"openclaw_command_count"`
		StatusCounts         map[string]int `json:"status_counts"`
	} `json:"summary"`
	Groups []cliParityEntry `json:"groups"`
}

type cliParityEntry struct {
	Name            string `json:"name"`
	ReferenceSource string `json:"reference_source"`
	Status          string `json:"status"`
	MetiqCommand    string `json:"metiq_command"`
	MetiqEntry      string `json:"metiq_entry"`
	Rationale       string `json:"rationale"`
}

type cliClassificationSource struct {
	StatusValues    []string                     `json:"status_values"`
	Classifications map[string]cliClassification `json:"classifications"`
}

type cliClassification struct {
	Status       string `json:"status"`
	MetiqCommand string `json:"metiq_command"`
	MetiqEntry   string `json:"metiq_entry"`
	Rationale    string `json:"rationale"`
}

func TestCLIParityCatalogMatchesClassificationsAndRegistry(t *testing.T) {
	docsPath := filepath.Join("..", "..", "docs", "parity")
	var snap cliParitySnapshot
	readCLIParityJSON(t, filepath.Join(docsPath, "cli-parity.json"), &snap)
	var source cliClassificationSource
	readCLIParityJSON(t, filepath.Join(docsPath, "cli-classifications.json"), &source)

	if snap.SchemaVersion != 2 {
		t.Fatalf("unexpected CLI parity schema version: %d", snap.SchemaVersion)
	}
	if snap.Summary.OpenClawCommandCount != len(snap.Groups) {
		t.Fatalf("CLI command summary mismatch: summary=%d groups=%d", snap.Summary.OpenClawCommandCount, len(snap.Groups))
	}

	validStatuses := map[string]struct{}{}
	for _, status := range source.StatusValues {
		validStatuses[status] = struct{}{}
	}
	registry := newCommandRegistry("")
	seen := map[string]struct{}{}
	statusCounts := map[string]int{}
	for _, entry := range snap.Groups {
		if _, ok := seen[entry.Name]; ok {
			t.Fatalf("duplicate CLI parity entry %q", entry.Name)
		}
		seen[entry.Name] = struct{}{}
		classification, ok := source.Classifications[entry.Name]
		if !ok {
			t.Fatalf("generated CLI entry %q has no classification source", entry.Name)
		}
		gotClassification := cliClassification{
			Status: entry.Status, MetiqCommand: entry.MetiqCommand,
			MetiqEntry: entry.MetiqEntry, Rationale: entry.Rationale,
		}
		if !reflect.DeepEqual(gotClassification, classification) {
			t.Fatalf("generated CLI classification drift for %q: got=%+v want=%+v", entry.Name, gotClassification, classification)
		}
		if _, ok := validStatuses[entry.Status]; !ok {
			t.Fatalf("CLI entry %q has unknown status %q", entry.Name, entry.Status)
		}
		if entry.ReferenceSource != "core" && entry.ReferenceSource != "subcli" {
			t.Fatalf("CLI entry %q has invalid reference source %q", entry.Name, entry.ReferenceSource)
		}
		if entry.Status != "implemented" && entry.Rationale == "" {
			t.Fatalf("CLI entry %q requires a rationale for status %q", entry.Name, entry.Status)
		}
		statusCounts[entry.Status]++

		target := entry.MetiqCommand
		if target == "" && entry.Status == "implemented" {
			target = entry.Name
		}
		if target != "" {
			if _, ok := registry.byName[target]; !ok {
				t.Fatalf("CLI entry %q maps to unregistered Metiq command %q", entry.Name, target)
			}
		}
	}
	if len(seen) != len(source.Classifications) {
		t.Fatalf("CLI classification count drift: generated=%d source=%d", len(seen), len(source.Classifications))
	}
	for name := range source.Classifications {
		if _, ok := seen[name]; !ok {
			t.Fatalf("stale CLI classification %q has no generated descriptor", name)
		}
	}
	if !reflect.DeepEqual(statusCounts, snap.Summary.StatusCounts) {
		t.Fatalf("CLI status summary drift: got=%v summary=%v", statusCounts, snap.Summary.StatusCounts)
	}

	assertCLIParityEntry(t, snap.Groups, "gateway", "deviation", "gw")
	assertCLIParityEntry(t, snap.Groups, "node", "deviation", "")
	for _, name := range []string{"claws", "audit", "promos", "exec-approvals", "users", "worker", "fleet", "worktrees", "attach"} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("refreshed OpenClaw CLI group %q is missing", name)
		}
	}
}

func assertCLIParityEntry(t *testing.T, entries []cliParityEntry, name, status, metiqCommand string) {
	t.Helper()
	for _, entry := range entries {
		if entry.Name != name {
			continue
		}
		if entry.Status != status || entry.MetiqCommand != metiqCommand {
			t.Fatalf("CLI entry %q = {status:%q metiq_command:%q}, want {status:%q metiq_command:%q}",
				name, entry.Status, entry.MetiqCommand, status, metiqCommand)
		}
		return
	}
	t.Fatalf("CLI entry %q not found", name)
}

func readCLIParityJSON(t *testing.T, path string, out any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}
