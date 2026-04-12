package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"metiq/internal/store/state"
)

func newEpisodicTestIndex(t *testing.T) *Index {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test-index.json")
	idx, err := OpenIndex(path)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	return idx
}

func TestEpisodicMemory_AddAndRetrieve(t *testing.T) {
	idx := newEpisodicTestIndex(t)
	doc := state.MemoryDoc{
		MemoryID:    "ep-1",
		Type:        state.MemoryTypeEpisodic,
		Text:        "Task completed successfully with 95% accuracy",
		GoalID:      "goal-abc",
		TaskID:      "task-123",
		RunID:       "run-456",
		EpisodeKind: state.EpisodeKindOutcome,
		Unix:        1000,
	}
	idx.Add(doc)

	results := idx.Search("completed accuracy", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Type != state.MemoryTypeEpisodic {
		t.Errorf("type = %q, want %q", r.Type, state.MemoryTypeEpisodic)
	}
	if r.GoalID != "goal-abc" {
		t.Errorf("goal_id = %q, want %q", r.GoalID, "goal-abc")
	}
	if r.TaskID != "task-123" {
		t.Errorf("task_id = %q, want %q", r.TaskID, "task-123")
	}
	if r.RunID != "run-456" {
		t.Errorf("run_id = %q, want %q", r.RunID, "run-456")
	}
	if r.EpisodeKind != state.EpisodeKindOutcome {
		t.Errorf("episode_kind = %q, want %q", r.EpisodeKind, state.EpisodeKindOutcome)
	}
}

func TestEpisodicMemory_ListByType(t *testing.T) {
	idx := newEpisodicTestIndex(t)
	idx.Add(state.MemoryDoc{MemoryID: "fact-1", Type: state.MemoryTypeFact, Text: "The sky is blue", Unix: 100})
	idx.Add(state.MemoryDoc{MemoryID: "ep-1", Type: state.MemoryTypeEpisodic, Text: "Agent chose approach A", GoalID: "g1", TaskID: "t1", EpisodeKind: state.EpisodeKindDecision, Unix: 200})
	idx.Add(state.MemoryDoc{MemoryID: "ep-2", Type: state.MemoryTypeEpisodic, Text: "Run failed with timeout", GoalID: "g1", TaskID: "t2", EpisodeKind: state.EpisodeKindError, Unix: 300})
	idx.Add(state.MemoryDoc{MemoryID: "pref-1", Type: state.MemoryTypePreference, Text: "User prefers dark mode", Unix: 400})

	episodic := idx.ListByType(state.MemoryTypeEpisodic, 10)
	if len(episodic) != 2 {
		t.Fatalf("expected 2 episodic, got %d", len(episodic))
	}
	// Should be newest first.
	if episodic[0].MemoryID != "ep-2" {
		t.Errorf("first = %q, want ep-2 (newest)", episodic[0].MemoryID)
	}
	if episodic[1].MemoryID != "ep-1" {
		t.Errorf("second = %q, want ep-1", episodic[1].MemoryID)
	}

	facts := idx.ListByType(state.MemoryTypeFact, 10)
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	if facts[0].MemoryID != "fact-1" {
		t.Errorf("fact = %q, want fact-1", facts[0].MemoryID)
	}
}

func TestEpisodicMemory_ListByTaskID(t *testing.T) {
	idx := newEpisodicTestIndex(t)
	idx.Add(state.MemoryDoc{MemoryID: "ep-1", Type: state.MemoryTypeEpisodic, Text: "First run outcome", TaskID: "task-A", RunID: "run-1", Unix: 100})
	idx.Add(state.MemoryDoc{MemoryID: "ep-2", Type: state.MemoryTypeEpisodic, Text: "Second run outcome", TaskID: "task-A", RunID: "run-2", Unix: 200})
	idx.Add(state.MemoryDoc{MemoryID: "ep-3", Type: state.MemoryTypeEpisodic, Text: "Different task outcome", TaskID: "task-B", RunID: "run-3", Unix: 300})
	idx.Add(state.MemoryDoc{MemoryID: "fact-1", Type: state.MemoryTypeFact, Text: "Unrelated fact", Unix: 400})

	taskA := idx.ListByTaskID("task-A", 10)
	if len(taskA) != 2 {
		t.Fatalf("expected 2 for task-A, got %d", len(taskA))
	}
	if taskA[0].MemoryID != "ep-2" {
		t.Errorf("first = %q, want ep-2 (newest)", taskA[0].MemoryID)
	}

	taskB := idx.ListByTaskID("task-B", 10)
	if len(taskB) != 1 {
		t.Fatalf("expected 1 for task-B, got %d", len(taskB))
	}

	none := idx.ListByTaskID("task-Z", 10)
	if len(none) != 0 {
		t.Fatalf("expected 0 for task-Z, got %d", len(none))
	}
}

func TestEpisodicMemory_ListByType_Limit(t *testing.T) {
	idx := newEpisodicTestIndex(t)
	for i := 0; i < 10; i++ {
		idx.Add(state.MemoryDoc{
			MemoryID:    GenerateMemoryID(),
			Type:        state.MemoryTypeEpisodic,
			Text:        "episodic entry",
			TaskID:      "task-X",
			EpisodeKind: state.EpisodeKindInsight,
			Unix:        int64(i),
		})
	}
	results := idx.ListByType(state.MemoryTypeEpisodic, 3)
	if len(results) != 3 {
		t.Fatalf("expected 3, got %d", len(results))
	}
	// Newest first.
	if results[0].Unix < results[1].Unix {
		t.Error("expected newest-first ordering")
	}
}

func TestEpisodicMemory_DistinguishFromSemantic(t *testing.T) {
	idx := newEpisodicTestIndex(t)
	idx.Add(state.MemoryDoc{MemoryID: "sem-1", Type: state.MemoryTypeFact, Text: "Go uses goroutines for concurrency", Unix: 100})
	idx.Add(state.MemoryDoc{MemoryID: "ep-1", Type: state.MemoryTypeEpisodic, Text: "Used goroutines to fix concurrency bug in worker pool", TaskID: "task-1", EpisodeKind: state.EpisodeKindOutcome, Unix: 200})

	// Search returns both.
	all := idx.Search("goroutines concurrency", 10)
	if len(all) != 2 {
		t.Fatalf("expected 2, got %d", len(all))
	}

	// ListByType distinguishes them.
	episodic := idx.ListByType(state.MemoryTypeEpisodic, 10)
	if len(episodic) != 1 || episodic[0].MemoryID != "ep-1" {
		t.Error("ListByType should return only the episodic entry")
	}
	facts := idx.ListByType(state.MemoryTypeFact, 10)
	if len(facts) != 1 || facts[0].MemoryID != "sem-1" {
		t.Error("ListByType should return only the fact entry")
	}
}

func TestEpisodicMemory_PersistAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-index.json")

	idx1, err := OpenIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	idx1.Add(state.MemoryDoc{
		MemoryID:    "ep-1",
		Type:        state.MemoryTypeEpisodic,
		Text:        "Important episodic memory",
		GoalID:      "goal-1",
		TaskID:      "task-1",
		RunID:       "run-1",
		EpisodeKind: state.EpisodeKindInsight,
		Unix:        1000,
	})
	if err := idx1.Save(); err != nil {
		t.Fatal(err)
	}

	idx2, err := OpenIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	results := idx2.ListByType(state.MemoryTypeEpisodic, 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 after reload, got %d", len(results))
	}
	r := results[0]
	if r.GoalID != "goal-1" || r.TaskID != "task-1" || r.RunID != "run-1" {
		t.Errorf("correlation fields lost after reload: goal=%q task=%q run=%q", r.GoalID, r.TaskID, r.RunID)
	}
	if r.EpisodeKind != state.EpisodeKindInsight {
		t.Errorf("episode_kind = %q, want %q", r.EpisodeKind, state.EpisodeKindInsight)
	}
}

func TestEpisodicMemory_JSONShape(t *testing.T) {
	doc := state.MemoryDoc{
		Version:     1,
		MemoryID:    "ep-json-1",
		Type:        state.MemoryTypeEpisodic,
		Text:        "Agent decided to use approach B",
		GoalID:      "goal-x",
		TaskID:      "task-y",
		RunID:       "run-z",
		EpisodeKind: state.EpisodeKindDecision,
		Unix:        5000,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"type", "goal_id", "task_id", "run_id", "episode_kind"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing key %q in JSON", key)
		}
	}
	if m["type"] != "episodic" {
		t.Errorf("type = %v, want episodic", m["type"])
	}
	if m["episode_kind"] != "decision" {
		t.Errorf("episode_kind = %v, want decision", m["episode_kind"])
	}

	// Round-trip.
	var decoded state.MemoryDoc
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.GoalID != doc.GoalID || decoded.TaskID != doc.TaskID || decoded.RunID != doc.RunID {
		t.Error("JSON round-trip lost correlation fields")
	}
}

func TestEpisodicMemory_IndexedMemoryJSONShape(t *testing.T) {
	im := IndexedMemory{
		MemoryID:    "im-1",
		Type:        state.MemoryTypeEpisodic,
		Text:        "test",
		GoalID:      "g1",
		TaskID:      "t1",
		RunID:       "r1",
		EpisodeKind: state.EpisodeKindOutcome,
		Unix:        1000,
	}
	raw, err := json.Marshal(im)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"type", "goal_id", "task_id", "run_id", "episode_kind"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing key %q in IndexedMemory JSON", key)
		}
	}
}

func TestEpisodicMemory_OmitEmptyCorrelation(t *testing.T) {
	// Non-episodic doc should omit correlation fields.
	doc := state.MemoryDoc{
		Version:  1,
		MemoryID: "fact-1",
		Type:     state.MemoryTypeFact,
		Text:     "plain fact",
		Unix:     100,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"goal_id", "task_id", "run_id", "episode_kind"} {
		if _, ok := m[key]; ok {
			t.Errorf("key %q should be omitted for non-episodic doc", key)
		}
	}
}

func TestEpisodicMemory_StoreInterface(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-index.json")
	idx, err := OpenIndex(path)
	if err != nil {
		t.Fatal(err)
	}

	var store Store = idx
	store.Add(state.MemoryDoc{
		MemoryID:    "ep-iface-1",
		Type:        state.MemoryTypeEpisodic,
		Text:        "interface test episodic memory",
		TaskID:      "task-iface",
		EpisodeKind: state.EpisodeKindError,
		Unix:        500,
	})
	store.Add(state.MemoryDoc{
		MemoryID: "fact-iface-1",
		Type:     state.MemoryTypeFact,
		Text:     "interface test fact memory",
		Unix:     600,
	})

	byType := store.ListByType(state.MemoryTypeEpisodic, 10)
	if len(byType) != 1 || byType[0].MemoryID != "ep-iface-1" {
		t.Errorf("ListByType via Store interface: got %d results", len(byType))
	}
	byTask := store.ListByTaskID("task-iface", 10)
	if len(byTask) != 1 || byTask[0].MemoryID != "ep-iface-1" {
		t.Errorf("ListByTaskID via Store interface: got %d results", len(byTask))
	}
}

func TestEpisodicMemory_AllEpisodeKinds(t *testing.T) {
	idx := newEpisodicTestIndex(t)
	kinds := []string{
		state.EpisodeKindOutcome,
		state.EpisodeKindDecision,
		state.EpisodeKindError,
		state.EpisodeKindInsight,
	}
	for i, kind := range kinds {
		idx.Add(state.MemoryDoc{
			MemoryID:    GenerateMemoryID(),
			Type:        state.MemoryTypeEpisodic,
			Text:        "episodic " + kind,
			TaskID:      "task-kinds",
			EpisodeKind: kind,
			Unix:        int64(i),
		})
	}
	results := idx.ListByType(state.MemoryTypeEpisodic, 10)
	if len(results) != 4 {
		t.Fatalf("expected 4, got %d", len(results))
	}
	seenKinds := map[string]bool{}
	for _, r := range results {
		seenKinds[r.EpisodeKind] = true
	}
	for _, kind := range kinds {
		if !seenKinds[kind] {
			t.Errorf("missing episode kind %q", kind)
		}
	}
}

func TestEpisodicMemory_DiskRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "disk-rt.json")

	idx, err := OpenIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	idx.Add(state.MemoryDoc{
		MemoryID:    "ep-disk-1",
		Type:        state.MemoryTypeEpisodic,
		Text:        "disk round trip test",
		GoalID:      "goal-disk",
		TaskID:      "task-disk",
		RunID:       "run-disk",
		EpisodeKind: state.EpisodeKindOutcome,
		Unix:        2000,
	})
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}

	// Verify the disk format contains the new fields.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var disk struct {
		Docs []json.RawMessage `json:"docs"`
	}
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	if len(disk.Docs) != 1 {
		t.Fatalf("expected 1 doc on disk, got %d", len(disk.Docs))
	}
	var m map[string]any
	if err := json.Unmarshal(disk.Docs[0], &m); err != nil {
		t.Fatal(err)
	}
	if m["goal_id"] != "goal-disk" {
		t.Errorf("disk goal_id = %v, want goal-disk", m["goal_id"])
	}
	if m["task_id"] != "task-disk" {
		t.Errorf("disk task_id = %v, want task-disk", m["task_id"])
	}
	if m["episode_kind"] != "outcome" {
		t.Errorf("disk episode_kind = %v, want outcome", m["episode_kind"])
	}
}
