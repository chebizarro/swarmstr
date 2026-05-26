package rules

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAndEvaluateBlock(t *testing.T) {
	src := `---
id: block-rm
name: Block rm root
action: block
event_types: [bash]
conditions:
  - field: command
    regex: 'rm -rf /'
---
body`
	r, err := ParseMarkdownRule(src)
	if err != nil {
		t.Fatal(err)
	}
	e, err := New([]Rule{r})
	if err != nil {
		t.Fatal(err)
	}
	d := e.Evaluate(Event{Type: EventBash, Command: "sudo rm -rf /"})
	if d.Action != ActionBlock || !d.Matched {
		t.Fatalf("unexpected decision: %+v", d)
	}
}

func TestNostrMatcherRequiresTags(t *testing.T) {
	e, err := New([]Rule{{ID: "nostr", Action: ActionWarn, EventTypes: []EventType{EventNostr}, Nostr: &NostrMatcher{Kinds: []int{1234}, RequiredTags: []string{"p"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := e.Evaluate(Event{Type: EventNostr, Nostr: NostrEventContext{Kind: 1234}}); got.Action != ActionAllow {
		t.Fatalf("got %+v", got)
	}
	got := e.Evaluate(Event{Type: EventNostr, Nostr: NostrEventContext{Kind: 1234, Tags: map[string][]string{"p": {"abc"}}}})
	if got.Action != ActionWarn {
		t.Fatalf("got %+v", got)
	}
}

func TestStoreReloadIfChanged(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".metiq"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, ".metiq", "local.local.md")
	if err := os.WriteFile(p, []byte("---\nid: x\naction: warn\nevent_types: [prompt]\nconditions:\n  - field: prompt\n    contains: hello\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Evaluate(Event{Type: EventPrompt, Prompt: "hello"}); got.Action != ActionWarn {
		t.Fatalf("got %+v", got)
	}
	if err := os.WriteFile(p, []byte("---\nid: x\naction: block\nevent_types: [prompt]\nconditions:\n  - field: prompt\n    contains: bye\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := s.ReloadIfChanged()
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed")
	}
	if got := s.Evaluate(Event{Type: EventPrompt, Prompt: "bye"}); got.Action != ActionBlock {
		t.Fatalf("got %+v", got)
	}
}

func TestInvalidRegexRejected(t *testing.T) {
	_, err := New([]Rule{{ID: "bad", Action: ActionWarn, Conditions: []Condition{{Field: "content", Regex: "["}}}})
	if err == nil {
		t.Fatal("expected invalid regex error")
	}
}

func TestBuildTeamPolicyEvent(t *testing.T) {
	ev, err := BuildTeamPolicyEvent(TeamPolicyBundle{Namespace: "eng", Rules: []Rule{{ID: "x", Action: ActionWarn}}})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != TeamPolicyKind || len(ev.Tags) == 0 || ev.Content == "" {
		t.Fatalf("bad event: %+v", ev)
	}
}
