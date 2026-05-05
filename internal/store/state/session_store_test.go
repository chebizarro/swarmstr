package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionStore_RecordTurn(t *testing.T) {
	ss, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := ss.LinkTask("sess-1", "task-1", "run-1", "parent-task", "parent-run"); err != nil {
		t.Fatalf("link task: %v", err)
	}
	if err := ss.RecordTurn("sess-1", TurnTelemetry{
		TurnID:         "turn-1",
		StartedAtMS:    100,
		EndedAtMS:      250,
		DurationMS:     150,
		Outcome:        "completed",
		StopReason:     "model_text",
		FallbackUsed:   true,
		FallbackFrom:   "a",
		FallbackTo:     "b",
		FallbackReason: "429",
		InputTokens:    10,
		OutputTokens:   5,
	}); err != nil {
		t.Fatalf("record turn: %v", err)
	}
	got, ok := ss.Get("sess-1")
	if !ok {
		t.Fatal("session not found")
	}
	if got.LastTurn == nil {
		t.Fatal("expected last turn snapshot")
	}
	if got.LastTurn.TurnID != "turn-1" || got.LastTurn.DurationMS != 150 || got.LastTurn.Outcome != "completed" {
		t.Fatalf("unexpected last turn snapshot: %+v", got.LastTurn)
	}
	if !got.LastTurn.FallbackUsed || got.LastTurn.FallbackTo != "b" {
		t.Fatalf("expected fallback data on last turn: %+v", got.LastTurn)
	}
	if got.LastTurn.TaskID != "task-1" || got.LastTurn.RunID != "run-1" {
		t.Fatalf("expected task linkage on last turn: %+v", got.LastTurn)
	}
	if got.LastTurn.ParentTaskID != "parent-task" || got.LastTurn.ParentRunID != "parent-run" {
		t.Fatalf("expected parent linkage on last turn: %+v", got.LastTurn)
	}
}

func TestTurnTelemetry_JSONShape(t *testing.T) {
	raw, err := json.Marshal(TurnTelemetry{
		TurnID:      "turn-1",
		TaskID:      "task-1",
		RunID:       "run-1",
		StartedAtMS: 1,
		EndedAtMS:   2,
		Outcome:     "completed",
		StopReason:  "model_text",
		Result:      TaskResultRef{Kind: "transcript_entry", ID: "entry-1"},
	})
	if err != nil {
		t.Fatalf("marshal telemetry: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal telemetry: %v", err)
	}
	for _, field := range []string{"turn_id", "task_id", "run_id", "started_at_ms", "ended_at_ms", "outcome", "stop_reason", "result"} {
		if _, ok := decoded[field]; !ok {
			t.Fatalf("missing field %q in telemetry JSON: %s", field, string(raw))
		}
	}
}

func TestSessionStore_LinkTaskAndRecordTaskResult(t *testing.T) {
	ss, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := ss.LinkTask("sess-1", "task-1", "run-1", "parent-task", "parent-run"); err != nil {
		t.Fatalf("link task: %v", err)
	}
	if err := ss.AppendChildTask("sess-1", "child-1"); err != nil {
		t.Fatalf("append child task: %v", err)
	}
	if err := ss.AppendChildTask("sess-1", "child-1"); err != nil {
		t.Fatalf("append child task dedupe: %v", err)
	}
	result := TaskResultRef{Kind: "transcript_entry", ID: "entry-1"}
	if err := ss.RecordTaskResult("sess-1", "task-1", "run-1", result); err != nil {
		t.Fatalf("record task result: %v", err)
	}
	got, ok := ss.Get("sess-1")
	if !ok {
		t.Fatal("session not found")
	}
	if got.ActiveTaskID != "" || got.ActiveRunID != "" {
		t.Fatalf("expected active task cleared after result: %+v", got)
	}
	if got.LastCompletedTaskID != "task-1" || got.LastCompletedRunID != "run-1" {
		t.Fatalf("expected completed task linkage: %+v", got)
	}
	if got.LastTaskResult.Kind != "transcript_entry" || got.LastTaskResult.ID != "entry-1" {
		t.Fatalf("expected result ref to persist: %+v", got.LastTaskResult)
	}
	if len(got.ChildTaskIDs) != 1 || got.ChildTaskIDs[0] != "child-1" {
		t.Fatalf("expected child task dedupe: %+v", got.ChildTaskIDs)
	}
}

func TestSessionStore_RecordMemoryRecall_MergesAndCaps(t *testing.T) {
	ss, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	for i := 0; i < memoryRecallSampleCap+2; i++ {
		if err := ss.RecordMemoryRecall("sess-1", "turn-"+string(rune('a'+i)), &MemoryRecallSample{
			QueryHash: "q",
			FileSelected: []MemoryRecallFileHit{{
				RelativePath: "prefs.md",
				Reasons:      []string{"name"},
			}},
		}, map[string]string{"root::prefs.md": "signal-1"}); err != nil {
			t.Fatalf("record memory recall %d: %v", i, err)
		}
	}
	got, ok := ss.Get("sess-1")
	if !ok {
		t.Fatal("session not found")
	}
	if len(got.FileMemorySurfaced) != 1 || got.FileMemorySurfaced["root::prefs.md"] != "signal-1" {
		t.Fatalf("unexpected surfaced file-memory state: %+v", got.FileMemorySurfaced)
	}
	if len(got.RecentMemoryRecall) != memoryRecallSampleCap {
		t.Fatalf("expected capped recall samples, got %d", len(got.RecentMemoryRecall))
	}
	if got.RecentMemoryRecall[0].TurnID != "turn-c" || got.RecentMemoryRecall[len(got.RecentMemoryRecall)-1].TurnID != "turn-j" {
		t.Fatalf("expected append-capped recall ordering, got %+v", got.RecentMemoryRecall)
	}
	if got.RecentMemoryRecall[0].Strategy != "deterministic" {
		t.Fatalf("expected default recall strategy, got %+v", got.RecentMemoryRecall[0])
	}
}

func TestSessionStore_RecordToolLifecycle(t *testing.T) {
	ss, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := ss.LinkTask("sess-1", "task-1", "run-1", "", ""); err != nil {
		t.Fatalf("link task: %v", err)
	}
	for i := 0; i < toolLifecycleSampleCap+2; i++ {
		if err := ss.RecordToolLifecycle("sess-1", ToolLifecycleTelemetry{TS: int64(1000 + i), Type: "start", ToolName: "grep", ToolCallID: "call-1"}); err != nil {
			t.Fatalf("record tool lifecycle %d: %v", i, err)
		}
	}
	got, ok := ss.Get("sess-1")
	if !ok {
		t.Fatal("session not found")
	}
	if len(got.RecentToolLifecycle) != toolLifecycleSampleCap {
		t.Fatalf("expected capped tool lifecycle samples, got %d", len(got.RecentToolLifecycle))
	}
	first := got.RecentToolLifecycle[0]
	if first.TaskID != "task-1" || first.RunID != "run-1" {
		t.Fatalf("expected task/run linkage from active task, got %+v", first)
	}
}

func TestToolLifecycleTelemetry_JSONShape(t *testing.T) {
	raw, err := json.Marshal(ToolLifecycleTelemetry{
		TS:         100,
		Type:       "start",
		SessionID:  "sess-1",
		TurnID:     "turn-1",
		TaskID:     "task-1",
		RunID:      "run-1",
		StepID:     "step-1",
		ToolCallID: "call-1",
		ToolName:   "grep",
		Result:     "ok",
		Error:      "",
	})
	if err != nil {
		t.Fatalf("marshal tool lifecycle sample: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal tool lifecycle sample: %v", err)
	}
	for _, field := range []string{"ts_ms", "type", "session_id", "task_id", "run_id", "tool_call_id", "tool_name"} {
		if _, ok := decoded[field]; !ok {
			t.Fatalf("missing field %q in tool lifecycle JSON: %s", field, string(raw))
		}
	}
}

func TestMemoryRecallSample_JSONShape(t *testing.T) {
	raw, err := json.Marshal(MemoryRecallSample{
		TurnID:          "turn-1",
		Strategy:        "deterministic",
		QueryHash:       "abc123",
		QueryRuneCount:  12,
		QueryTokenCount: 3,
		Scope:           "project",
		IndexedSession:  []MemoryRecallIndexedHit{{MemoryID: "m1", Topic: "task"}},
		FileSelected:    []MemoryRecallFileHit{{RelativePath: "prefs.md", Reasons: []string{"name"}, Score: 4}},
		InjectedAny:     true,
	})
	if err != nil {
		t.Fatalf("marshal recall sample: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal recall sample: %v", err)
	}
	for _, field := range []string{"turn_id", "strategy", "query_hash", "indexed_session", "file_selected", "injected_any"} {
		if _, ok := decoded[field]; !ok {
			t.Fatalf("missing field %q in recall sample JSON: %s", field, string(raw))
		}
	}
}

func TestSessionStore_GetOrNewDoesNotLeaveUnjournaledEntryOnFailedPut(t *testing.T) {
	ss, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	entry := ss.GetOrNew("sess-ephemeral")
	entry.Label = "should-not-stick"

	sentinel := errors.New("forced journal failure")
	ss.journalFn = func(path string, data []byte) error { return sentinel }
	if err := ss.Put("sess-ephemeral", entry); !errors.Is(err, sentinel) {
		t.Fatalf("expected forced failure, got %v", err)
	}
	if _, ok := ss.Get("sess-ephemeral"); ok {
		t.Fatal("GetOrNew must not leave an unpersisted entry behind after failed Put")
	}
	list := ss.List()
	if len(list) != 0 {
		t.Fatalf("expected empty in-memory store after rollback, got %+v", list)
	}

	reloaded, err := NewSessionStore(ss.Path())
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	if _, ok := reloaded.Get("sess-ephemeral"); ok {
		t.Fatal("failed Put after GetOrNew should not survive reload")
	}
}

func TestSessionStore_PutInitializesIdentityAndTimestamps(t *testing.T) {
	ss, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := ss.Put("sess-identity", SessionEntry{Label: "created"}); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, ok := ss.Get("sess-identity")
	if !ok {
		t.Fatal("expected stored entry")
	}
	if got.SessionID != "sess-identity" {
		t.Fatalf("SessionID = %q, want key", got.SessionID)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("expected CreatedAt/UpdatedAt initialized, got %+v", got)
	}
}

func TestSessionStore_PutRollbackOnPersistFailure(t *testing.T) {
	ss, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	orig := SessionEntry{SessionID: "sess-1", Label: "before"}
	if err := ss.Put("sess-1", orig); err != nil {
		t.Fatalf("seed put: %v", err)
	}
	before, _ := ss.Get("sess-1")

	sentinel := errors.New("forced persist failure")
	ss.journalFn = func(path string, data []byte) error { return sentinel }
	if err := ss.Put("sess-1", SessionEntry{SessionID: "sess-1", Label: "after"}); !errors.Is(err, sentinel) {
		t.Fatalf("expected forced failure, got %v", err)
	}
	after, _ := ss.Get("sess-1")
	if after.Label != before.Label || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("expected in-memory rollback, before=%+v after=%+v", before, after)
	}

	disk, err := NewSessionStore(ss.Path())
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	diskEntry, _ := disk.Get("sess-1")
	if diskEntry.Label != before.Label {
		t.Fatalf("expected disk rollback, before=%+v disk=%+v", before, diskEntry)
	}
}

func TestSessionStore_JournalAppendFailureRestoresJournalBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	ss, err := NewSessionStore(path)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := ss.Put("sess-1", SessionEntry{SessionID: "sess-1", Label: "before"}); err != nil {
		t.Fatalf("seed put: %v", err)
	}
	beforeInfo, err := os.Stat(path + sessionStoreJournalSuffix)
	if err != nil {
		t.Fatalf("stat seed journal: %v", err)
	}

	sentinel := errors.New("forced sync failure after write")
	ss.journalFn = func(path string, data []byte) error {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		return sentinel
	}
	if err := ss.Put("sess-1", SessionEntry{SessionID: "sess-1", Label: "after"}); !errors.Is(err, sentinel) {
		t.Fatalf("expected forced failure, got %v", err)
	}
	afterInfo, err := os.Stat(path + sessionStoreJournalSuffix)
	if err != nil {
		t.Fatalf("stat restored journal: %v", err)
	}
	if afterInfo.Size() != beforeInfo.Size() {
		t.Fatalf("journal size = %d, want restored size %d", afterInfo.Size(), beforeInfo.Size())
	}

	reloaded, err := NewSessionStore(path)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	got, ok := reloaded.Get("sess-1")
	if !ok || got.Label != "before" {
		t.Fatalf("failed append should not replay after reload, ok=%v entry=%+v", ok, got)
	}
}

func TestSessionStore_AddTokensRollbackOnPersistFailure(t *testing.T) {
	ss, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := ss.Put("sess-1", SessionEntry{SessionID: "sess-1", InputTokens: 10, OutputTokens: 20, TotalTokens: 30}); err != nil {
		t.Fatalf("seed put: %v", err)
	}
	before, _ := ss.Get("sess-1")

	sentinel := errors.New("forced persist failure")
	ss.journalFn = func(path string, data []byte) error { return sentinel }
	if err := ss.AddTokens("sess-1", 5, 7, 2, 3); !errors.Is(err, sentinel) {
		t.Fatalf("expected forced failure, got %v", err)
	}
	after, _ := ss.Get("sess-1")
	if after.InputTokens != before.InputTokens || after.OutputTokens != before.OutputTokens || after.TotalTokens != before.TotalTokens || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("expected in-memory rollback, before=%+v after=%+v", before, after)
	}
}

func TestSessionStore_RecordTurnRollbackOnPersistFailure(t *testing.T) {
	ss, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := ss.LinkTask("sess-1", "task-1", "run-1", "parent-task", "parent-run"); err != nil {
		t.Fatalf("link task: %v", err)
	}
	before, _ := ss.Get("sess-1")

	sentinel := errors.New("forced persist failure")
	ss.journalFn = func(path string, data []byte) error { return sentinel }
	if err := ss.RecordTurn("sess-1", TurnTelemetry{TurnID: "turn-1", Outcome: "completed"}); !errors.Is(err, sentinel) {
		t.Fatalf("expected forced failure, got %v", err)
	}
	after, _ := ss.Get("sess-1")
	if after.LastTurn != nil || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("expected rollback of last turn and updated_at, before=%+v after=%+v", before, after)
	}
}

func TestSessionStore_LoadJournalIgnoresTornTrailingRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	ss, err := NewSessionStore(path)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := ss.Put("sess-1", SessionEntry{SessionID: "sess-1", Label: "valid"}); err != nil {
		t.Fatalf("put valid entry: %v", err)
	}
	f, err := os.OpenFile(path+sessionStoreJournalSuffix, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	if _, err := f.WriteString(`{"op":"put","key":"sess-2","entry":`); err != nil {
		_ = f.Close()
		t.Fatalf("append torn journal record: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close journal: %v", err)
	}

	reloaded, err := NewSessionStore(path)
	if err != nil {
		t.Fatalf("reload with torn trailing journal record: %v", err)
	}
	got, ok := reloaded.Get("sess-1")
	if !ok || got.Label != "valid" {
		t.Fatalf("expected valid journal prefix to replay, ok=%v entry=%+v", ok, got)
	}
	if _, ok := reloaded.Get("sess-2"); ok {
		t.Fatal("did not expect torn trailing record to be applied")
	}
}

func TestSessionStore_HotMutationsAppendJournalAndReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	ss, err := NewSessionStore(path)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := ss.Put("sess-1", SessionEntry{SessionID: "sess-1", Label: "base"}); err != nil {
		t.Fatalf("seed put: %v", err)
	}

	fullWrites := 0
	journalWrites := 0
	ss.persistFn = func(path string, data []byte) error {
		fullWrites++
		return defaultSessionStorePersist(path, data)
	}
	ss.journalFn = func(path string, data []byte) error {
		journalWrites++
		return defaultSessionStoreAppendJournal(path, data)
	}

	if err := ss.RecordTurn("sess-1", TurnTelemetry{TurnID: "turn-1", Outcome: "completed"}); err != nil {
		t.Fatalf("record turn: %v", err)
	}
	if fullWrites != 0 {
		t.Fatalf("expected hot mutation to avoid full sessions rewrite, got %d full writes", fullWrites)
	}
	if journalWrites != 1 {
		t.Fatalf("expected one journal write, got %d", journalWrites)
	}
	if _, err := os.Stat(path + sessionStoreJournalSuffix); err != nil {
		t.Fatalf("expected journal file: %v", err)
	}

	reloaded, err := NewSessionStore(path)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	got, ok := reloaded.Get("sess-1")
	if !ok || got.LastTurn == nil || got.LastTurn.TurnID != "turn-1" {
		t.Fatalf("expected replayed journal turn, ok=%v entry=%+v", ok, got)
	}

	if err := ss.Save(); err != nil {
		t.Fatalf("save compacted store: %v", err)
	}
	if _, err := os.Stat(path + sessionStoreJournalSuffix); !os.IsNotExist(err) {
		t.Fatalf("expected journal removed after full save, got %v", err)
	}
}

func TestSessionStore_NestedStateDoesNotAliasCallers(t *testing.T) {
	ss, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	fresh := true
	entry := SessionEntry{
		SessionID:          "sess-1",
		TaskState:          &TaskState{Decisions: []string{"original"}},
		TotalTokensFresh:   &fresh,
		FileMemorySurfaced: map[string]string{"prefs.md": "original"},
		ChildTaskIDs:       []string{"child-1"},
		RecentMemoryRecall: []MemoryRecallSample{{FileSelected: []MemoryRecallFileHit{{RelativePath: "prefs.md", Reasons: []string{"original"}}}}},
		CompactionCheckpoints: []CompactionCheckpointRef{{
			CheckpointID: "cp-1",
			PreCompaction: map[string]any{
				"phase":             "original",
				"nested":            map[string]any{"k": "v"},
				"typed_ints":        []int{1, 2},
				"typed_map_slice":   []map[string]any{{"phase": "original"}},
				"typed_map_strings": map[string][]string{"k": {"v"}},
			},
			PostCompaction: map[string]any{"items": []any{"a"}},
		}},
	}
	if err := ss.Put("sess-1", entry); err != nil {
		t.Fatalf("put entry: %v", err)
	}

	entry.TaskState.Decisions[0] = "caller-mutated"
	entry.FileMemorySurfaced["prefs.md"] = "caller-mutated"
	entry.ChildTaskIDs[0] = "caller-mutated"
	entry.RecentMemoryRecall[0].FileSelected[0].Reasons[0] = "caller-mutated"
	entry.CompactionCheckpoints[0].PreCompaction["phase"] = "caller-mutated"
	*entry.TotalTokensFresh = false

	got, ok := ss.Get("sess-1")
	if !ok {
		t.Fatal("expected stored entry")
	}
	assertSessionEntryNestedOriginal(t, got)

	got.TaskState.Decisions[0] = "get-mutated"
	got.FileMemorySurfaced["prefs.md"] = "get-mutated"
	got.ChildTaskIDs[0] = "get-mutated"
	got.RecentMemoryRecall[0].FileSelected[0].Reasons[0] = "get-mutated"
	got.CompactionCheckpoints[0].PreCompaction["phase"] = "get-mutated"
	got.CompactionCheckpoints[0].PreCompaction["nested"].(map[string]any)["k"] = "get-mutated"
	got.CompactionCheckpoints[0].PreCompaction["typed_ints"].([]int)[0] = 99
	got.CompactionCheckpoints[0].PreCompaction["typed_map_slice"].([]map[string]any)[0]["phase"] = "get-mutated"
	got.CompactionCheckpoints[0].PreCompaction["typed_map_strings"].(map[string][]string)["k"][0] = "get-mutated"
	got.CompactionCheckpoints[0].PostCompaction["items"].([]any)[0] = "get-mutated"
	*got.TotalTokensFresh = false

	again, _ := ss.Get("sess-1")
	assertSessionEntryNestedOriginal(t, again)

	listed := ss.List()
	listedEntry := listed["sess-1"]
	listedEntry.FileMemorySurfaced["prefs.md"] = "list-mutated"
	listedEntry.TaskState.Decisions[0] = "list-mutated"
	listedEntry.CompactionCheckpoints[0].PreCompaction["phase"] = "list-mutated"

	again, _ = ss.Get("sess-1")
	assertSessionEntryNestedOriginal(t, again)
}

func TestSessionStore_NestedRollbackOnPersistFailure(t *testing.T) {
	ss, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	fresh := true
	if err := ss.Put("sess-1", SessionEntry{
		SessionID:          "sess-1",
		TaskState:          &TaskState{Decisions: []string{"original"}},
		TotalTokensFresh:   &fresh,
		FileMemorySurfaced: map[string]string{"prefs.md": "original"},
		RecentMemoryRecall: []MemoryRecallSample{{FileSelected: []MemoryRecallFileHit{{RelativePath: "prefs.md", Reasons: []string{"original"}}}}},
		CompactionCheckpoints: []CompactionCheckpointRef{{
			CheckpointID: "cp-1",
			PreCompaction: map[string]any{
				"phase":             "original",
				"nested":            map[string]any{"k": "v"},
				"typed_ints":        []int{1, 2},
				"typed_map_slice":   []map[string]any{{"phase": "original"}},
				"typed_map_strings": map[string][]string{"k": {"v"}},
			},
			PostCompaction: map[string]any{"items": []any{"a"}},
		}},
	}); err != nil {
		t.Fatalf("seed put: %v", err)
	}

	sentinel := errors.New("forced persist failure")
	ss.persistFn = func(path string, data []byte) error { return sentinel }
	if err := ss.mutateAndPersist(func(entries map[string]SessionEntry) error {
		e := entries["sess-1"]
		e.TaskState.Decisions[0] = "mutated"
		e.FileMemorySurfaced["prefs.md"] = "mutated"
		e.RecentMemoryRecall[0].FileSelected[0].Reasons[0] = "mutated"
		e.CompactionCheckpoints[0].PreCompaction["phase"] = "mutated"
		e.CompactionCheckpoints[0].PreCompaction["nested"].(map[string]any)["k"] = "mutated"
		e.CompactionCheckpoints[0].PreCompaction["typed_ints"].([]int)[0] = 99
		e.CompactionCheckpoints[0].PreCompaction["typed_map_slice"].([]map[string]any)[0]["phase"] = "mutated"
		e.CompactionCheckpoints[0].PreCompaction["typed_map_strings"].(map[string][]string)["k"][0] = "mutated"
		e.CompactionCheckpoints[0].PostCompaction["items"].([]any)[0] = "mutated"
		*e.TotalTokensFresh = false
		entries["sess-1"] = e
		return nil
	}); !errors.Is(err, sentinel) {
		t.Fatalf("expected forced failure, got %v", err)
	}

	after, _ := ss.Get("sess-1")
	assertSessionEntryNestedOriginal(t, after)
}

func assertSessionEntryNestedOriginal(t *testing.T, got SessionEntry) {
	t.Helper()
	if got.TaskState == nil || got.TaskState.Decisions[0] != "original" {
		t.Fatalf("expected original task state, got %+v", got.TaskState)
	}
	if got.FileMemorySurfaced["prefs.md"] != "original" {
		t.Fatalf("expected original surfaced memory, got %+v", got.FileMemorySurfaced)
	}
	if len(got.ChildTaskIDs) > 0 && got.ChildTaskIDs[0] != "child-1" {
		t.Fatalf("expected original child task IDs, got %+v", got.ChildTaskIDs)
	}
	if got.RecentMemoryRecall[0].FileSelected[0].Reasons[0] != "original" {
		t.Fatalf("expected original recall reasons, got %+v", got.RecentMemoryRecall)
	}
	if got.CompactionCheckpoints[0].PreCompaction["phase"] != "original" {
		t.Fatalf("expected original pre-compaction map, got %+v", got.CompactionCheckpoints[0].PreCompaction)
	}
	if got.CompactionCheckpoints[0].PreCompaction["nested"].(map[string]any)["k"] != "v" {
		t.Fatalf("expected original nested pre-compaction map, got %+v", got.CompactionCheckpoints[0].PreCompaction)
	}
	if got.CompactionCheckpoints[0].PreCompaction["typed_ints"].([]int)[0] != 1 {
		t.Fatalf("expected original typed int slice, got %+v", got.CompactionCheckpoints[0].PreCompaction)
	}
	if got.CompactionCheckpoints[0].PreCompaction["typed_map_slice"].([]map[string]any)[0]["phase"] != "original" {
		t.Fatalf("expected original typed map slice, got %+v", got.CompactionCheckpoints[0].PreCompaction)
	}
	if got.CompactionCheckpoints[0].PreCompaction["typed_map_strings"].(map[string][]string)["k"][0] != "v" {
		t.Fatalf("expected original typed map strings, got %+v", got.CompactionCheckpoints[0].PreCompaction)
	}
	if got.CompactionCheckpoints[0].PostCompaction["items"].([]any)[0] != "a" {
		t.Fatalf("expected original post-compaction slice, got %+v", got.CompactionCheckpoints[0].PostCompaction)
	}
	if got.TotalTokensFresh == nil || !*got.TotalTokensFresh {
		t.Fatalf("expected original total_tokens_fresh pointer, got %+v", got.TotalTokensFresh)
	}
}
