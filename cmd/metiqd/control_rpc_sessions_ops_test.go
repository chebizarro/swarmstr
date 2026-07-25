package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func sessionsOpsCall(t *testing.T, h controlRPCHandler, method, params string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	t.Helper()
	return h.handleSessionsOpsRPC(context.Background(), nostruntime.ControlRPCInbound{
		Method: method,
		Params: json.RawMessage(params),
	}, method, cfg)
}

func TestSessionsPluginPatchNamespacesUnderPlugin(t *testing.T) {
	h, docsRepo, _ := newTestControlRPCHandler(t)
	ctx := context.Background()
	if _, err := docsRepo.PutSession(ctx, "s1", state.SessionDoc{Version: 1, SessionID: "s1"}); err != nil {
		t.Fatalf("put session: %v", err)
	}
	cfg := pluginTestConfig() // provides plugin "weather"

	res, handled, err := sessionsOpsCall(t, h, methods.MethodSessionsPluginPatch,
		`{"session_id":"s1","plugin_id":"weather","patch":{"units":"metric"}}`, cfg)
	if !handled || err != nil {
		t.Fatalf("pluginPatch handled=%v err=%v", handled, err)
	}
	session := res.Result.(map[string]any)["session"].(state.SessionDoc)
	plugins, ok := session.Meta["plugins"].(map[string]any)
	if !ok {
		t.Fatalf("expected meta.plugins subtree: %+v", session.Meta)
	}
	weather, ok := plugins["weather"].(map[string]any)
	if !ok || weather["units"] != "metric" {
		t.Fatalf("expected namespaced plugin patch: %+v", plugins)
	}

	// A second merge preserves prior keys.
	res, _, err = sessionsOpsCall(t, h, methods.MethodSessionsPluginPatch,
		`{"session_id":"s1","plugin_id":"weather","patch":{"lang":"en"}}`, cfg)
	if err != nil {
		t.Fatalf("pluginPatch merge: %v", err)
	}
	session = res.Result.(map[string]any)["session"].(state.SessionDoc)
	weather = session.Meta["plugins"].(map[string]any)["weather"].(map[string]any)
	if weather["units"] != "metric" || weather["lang"] != "en" {
		t.Fatalf("expected merged plugin subtree: %+v", weather)
	}
}

func TestSessionsPluginPatchRejectsUnknownPlugin(t *testing.T) {
	h, docsRepo, _ := newTestControlRPCHandler(t)
	ctx := context.Background()
	if _, err := docsRepo.PutSession(ctx, "s1", state.SessionDoc{Version: 1, SessionID: "s1"}); err != nil {
		t.Fatalf("put session: %v", err)
	}
	_, _, err := sessionsOpsCall(t, h, methods.MethodSessionsPluginPatch,
		`{"session_id":"s1","plugin_id":"ghost","patch":{}}`, pluginTestConfig())
	if err == nil || !strings.Contains(err.Error(), "unknown plugin") {
		t.Fatalf("expected unknown-plugin error, got %v", err)
	}
}

func TestSessionsCleanupCollectsTerminalSessions(t *testing.T) {
	h, docsRepo, transcriptRepo := newTestControlRPCHandler(t)
	ctx := context.Background()
	if _, err := docsRepo.PutSession(ctx, "dead", state.SessionDoc{
		Version:   1,
		SessionID: "dead",
		Meta:      map[string]any{"deleted": true, "deleted_at": int64(1)},
	}); err != nil {
		t.Fatalf("put dead session: %v", err)
	}
	if _, err := transcriptRepo.PutEntry(ctx, state.TranscriptEntryDoc{
		Version: 1, SessionID: "dead", EntryID: "e1", Role: "user", Text: "x", Unix: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("put entry: %v", err)
	}
	if _, err := docsRepo.PutSession(ctx, "live", state.SessionDoc{Version: 1, SessionID: "live"}); err != nil {
		t.Fatalf("put live session: %v", err)
	}

	// Dry run reports but does not delete.
	res, _, err := sessionsOpsCall(t, h, methods.MethodSessionsCleanup, `{"dry_run":true}`, state.ConfigDoc{})
	if err != nil {
		t.Fatalf("cleanup dry run: %v", err)
	}
	payload := res.Result.(map[string]any)
	if payload["dry_run"] != true || payload["cleaned_count"].(int) != 1 {
		t.Fatalf("expected 1 dry-run candidate: %+v", payload)
	}
	if entries, _ := transcriptRepo.ListSessionAll(ctx, "dead"); len(entries) != 1 {
		t.Fatalf("dry run must not delete transcript: %d", len(entries))
	}

	// Real run collects the terminal session and removes residual transcript.
	res, _, err = sessionsOpsCall(t, h, methods.MethodSessionsCleanup, `{}`, state.ConfigDoc{})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	payload = res.Result.(map[string]any)
	cleaned := payload["cleaned"].([]string)
	if len(cleaned) != 1 || cleaned[0] != "dead" {
		t.Fatalf("expected only dead collected: %+v", cleaned)
	}
	if entries, _ := transcriptRepo.ListSessionAll(ctx, "dead"); len(entries) != 0 {
		t.Fatalf("expected residual transcript removed, got %d", len(entries))
	}
}

func TestSessionsDiffComparesCheckpoints(t *testing.T) {
	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := sessionStore.Put("s1", state.SessionEntry{
		SessionID: "s1",
		CompactionCheckpoints: []state.CompactionCheckpointRef{
			{CheckpointID: "cp1", SessionKey: "s1", SessionID: "s1", CreatedAt: 10, Reason: "manual", TokensBefore: 100, TokensAfter: 80},
			{CheckpointID: "cp2", SessionKey: "s1", SessionID: "s1", CreatedAt: 20, Reason: "auto-threshold", TokensBefore: 200, TokensAfter: 120, Summary: "s"},
		},
	}); err != nil {
		t.Fatalf("seed checkpoints: %v", err)
	}
	h := newControlRPCHandler(controlRPCDeps{sessionStore: sessionStore})

	res, handled, err := sessionsOpsCall(t, h, methods.MethodSessionsDiff, `{"session_id":"s1","from":"cp1","to":"cp2"}`, state.ConfigDoc{})
	if !handled || err != nil {
		t.Fatalf("diff handled=%v err=%v", handled, err)
	}
	diff := res.Result.(map[string]any)["diff"].(map[string]any)
	changed := diff["changed"].(map[string]any)
	if _, ok := changed["reason"]; !ok {
		t.Fatalf("expected reason change: %+v", changed)
	}
	if _, ok := changed["tokensAfter"]; !ok {
		t.Fatalf("expected tokensAfter change: %+v", changed)
	}
	if diff["tokens_after_delta"] != int64(40) {
		t.Fatalf("expected tokensAfter delta 40, got %v", diff["tokens_after_delta"])
	}
	if diff["created_at_delta"] != int64(10) {
		t.Fatalf("expected createdAt delta 10, got %v", diff["created_at_delta"])
	}
}

func TestSessionsDiffMissingCheckpoint(t *testing.T) {
	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := sessionStore.Put("s1", state.SessionEntry{SessionID: "s1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := newControlRPCHandler(controlRPCDeps{sessionStore: sessionStore})
	_, _, err = sessionsOpsCall(t, h, methods.MethodSessionsDiff, `{"session_id":"s1","from":"nope","to":"nada"}`, state.ConfigDoc{})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected checkpoint-not-found error, got %v", err)
	}
}

func TestSessionsCleanupRespectsGraceAndIdle(t *testing.T) {
	h, docsRepo, _ := newTestControlRPCHandler(t)
	ctx := context.Background()
	now := time.Now()
	// Recently tombstoned: within a 7-day grace -> must NOT be collected.
	if _, err := docsRepo.PutSession(ctx, "recent-dead", state.SessionDoc{
		Version: 1, SessionID: "recent-dead",
		Meta: map[string]any{"deleted": true, "deleted_at": now.Add(-1 * time.Hour).Unix()},
	}); err != nil {
		t.Fatalf("put recent-dead: %v", err)
	}
	// Old tombstone -> collected under the grace.
	if _, err := docsRepo.PutSession(ctx, "old-dead", state.SessionDoc{
		Version: 1, SessionID: "old-dead",
		Meta: map[string]any{"deleted": true, "deleted_at": now.Add(-30 * 24 * time.Hour).Unix()},
	}); err != nil {
		t.Fatalf("put old-dead: %v", err)
	}
	// Long-idle live session -> collected only via include_idle.
	if _, err := docsRepo.PutSession(ctx, "idle", state.SessionDoc{
		Version: 1, SessionID: "idle", LastInboundAt: now.Add(-30 * 24 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("put idle: %v", err)
	}

	res, _, err := sessionsOpsCall(t, h, methods.MethodSessionsCleanup,
		`{"older_than_days":7,"include_idle":true,"idle_days":7,"dry_run":true}`, state.ConfigDoc{})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	set := map[string]bool{}
	for _, id := range res.Result.(map[string]any)["cleaned"].([]string) {
		set[id] = true
	}
	if set["recent-dead"] {
		t.Fatalf("recent tombstone within grace must not be collected: %+v", set)
	}
	if !set["old-dead"] {
		t.Fatalf("old tombstone should be collected: %+v", set)
	}
	if !set["idle"] {
		t.Fatalf("long-idle session should be collected with include_idle: %+v", set)
	}
}
