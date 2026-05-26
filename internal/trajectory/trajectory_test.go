package trajectory

import (
	"archive/zip"
	"io"
	"strings"
	"testing"
)

func TestRecorderExportRedacts(t *testing.T) {
	root := t.TempDir()
	r, err := NewRecorder(root, "sess/1", Metadata{Version: "test", Provider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Record(Event{Type: EventToolResult, Payload: map[string]any{"text": "api_key=sk-abcdefghijklmnopqrstuvwxyz secret=hello"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	out := root + "/bundle.zip"
	manifest, err := ExportBundle(root, "sess/1", out)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.ToolCalls != 0 || manifest.Summary.Provider != "openai" {
		t.Fatalf("unexpected summary: %+v", manifest.Summary)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var found bool
	for _, f := range zr.File {
		if f.Name == "trajectory.jsonl" {
			rc, _ := f.Open()
			buf := new(strings.Builder)
			_, _ = io.Copy(buf, rc)
			_ = rc.Close()
			found = true
			if strings.Contains(buf.String(), "sk-abcdefghijklmnopqrstuvwxyz") {
				t.Fatal("secret not redacted")
			}
		}
	}
	if !found {
		t.Fatal("missing trajectory.jsonl")
	}
}

func TestBuildNostrAuditSummary(t *testing.T) {
	ev, err := BuildNostrAuditSummary(AuditSummary{SessionID: "s1", EventCounts: map[EventType]int{EventError: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != AuditSummaryKind || ev.Content == "" {
		t.Fatalf("bad event: %+v", ev)
	}
}
