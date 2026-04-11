package acp

import (
	"context"
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/store/state"
)

func mustTaskSecretKey(t *testing.T) [32]byte {
	t.Helper()
	sk, err := nostr.SecretKeyFromHex("1111111111111111111111111111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("SecretKeyFromHex: %v", err)
	}
	return [32]byte(sk)
}

func mustTaskPubKey(t *testing.T) string {
	t.Helper()
	pk := nostr.GetPublicKey(mustTaskSecretKey(t))
	return pk.Hex()
}

func TestBuildUnsignedTaskEventAndParseRoundTrip(t *testing.T) {
	sender := mustTaskPubKey(t)
	env := BuildTaskEnvelope("task-123", sender, TaskPayload{
		Instructions: "Implement the task event envelope",
		Task: &state.TaskSpec{
			GoalID:        "goal-1",
			ParentTaskID:  "task-root",
			PlanID:        "plan-1",
			AssignedAgent: "builder",
			Priority:      state.TaskPriorityHigh,
		},
		ContextMessages: []map[string]any{{"role": "user", "content": "prior context"}},
		MemoryScope:     state.AgentMemoryScopeProject,
		ToolProfile:     "coding",
		EnabledTools:    []string{"apply_edits", "read_file"},
		ParentContext:   &ParentContext{SessionID: "sess-1", AgentID: "director"},
		TimeoutMS:       30000,
		ReplyTo:         sender,
	})
	evtEnv, err := BuildUnsignedTaskEvent(sender, env)
	if err != nil {
		t.Fatalf("BuildUnsignedTaskEvent: %v", err)
	}
	if evtEnv.Kind != 38383 {
		t.Fatalf("unexpected event kind %d", evtEnv.Kind)
	}
	ev, err := evtEnv.ToNostrEvent()
	if err != nil {
		t.Fatalf("ToNostrEvent: %v", err)
	}
	parsed, err := ParseTaskEvent(ev)
	if err != nil {
		t.Fatalf("ParseTaskEvent: %v", err)
	}
	if parsed.Task.TaskID != "task-123" || parsed.Task.GoalID != "goal-1" {
		t.Fatalf("unexpected parsed task: %+v", parsed.Task)
	}
	if parsed.Task.MemoryScope != state.AgentMemoryScopeProject {
		t.Fatalf("expected project scope, got %q", parsed.Task.MemoryScope)
	}
	if parsed.Task.ToolProfile != "coding" {
		t.Fatalf("expected coding tool profile, got %q", parsed.Task.ToolProfile)
	}
	if parsed.ParentContext == nil || parsed.ParentContext.SessionID != "sess-1" {
		t.Fatalf("unexpected parent context: %#v", parsed.ParentContext)
	}
	if got := taskTagValue(ev.Tags, "d"); got != "task-123" {
		t.Fatalf("d tag = %q", got)
	}
	if got := taskTagValue(ev.Tags, "goal"); got != "goal-1" {
		t.Fatalf("goal tag = %q", got)
	}
}

func TestNewTaskIncludesTaskEventAndDecodeTaskPayload(t *testing.T) {
	sender := mustTaskPubKey(t)
	msg := NewTask("task-abc", sender, TaskPayload{
		Task: &state.TaskSpec{
			GoalID:        "goal-abc",
			Title:         "Canonical task",
			Instructions:  "Do the work",
			MemoryScope:   state.AgentMemoryScopeLocal,
			ToolProfile:   "coding",
			EnabledTools:  []string{"read_file"},
			AssignedAgent: "worker",
		},
		ParentContext: &ParentContext{SessionID: "sess-a", AgentID: "director"},
		TimeoutMS:     1500,
		ReplyTo:       sender,
	})
	if _, ok := msg.Payload["task_event"]; !ok {
		t.Fatal("expected task_event in payload")
	}
	payloadOnlyEvent := map[string]any{
		"task_event": msg.Payload["task_event"],
		"reply_to":   sender,
	}
	decoded, err := DecodeTaskPayload(payloadOnlyEvent)
	if err != nil {
		t.Fatalf("DecodeTaskPayload: %v", err)
	}
	if decoded.Task == nil {
		t.Fatal("expected decoded task")
	}
	if decoded.Task.TaskID != "task-abc" || decoded.Task.GoalID != "goal-abc" {
		t.Fatalf("unexpected decoded task: %+v", decoded.Task)
	}
	if decoded.Instructions != "Do the work" {
		t.Fatalf("expected instructions from task_event, got %q", decoded.Instructions)
	}
	if decoded.MemoryScope != state.AgentMemoryScopeLocal {
		t.Fatalf("expected memory scope from task_event, got %q", decoded.MemoryScope)
	}
}

func TestTaskEventEnvelopeSignAndVerify(t *testing.T) {
	sk := mustTaskSecretKey(t)
	sender := nostr.GetPublicKey(sk).Hex()
	evtEnv, err := BuildUnsignedTaskEvent(sender, BuildTaskEnvelope("task-signed", sender, TaskPayload{
		Instructions: "Produce a signed task event",
		Task: &state.TaskSpec{
			Title:        "Signed task",
			Instructions: "Produce a signed task event",
			Priority:     state.TaskPriorityHigh,
		},
	}))
	if err != nil {
		t.Fatalf("BuildUnsignedTaskEvent: %v", err)
	}
	ev, err := evtEnv.ToNostrEvent()
	if err != nil {
		t.Fatalf("ToNostrEvent: %v", err)
	}
	if err := ev.Sign(sk); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !ev.CheckID() {
		t.Fatal("expected signed task event id to verify")
	}
	if !ev.VerifySignature() {
		t.Fatal("expected signed task event signature to verify")
	}
	roundTrip := TaskEventEnvelopeFromNostr(*ev)
	if roundTrip.Sig == "" {
		t.Fatal("expected signature in task event envelope")
	}
	parsed, err := ParseTaskEvent(ev)
	if err != nil {
		t.Fatalf("ParseTaskEvent: %v", err)
	}
	if parsed.Task.TaskID != "task-signed" {
		t.Fatalf("unexpected task id %q", parsed.Task.TaskID)
	}
}

func TestTaskEventParseRejectsWrongKind(t *testing.T) {
	ev := &nostr.Event{Kind: nostr.Kind(1), Content: `{}`}
	_, err := ParseTaskEvent(ev)
	if err == nil || !strings.Contains(err.Error(), "unexpected task kind") {
		t.Fatalf("expected wrong-kind error, got %v", err)
	}
}

func TestTaskEventEnvelopeToNostrEventRejectsBadSig(t *testing.T) {
	_, err := (TaskEventEnvelope{Kind: 38383, Content: `{}`, Sig: "deadbeef"}).ToNostrEvent()
	if err == nil || !strings.Contains(err.Error(), "expected 64 bytes") {
		t.Fatalf("expected bad signature length error, got %v", err)
	}
}

func TestTaskEventEnvelopeFromSignedEventCarriesIdentity(t *testing.T) {
	sk := mustTaskSecretKey(t)
	ev := &nostr.Event{Kind: nostr.Kind(38383), CreatedAt: nostr.Now(), Content: `{"version":1,"task":{"task_id":"task-identity","title":"Identity","instructions":"Verify identity"}}`}
	if err := ev.Sign(sk); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	env := TaskEventEnvelopeFromNostr(*ev)
	if env.PubKey == "" || env.ID == "" || env.Sig == "" {
		t.Fatalf("expected identity fields, got %+v", env)
	}
	parsed, err := env.ToNostrEvent()
	if err != nil {
		t.Fatalf("ToNostrEvent: %v", err)
	}
	if !parsed.VerifySignature() {
		t.Fatal("expected reconstructed event signature to verify")
	}
	_ = context.Background()
}
