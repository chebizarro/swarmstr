package harness

import (
	"encoding/json"
	"os"
	"testing"

	"metiq/internal/session/checkpoint"
)

func TestJSONLRoundTripAppendReadLoad(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenSession(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	id1, _ := s.AppendMessage("user", "hello")
	if _, err := s.AppendMessage("assistant", "world"); err != nil {
		t.Fatal(err)
	}
	if len(s.Storage().ReadAll()) != 2 {
		t.Fatalf("in-memory entries missing")
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenSession(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	h, err := s2.History()
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 2 || h[0].ID != id1 || h[1].Message.Content != "world" {
		t.Fatalf("bad history: %#v", h)
	}
}

func TestPendingWritesFlushDurability(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenSession(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMessage("user", "not flushed"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(s.Storage().Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := lines(string(b)); got != 1 {
		t.Fatalf("pending write reached disk before flush: %d lines", got)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(s.Storage().Path())
	if got := lines(string(b)); got != 2 {
		t.Fatalf("flush did not persist entry: %d lines", got)
	}
}

func TestBranchForkNavigationHistory(t *testing.T) {
	s, _ := OpenSession(t.TempDir(), "s1")
	root, _ := s.AppendMessage("user", "root")
	left, _ := s.AppendMessage("assistant", "left")
	if _, err := s.Branch(root, "right"); err != nil {
		t.Fatal(err)
	}
	if err := s.SwitchBranch(root); err != nil {
		t.Fatal(err)
	}
	right, _ := s.AppendMessage("assistant", "right")
	branches := s.Branches()
	if len(branches) != 2 {
		t.Fatalf("branches=%#v", branches)
	}
	if err := s.SwitchBranch(left); err != nil {
		t.Fatal(err)
	}
	h, _ := s.History()
	if len(h) != 2 || h[1].ID != left {
		t.Fatalf("left history=%#v", h)
	}
	if err := s.SwitchBranch(right); err != nil {
		t.Fatal(err)
	}
	h, _ = s.History()
	if len(h) != 2 || h[1].ID != right || h[1].Message.Content != "right" {
		t.Fatalf("right history=%#v", h)
	}
}

func TestCompactionPersistenceFileOpsAndCheckpoint(t *testing.T) {
	s, _ := OpenSession(t.TempDir(), "s1")
	s.AppendMessage("user", "inspect")
	s.AppendToolCall("read_file", map[string]any{"path": "a.go"})
	s.AppendToolCall("apply_patch", map[string]any{"path": "b.go"})
	keep, _ := s.AppendMessage("assistant", "done")
	ce, err := s.Compact(CompactOptions{Summary: "summary", FirstKeptEntryID: keep, TokensBefore: 100, TokensAfter: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(ce.FileOps.ReadFiles) != 1 || ce.FileOps.ReadFiles[0] != "a.go" {
		t.Fatalf("read ops=%#v", ce.FileOps)
	}
	if len(ce.FileOps.EditedFiles) != 1 || ce.FileOps.EditedFiles[0] != "b.go" {
		t.Fatalf("edit ops=%#v", ce.FileOps)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	loaded, _ := OpenSession(t.TempDir(), "unused")
	_ = loaded
	s2, err := OpenSession(dirOf(s.Storage().Path()), "s1")
	if err != nil {
		t.Fatal(err)
	}
	last := s2.Storage().ReadAll()[len(s2.Storage().ReadAll())-1]
	if last.Type != EntryTypeCompaction || last.Summary != "summary" {
		t.Fatalf("bad compact entry=%#v", last)
	}
	store := checkpoint.NewStore()
	cp := s.PersistCompactionToStore(store, "key", ce)
	if len(cp.FileOps.ReadFiles) != 1 || cp.FileOps.ReadFiles[0] != "a.go" {
		t.Fatalf("checkpoint ops=%#v", cp.FileOps)
	}
}

func TestBranchSummarization(t *testing.T) {
	s, _ := OpenSession(t.TempDir(), "s1")
	s.AppendMessage("user", "please read file")
	s.AppendToolCall("read_file", map[string]any{"path": "main.go"})
	se, err := s.Summarize(DefaultSummarizer{}, "root")
	if err != nil {
		t.Fatal(err)
	}
	if se.Type != EntryTypeBranchSummary || se.Summary == "" || se.TokensBefore == 0 {
		t.Fatalf("bad summary=%#v", se)
	}
	if len(se.FileOps.ReadFiles) != 1 || se.FileOps.ReadFiles[0] != "main.go" {
		b, _ := json.Marshal(se)
		t.Fatalf("missing file ops %s", b)
	}
}

func lines(s string) int {
	n := 0
	for _, c := range s {
		if c == '\n' {
			n++
		}
	}
	return n
}
func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}
