package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	if snap.SchemaVersion != 2 {
		t.Fatalf("unexpected CLI parity schema version: %d", snap.SchemaVersion)
	}
	if snap.Summary.OpenClawCommandCount != len(snap.Groups) {
		t.Fatalf("CLI command summary mismatch: summary=%d groups=%d", snap.Summary.OpenClawCommandCount, len(snap.Groups))
	}

	validStatuses := map[string]struct{}{"implemented": {}, "deviation": {}, "missing": {}}
	registry := newCommandRegistry("")
	seen := map[string]struct{}{}
	statusCounts := map[string]int{}
	for _, entry := range snap.Groups {
		if _, ok := seen[entry.Name]; ok {
			t.Fatalf("duplicate CLI parity entry %q", entry.Name)
		}
		seen[entry.Name] = struct{}{}
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
	if len(statusCounts) != len(snap.Summary.StatusCounts) {
		t.Fatalf("CLI status summary key drift: got=%v summary=%v", statusCounts, snap.Summary.StatusCounts)
	}
	for status, count := range statusCounts {
		if snap.Summary.StatusCounts[status] != count {
			t.Fatalf("CLI status summary drift: got=%v summary=%v", statusCounts, snap.Summary.StatusCounts)
		}
	}
	if snap.Summary.OpenClawCommandCount != 70 {
		t.Fatalf("expected current 70-command OpenClaw snapshot, got %d", snap.Summary.OpenClawCommandCount)
	}
	if len(seen) != 70 {
		t.Fatalf("CLI status summary drift: got=%v summary=%v", statusCounts, snap.Summary.StatusCounts)
	}

	assertCLIParityEntry(t, snap.Groups, "gateway", "implemented", "gw")
	assertCLIParityEntry(t, snap.Groups, "exec-approvals", "implemented", "approvals")
	assertCLIParityEntry(t, snap.Groups, "automations", "implemented", "cron")
	assertCLIParityEntry(t, snap.Groups, "triage", "implemented", "diagnostics")
	assertCLIParityEntry(t, snap.Groups, "node", "deviation", "")
	for _, name := range []string{"claws", "audit", "promos", "exec-approvals", "users", "worker", "fleet", "worktrees", "attach", "database", "telemetry", "connect", "resume"} {
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
