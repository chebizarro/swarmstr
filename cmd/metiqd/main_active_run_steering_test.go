package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"metiq/internal/agent"
	"metiq/internal/autoreply"
	ctxengine "metiq/internal/context"
	"metiq/internal/store/state"
)

func TestEnqueueActiveRunSteeringDrainsForModel(t *testing.T) {
	mailboxes := autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)
	settings := queueRuntimeSettings{Mode: "steer", Cap: 10, Drop: autoreply.QueueDropSummarize}

	accepted := enqueueActiveRunSteering(mailboxes, settings, activeRunSteeringInput{
		SessionID: "sess-1",
		Text:      "please also consider the new constraint",
		EventID:   "evt-secret",
		SenderID:  "alice",
		Source:    "dm",
		CreatedAt: 10,
	})
	if !accepted {
		t.Fatal("expected steering input to enqueue")
	}

	drain := makeActiveRunSteeringDrain(mailboxes, "sess-1", nil)
	got := drain(context.Background())
	if len(got) != 1 {
		t.Fatalf("expected one drained input, got %d", len(got))
	}
	if !strings.Contains(got[0].Content, "Additional user input received while you were working") {
		t.Fatalf("expected DM provenance header, got %q", got[0].Content)
	}
	if !strings.Contains(got[0].Content, "new constraint") {
		t.Fatalf("expected original text in injected input, got %q", got[0].Content)
	}
	if strings.Contains(got[0].Content, "evt-secret") {
		t.Fatalf("model-facing steering content exposed event id: %q", got[0].Content)
	}
	if again := drain(context.Background()); len(again) != 0 {
		t.Fatalf("expected mailbox to be empty after drain, got %d", len(again))
	}
}

func TestActiveRunSteeringChannelHeaderAndResidualMetadata(t *testing.T) {
	mailboxes := autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)
	settings := queueRuntimeSettings{Mode: "steer", Cap: 10, Drop: autoreply.QueueDropSummarize}

	enqueueActiveRunSteering(mailboxes, settings, activeRunSteeringInput{
		SessionID:    "sess-chan",
		Text:         "normal update",
		EventID:      "evt-normal",
		SenderID:     "bob",
		ChannelID:    "chan-1",
		ThreadID:     "thread-1",
		Source:       "channel",
		ToolProfile:  "safe",
		EnabledTools: []string{"read_file"},
		CreatedAt:    20,
		Priority:     autoreply.SteeringPriorityNormal,
	})
	enqueueActiveRunSteering(mailboxes, settings, activeRunSteeringInput{
		SessionID: "sess-chan",
		Text:      "urgent update",
		EventID:   "evt-urgent",
		SenderID:  "carol",
		ChannelID: "chan-1",
		Source:    "channel",
		CreatedAt: 30,
		Priority:  autoreply.SteeringPriorityUrgent,
	})

	drain := makeActiveRunSteeringDrain(mailboxes, "sess-chan", nil)
	modelInputs := drain(context.Background())
	if len(modelInputs) != 2 {
		t.Fatalf("expected two model inputs, got %d", len(modelInputs))
	}
	if !strings.Contains(modelInputs[0].Content, "from carol") || !strings.Contains(modelInputs[0].Content, "urgent update") {
		t.Fatalf("expected urgent channel steering first with sender provenance, got %q", modelInputs[0].Content)
	}

	// Re-enqueue and exercise residual fallback conversion, which must keep raw
	// text/provenance for the follow-up turn rather than model-facing headers.
	enqueueActiveRunSteering(mailboxes, settings, activeRunSteeringInput{
		SessionID:    "sess-chan",
		Text:         "normal update",
		EventID:      "evt-normal-2",
		SenderID:     "bob",
		ChannelID:    "chan-1",
		ThreadID:     "thread-1",
		Source:       "channel",
		ToolProfile:  "safe",
		EnabledTools: []string{"read_file"},
		CreatedAt:    20,
		Priority:     autoreply.SteeringPriorityNormal,
	})
	pending := drainSteeringAsPending(mailboxes, "sess-chan")
	if len(pending) != 1 {
		t.Fatalf("expected one residual pending turn, got %d", len(pending))
	}
	pt := pending[0]
	if pt.Text != "normal update" || pt.SenderID != "bob" || pt.EventID != "evt-normal-2" {
		t.Fatalf("residual pending turn did not preserve raw provenance: %+v", pt)
	}
	if pt.ToolProfile != "safe" || len(pt.EnabledTools) != 1 || pt.EnabledTools[0] != "read_file" {
		t.Fatalf("residual pending turn did not preserve tool constraints: %+v", pt)
	}
}

func TestClearTransientSessionSteeringDeletesMailbox(t *testing.T) {
	mailboxes := autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)
	settings := queueRuntimeSettings{Mode: "steer", Cap: 10, Drop: autoreply.QueueDropSummarize}
	enqueueActiveRunSteering(mailboxes, settings, activeRunSteeringInput{SessionID: "sess-clear", Text: "stale", EventID: "evt-stale", Source: "dm"})
	if got := steeringMailboxLen(mailboxes, "sess-clear"); got != 1 {
		t.Fatalf("expected mailbox len 1 before cleanup, got %d", got)
	}
	clearTransientSessionSteering(mailboxes, "sess-clear")
	if got := steeringMailboxLen(mailboxes, "sess-clear"); got != 0 {
		t.Fatalf("expected mailbox len 0 after cleanup, got %d", got)
	}
}

func TestPersistAndIngestInlineChannelSteeringRecordsDurableProvenance(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	docsRepo := state.NewDocsRepository(store, "author")
	transcriptRepo := state.NewTranscriptRepository(store, "author")
	engine := &capturingSteeringContextEngine{}
	items := []autoreply.SteeringMessage{
		{Text: "dm steering ignored here", EventID: "evt-dm", SenderID: "alice", Source: "dm", CreatedAt: 10},
		{Text: "channel inline update", EventID: "evt-channel", SenderID: "bob", ChannelID: "chan-1", ThreadID: "thread-1", Source: "channel", CreatedAt: 42},
	}

	persistAndIngestInlineChannelSteering(ctx, docsRepo, transcriptRepo, engine, "ch:chan-1:bob:thread:thread-1", "fallback-chan", "fallback-thread", "fallback-sender", items)

	entries, err := transcriptRepo.ListSessionAll(ctx, "ch:chan-1:bob:thread:thread-1")
	if err != nil {
		t.Fatalf("list transcript: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected only channel steering transcript entry, got %+v", entries)
	}
	entry := entries[0]
	if entry.EntryID != "evt-channel" || entry.Role != "user" || entry.Text != "channel inline update" || entry.Unix != 42 {
		t.Fatalf("unexpected transcript entry: %+v", entry)
	}
	if entry.Meta["source"] != "channel" || entry.Meta["inline_steering"] != true || entry.Meta["channel_id"] != "chan-1" || entry.Meta["thread_id"] != "thread-1" || entry.Meta["sender_id"] != "bob" {
		t.Fatalf("channel steering transcript metadata lost provenance: %+v", entry.Meta)
	}
	session, err := docsRepo.GetSession(ctx, "ch:chan-1:bob:thread:thread-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.LastInboundAt != 42 || session.PeerPubKey != "bob" {
		t.Fatalf("session doc not updated from inline channel steering: %+v", session)
	}
	if len(engine.messages) != 1 {
		t.Fatalf("expected one context ingest, got %+v", engine.messages)
	}
	msg := engine.messages[0]
	if msg.sessionID != "ch:chan-1:bob:thread:thread-1" || msg.msg.ID != "evt-channel" || msg.msg.Unix != 42 || msg.msg.Role != "user" {
		t.Fatalf("unexpected context ingest envelope: %+v", msg)
	}
	for _, want := range []string{"channel inline update", "chan-1", "thread-1", "bob"} {
		if !strings.Contains(msg.msg.Content, want) {
			t.Fatalf("context ingest content missing %q provenance: %q", want, msg.msg.Content)
		}
	}
}

type capturingSteeringContextEngine struct {
	messages []struct {
		sessionID string
		msg       ctxengine.Message
	}
}

func (e *capturingSteeringContextEngine) Ingest(_ context.Context, sessionID string, msg ctxengine.Message) (ctxengine.IngestResult, error) {
	e.messages = append(e.messages, struct {
		sessionID string
		msg       ctxengine.Message
	}{sessionID: sessionID, msg: msg})
	return ctxengine.IngestResult{Ingested: true}, nil
}

func (e *capturingSteeringContextEngine) Assemble(context.Context, string, int) (ctxengine.AssembleResult, error) {
	return ctxengine.AssembleResult{}, nil
}

func (e *capturingSteeringContextEngine) Compact(context.Context, string) (ctxengine.CompactResult, error) {
	return ctxengine.CompactResult{}, nil
}

func (e *capturingSteeringContextEngine) Bootstrap(context.Context, string, []ctxengine.Message) (ctxengine.BootstrapResult, error) {
	return ctxengine.BootstrapResult{}, nil
}

func (e *capturingSteeringContextEngine) Close() error { return nil }

func TestPersistAndIngestInlineChannelSteeringSynthesizesUniqueIDsAndKeepsLatestActivity(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	docsRepo := state.NewDocsRepository(store, "author")
	transcriptRepo := state.NewTranscriptRepository(store, "author")
	sessionID := "ch:chan-1:bob:thread:thread-1"
	if _, err := docsRepo.PutSession(ctx, sessionID, state.SessionDoc{Version: 1, SessionID: sessionID, PeerPubKey: "bob", LastInboundAt: 100}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	persistAndIngestInlineChannelSteering(ctx, docsRepo, transcriptRepo, nil, sessionID, "chan-1", "thread-1", "bob", []autoreply.SteeringMessage{
		{Text: "same text", SenderID: "bob", Source: "channel", CreatedAt: 200},
		{Text: "newer urgent same text", SenderID: "bob", Source: "channel", CreatedAt: 300},
		{Text: "same text", SenderID: "bob", Source: "channel", CreatedAt: 200},
	})

	entries, err := transcriptRepo.ListSessionAll(ctx, sessionID)
	if err != nil {
		t.Fatalf("list transcript: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected all no-event steering entries persisted, got %+v", entries)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.EntryID == "" {
			t.Fatalf("synthesized entry id was empty: %+v", entry)
		}
		if seen[entry.EntryID] {
			t.Fatalf("synthesized entry id collided: %+v", entries)
		}
		seen[entry.EntryID] = true
	}
	session, err := docsRepo.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.LastInboundAt != 300 {
		t.Fatalf("LastInboundAt should keep newest drained steering timestamp, got %d", session.LastInboundAt)
	}
}

func TestHandleBusySteerEnqueuesMailboxWithoutPostTurnQueue(t *testing.T) {
	mailboxes := autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)
	settings := queueRuntimeSettings{Mode: "steer", Cap: 10, Drop: autoreply.QueueDropSummarize}

	dmQ := autoreply.NewSessionQueue(10, autoreply.QueueDropSummarize)
	if !handleBusySteer(mailboxes, dmQ, settings, activeRunSteeringInput{
		SessionID: "sess-dm-busy",
		Text:      "dm busy steer",
		EventID:   "evt-dm-busy",
		SenderID:  "alice",
		Source:    "dm",
	}) {
		t.Fatal("expected DM busy steer to enqueue into active-run mailbox")
	}
	if dmQ.Len() != 0 {
		t.Fatalf("DM busy steer should not enqueue post-turn backlog, got len=%d", dmQ.Len())
	}
	dmPending := drainSteeringAsPending(mailboxes, "sess-dm-busy")
	if len(dmPending) != 1 || dmPending[0].Text != "dm busy steer" || dmPending[0].SenderID != "alice" {
		t.Fatalf("DM busy steer did not route to mailbox with provenance: %+v", dmPending)
	}

	channelQ := autoreply.NewSessionQueue(10, autoreply.QueueDropSummarize)
	if !handleBusySteer(mailboxes, channelQ, settings, activeRunSteeringInput{
		SessionID: "sess-channel-busy",
		Text:      "channel busy steer",
		EventID:   "evt-channel-busy",
		SenderID:  "bob",
		ChannelID: "chan-1",
		ThreadID:  "thread-1",
		Source:    "channel",
	}) {
		t.Fatal("expected channel busy steer to enqueue into active-run mailbox")
	}
	if channelQ.Len() != 0 {
		t.Fatalf("channel busy steer should not enqueue post-turn backlog, got len=%d", channelQ.Len())
	}
	channelInputs := makeActiveRunSteeringDrain(mailboxes, "sess-channel-busy", nil)(context.Background())
	if len(channelInputs) != 1 || !strings.Contains(channelInputs[0].Content, "from bob") || !strings.Contains(channelInputs[0].Content, "channel busy steer") {
		t.Fatalf("channel busy steer did not route to model-facing mailbox input: %+v", channelInputs)
	}
}

func TestRestoreDrainedSteeringAfterProviderFailureBypassesRecentDedupe(t *testing.T) {
	mailboxes := autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)
	settings := queueRuntimeSettings{Mode: "steer", Cap: 10, Drop: autoreply.QueueDropSummarize}
	drained, err := runAgentLoopThatFailsAfterSteeringDrain(t, mailboxes, settings, false)
	if err == nil {
		t.Fatal("expected provider failure after steering drain")
	}
	if len(drained) != 1 || drained[0].EventID != "evt-drained" {
		t.Fatalf("expected one tracked drained steering item, got %+v", drained)
	}
	if enqueueActiveRunSteering(mailboxes, settings, activeRunSteeringInput{SessionID: "sess-fail", Text: "duplicate", EventID: "evt-drained", Source: "dm"}) {
		t.Fatal("expected normal enqueue to dedupe the drained event id")
	}
	if restored := restoreDrainedSteering(mailboxes, settings, "sess-fail", drained); restored != 1 {
		t.Fatalf("expected restore to bypass recent dedupe for accepted drained input, got %d", restored)
	}
	pending := drainSteeringAsPending(mailboxes, "sess-fail")
	if len(pending) != 1 || pending[0].EventID != "evt-drained" || pending[0].Text != "please preserve me" {
		t.Fatalf("restored steering was not available for retry: %+v", pending)
	}
}

func TestRestoreDrainedSteeringAfterToolErrorThenProviderFailure(t *testing.T) {
	mailboxes := autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)
	settings := queueRuntimeSettings{Mode: "steer", Cap: 10, Drop: autoreply.QueueDropSummarize}
	drained, err := runAgentLoopThatFailsAfterSteeringDrain(t, mailboxes, settings, true)
	if err == nil {
		t.Fatal("expected provider failure after tool error and steering drain")
	}
	if restored := restoreDrainedSteering(mailboxes, settings, "sess-fail", drained); restored != 1 {
		t.Fatalf("expected drained steering restored after tool/provider failure, got %d", restored)
	}
	pending := drainSteeringAsPending(mailboxes, "sess-fail")
	if len(pending) != 1 || pending[0].EventID != "evt-drained" {
		t.Fatalf("expected restored pending steering after tool/provider failure, got %+v", pending)
	}
}

func TestDrainedSteeringNotRestoredAfterImmediateInterruptInvalidatesStaleInput(t *testing.T) {
	mailboxes := autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)
	settings := queueRuntimeSettings{Mode: "interrupt", Cap: 10, Drop: autoreply.QueueDropSummarize}
	chatCancels := newChatAbortRegistry()
	activeTools := newActiveToolRegistry()
	q := autoreply.NewSessionQueue(10, autoreply.QueueDropSummarize)

	drained := []autoreply.SteeringMessage{{Text: "stale inline steering", EventID: "evt-stale-inline", SenderID: "alice", Source: "dm", CreatedAt: 1}}
	ctx, release := chatCancels.Begin("sess-interrupt-drain", context.Background())
	defer release()
	if handleBusyInterrupt(chatCancels, activeTools, mailboxes, q, settings, activeRunSteeringInput{SessionID: "sess-interrupt-drain", Text: "newest interrupt", EventID: "evt-newest", SenderID: "alice", Source: "dm", CreatedAt: 2}) {
		t.Fatal("no active tools should abort immediately")
	}
	if ctx.Err() == nil || !errors.Is(context.Cause(ctx), agent.ErrTurnInterrupted) {
		t.Fatalf("expected interrupt cause, err=%v cause=%v", ctx.Err(), context.Cause(ctx))
	}
	if shouldRestoreDrainedSteering(agent.ErrTurnInterrupted) {
		restoreDrainedSteering(mailboxes, settings, "sess-interrupt-drain", drained)
	}
	q.Enqueue(autoreply.PendingTurn{Text: "newest interrupt", EventID: "evt-newest", SenderID: "alice", CreatedAt: 2})
	if pending := drainSteeringAsPending(mailboxes, "sess-interrupt-drain"); len(pending) != 0 {
		t.Fatalf("immediate interrupt should not resurrect stale drained steering: %+v", pending)
	}
	pending := q.Dequeue()
	if len(pending) != 1 || pending[0].EventID != "evt-newest" {
		t.Fatalf("expected only newest interrupt restart turn to survive, got %+v", pending)
	}
}

func runAgentLoopThatFailsAfterSteeringDrain(t *testing.T, mailboxes *autoreply.SteeringMailboxRegistry, settings queueRuntimeSettings, failTool bool) ([]autoreply.SteeringMessage, error) {
	t.Helper()
	tracker := &activeRunSteeringDrainTracker{}
	provider := &providerFailureAfterSteeringDrain{
		t:         t,
		mailboxes: mailboxes,
		settings:  settings,
		failTool:  failTool,
	}
	_, err := agent.RunAgenticLoop(context.Background(), agent.AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: []agent.LLMMessage{{Role: "user", Content: "start"}},
		Tools:           []agent.ToolDefinition{{Name: "probe"}},
		Executor:        toolExecutorForSteeringFailure{fail: failTool},
		MaxIterations:   2,
		LogPrefix:       "test",
		SessionID:       "sess-fail",
		SteeringDrain:   makeActiveRunSteeringDrain(mailboxes, "sess-fail", tracker.Record),
	})
	return tracker.Snapshot(), err
}

type providerFailureAfterSteeringDrain struct {
	t         *testing.T
	mailboxes *autoreply.SteeringMailboxRegistry
	settings  queueRuntimeSettings
	failTool  bool
	calls     int
}

func (p *providerFailureAfterSteeringDrain) Chat(_ context.Context, messages []agent.LLMMessage, _ []agent.ToolDefinition, _ agent.ChatOptions) (*agent.LLMResponse, error) {
	p.calls++
	switch p.calls {
	case 1:
		if !enqueueActiveRunSteering(p.mailboxes, p.settings, activeRunSteeringInput{SessionID: "sess-fail", Text: "please preserve me", EventID: "evt-drained", SenderID: "alice", Source: "dm", CreatedAt: 42}) {
			p.t.Fatal("expected mid-run steering enqueue to be accepted")
		}
		return &agent.LLMResponse{NeedsToolResults: true, ToolCalls: []agent.ToolCall{{ID: "tc1", Name: "probe"}}}, nil
	case 2:
		if !llmMessagesContain(messages, "please preserve me") {
			p.t.Fatalf("expected second provider call to include drained steering, got %+v", messages)
		}
		if p.failTool && !llmMessagesContain(messages, "error: tool failed") {
			p.t.Fatalf("expected failed tool result before provider failure, got %+v", messages)
		}
		return nil, errors.New("provider failed after steering drain")
	default:
		p.t.Fatalf("unexpected provider call %d", p.calls)
		return nil, errors.New("unexpected provider call")
	}
}

type toolExecutorForSteeringFailure struct {
	fail bool
}

func (e toolExecutorForSteeringFailure) Execute(context.Context, agent.ToolCall) (string, error) {
	if e.fail {
		return "", errors.New("tool failed")
	}
	return "tool ok", nil
}

func (e toolExecutorForSteeringFailure) Definitions() []agent.ToolDefinition { return nil }

func llmMessagesContain(messages []agent.LLMMessage, needle string) bool {
	for _, msg := range messages {
		if strings.Contains(msg.Content, needle) {
			return true
		}
	}
	return false
}

func TestEnqueueActiveRunSteeringDedupesDuplicateEventIDs(t *testing.T) {
	mailboxes := autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)
	settings := queueRuntimeSettings{Mode: "steer", Cap: 10, Drop: autoreply.QueueDropSummarize}

	if !enqueueActiveRunSteering(mailboxes, settings, activeRunSteeringInput{SessionID: "sess-dupe", Text: "first delivery", EventID: "evt-dupe", Source: "dm"}) {
		t.Fatal("expected first event delivery to be accepted")
	}
	if enqueueActiveRunSteering(mailboxes, settings, activeRunSteeringInput{SessionID: "sess-dupe", Text: "duplicate delivery", EventID: "evt-dupe", Source: "dm"}) {
		t.Fatal("expected duplicate event id to be rejected")
	}

	drain := makeActiveRunSteeringDrain(mailboxes, "sess-dupe", nil)
	got := drain(context.Background())
	if len(got) != 1 {
		t.Fatalf("expected one injected input after duplicate delivery, got %d", len(got))
	}
	if !strings.Contains(got[0].Content, "first delivery") || strings.Contains(got[0].Content, "duplicate delivery") {
		t.Fatalf("duplicate event leaked into model input: %q", got[0].Content)
	}
	mailbox := mailboxes.GetIfExists("sess-dupe")
	if mailbox == nil {
		t.Fatal("expected mailbox to exist for stats")
	}
	if stats := mailbox.Stats(); stats.Enqueued != 1 || stats.Deduped != 1 || stats.Drained != 1 {
		t.Fatalf("unexpected mailbox stats after duplicate delivery: %#v", stats)
	}
	if enqueueActiveRunSteering(mailboxes, settings, activeRunSteeringInput{SessionID: "sess-dupe", Text: "duplicate after drain", EventID: "evt-dupe", Source: "dm"}) {
		t.Fatal("expected recent event id to remain deduped after drain")
	}
}

func TestEnqueueActiveRunSteeringRespectsRuntimeCapacityDropPolicy(t *testing.T) {
	mailboxes := autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)

	oldestSettings := queueRuntimeSettings{Mode: "steer", Cap: 1, Drop: autoreply.QueueDropOldest}
	if !enqueueActiveRunSteering(mailboxes, oldestSettings, activeRunSteeringInput{SessionID: "sess-oldest", Text: "older", EventID: "evt-old", Source: "channel", SenderID: "alice", CreatedAt: 1}) {
		t.Fatal("expected first oldest-policy enqueue to succeed")
	}
	if !enqueueActiveRunSteering(mailboxes, oldestSettings, activeRunSteeringInput{SessionID: "sess-oldest", Text: "newer", EventID: "evt-new", Source: "channel", SenderID: "bob", CreatedAt: 2}) {
		t.Fatal("expected second oldest-policy enqueue to replace oldest item")
	}
	oldestPending := drainSteeringAsPending(mailboxes, "sess-oldest")
	if len(oldestPending) != 1 || oldestPending[0].Text != "newer" || oldestPending[0].EventID != "evt-new" {
		t.Fatalf("oldest drop policy should keep only newest item, got %+v", oldestPending)
	}

	newestSettings := queueRuntimeSettings{Mode: "steer", Cap: 1, Drop: autoreply.QueueDropNewest}
	if !enqueueActiveRunSteering(mailboxes, newestSettings, activeRunSteeringInput{SessionID: "sess-newest", Text: "kept", EventID: "evt-kept", Source: "dm"}) {
		t.Fatal("expected first newest-policy enqueue to succeed")
	}
	if enqueueActiveRunSteering(mailboxes, newestSettings, activeRunSteeringInput{SessionID: "sess-newest", Text: "dropped", EventID: "evt-dropped", Source: "dm"}) {
		t.Fatal("expected newest-policy mailbox to reject incoming item when full")
	}
	newestInputs := makeActiveRunSteeringDrain(mailboxes, "sess-newest", nil)(context.Background())
	if len(newestInputs) != 1 || !strings.Contains(newestInputs[0].Content, "kept") || strings.Contains(newestInputs[0].Content, "dropped") {
		t.Fatalf("newest drop policy should keep original item only, got %+v", newestInputs)
	}
}

func TestBusyInterruptAbortPathQueuesNewestRestartTurn(t *testing.T) {
	chatCancels := newChatAbortRegistry()
	activeTools := newActiveToolRegistry()
	mailboxes := autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)
	q := autoreply.NewSessionQueue(10, autoreply.QueueDropSummarize)
	settings := queueRuntimeSettings{Mode: "interrupt", Cap: 10, Drop: autoreply.QueueDropSummarize}
	ctx, release := chatCancels.Begin("sess-restart", context.Background())
	defer release()
	q.Enqueue(autoreply.PendingTurn{Text: "stale backlog", EventID: "evt-stale-backlog"})
	enqueueActiveRunSteering(mailboxes, settings, activeRunSteeringInput{SessionID: "sess-restart", Text: "stale steering", EventID: "evt-stale-steering", Source: "dm"})

	input := activeRunSteeringInput{
		SessionID:    "sess-restart",
		Text:         "newest interrupt",
		EventID:      "evt-newest",
		SenderID:     "alice",
		Source:       "dm",
		ToolProfile:  "safe",
		EnabledTools: []string{"read_file"},
		CreatedAt:    42,
	}
	if handleBusyInterrupt(chatCancels, activeTools, mailboxes, q, settings, input) {
		t.Fatal("no active tools should abort immediately rather than defer")
	}
	if ctx.Err() == nil || !errors.Is(context.Cause(ctx), agent.ErrTurnInterrupted) {
		t.Fatalf("expected active turn to be cancelled with interrupt cause, err=%v cause=%v", ctx.Err(), context.Cause(ctx))
	}

	// This mirrors the busy interrupt branch in main.go: after handleBusyInterrupt
	// clears stale state and aborts the current turn, the caller enqueues exactly
	// the newest inbound input as the restart turn.
	q.Enqueue(autoreply.PendingTurn{
		Text:         input.Text,
		EventID:      input.EventID,
		SenderID:     input.SenderID,
		ToolProfile:  input.ToolProfile,
		EnabledTools: append([]string(nil), input.EnabledTools...),
		CreatedAt:    input.CreatedAt,
	})
	pending := q.Dequeue()
	if len(pending) != 1 {
		t.Fatalf("expected exactly one restart turn after interrupt, got %+v", pending)
	}
	got := pending[0]
	if got.Text != "newest interrupt" || got.EventID != "evt-newest" || got.SenderID != "alice" || got.ToolProfile != "safe" || got.CreatedAt != 42 {
		t.Fatalf("restart turn did not preserve newest interrupt input: %+v", got)
	}
	if len(got.EnabledTools) != 1 || got.EnabledTools[0] != "read_file" {
		t.Fatalf("restart turn did not preserve enabled tools: %+v", got.EnabledTools)
	}
	if steeringMailboxLen(mailboxes, "sess-restart") != 0 {
		t.Fatal("expected stale steering to be cleared on immediate interrupt")
	}
}

func TestHandleBusyInterruptAbortsWhenNoToolsActive(t *testing.T) {
	chatCancels := newChatAbortRegistry()
	activeTools := newActiveToolRegistry()
	mailboxes := autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)
	q := autoreply.NewSessionQueue(10, autoreply.QueueDropSummarize)
	settings := queueRuntimeSettings{Mode: "interrupt", Cap: 10, Drop: autoreply.QueueDropSummarize}
	ctx, release := chatCancels.Begin("sess-interrupt", context.Background())
	defer release()
	q.Enqueue(autoreply.PendingTurn{Text: "older backlog", EventID: "old-backlog"})
	enqueueActiveRunSteering(mailboxes, settings, activeRunSteeringInput{SessionID: "sess-interrupt", Text: "older steering", EventID: "old-steer", Source: "dm"})

	deferred := handleBusyInterrupt(chatCancels, activeTools, mailboxes, q, settings, activeRunSteeringInput{
		SessionID: "sess-interrupt",
		Text:      "new interrupt",
		EventID:   "new-interrupt",
		Source:    "dm",
	})
	if deferred {
		t.Fatal("expected no active tools to abort immediately")
	}
	if ctx.Err() == nil {
		t.Fatal("expected active turn context to be cancelled")
	}
	if !errors.Is(context.Cause(ctx), agent.ErrTurnInterrupted) {
		t.Fatalf("expected interrupt cause, got %v", context.Cause(ctx))
	}
	if q.Len() != 0 {
		t.Fatalf("expected backlog cleared, got len=%d", q.Len())
	}
	if got := steeringMailboxLen(mailboxes, "sess-interrupt"); got != 0 {
		t.Fatalf("expected steering mailbox cleared, got len=%d", got)
	}
}

func TestHandleBusyInterruptAbortsWhenAllActiveToolsCancelable(t *testing.T) {
	chatCancels := newChatAbortRegistry()
	activeTools := newActiveToolRegistry()
	settings := queueRuntimeSettings{Mode: "interrupt", Cap: 10, Drop: autoreply.QueueDropSummarize}
	ctx, release := chatCancels.Begin("sess-cancel", context.Background())
	defer release()
	activeTools.Record(agent.ToolLifecycleEvent{
		Type:       agent.ToolLifecycleEventStart,
		SessionID:  "sess-cancel",
		ToolCallID: "tool-1",
		ToolName:   "cancelable",
		Data: agent.ToolInterruptPolicyDecision{
			Kind:              agent.ToolDecisionKindInterruptPolicy,
			InterruptBehavior: agent.ToolInterruptBehaviorCancel,
		},
	})

	deferred := handleBusyInterrupt(chatCancels, activeTools, nil, nil, settings, activeRunSteeringInput{SessionID: "sess-cancel", Text: "interrupt"})
	if deferred {
		t.Fatal("expected all-cancelable tools to abort immediately")
	}
	if ctx.Err() == nil {
		t.Fatal("expected active turn context to be cancelled")
	}
}

func TestHandleBusyInterruptDefersWhenAnyActiveToolBlocks(t *testing.T) {
	chatCancels := newChatAbortRegistry()
	activeTools := newActiveToolRegistry()
	mailboxes := autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)
	q := autoreply.NewSessionQueue(10, autoreply.QueueDropSummarize)
	settings := queueRuntimeSettings{Mode: "interrupt", Cap: 10, Drop: autoreply.QueueDropSummarize}
	ctx, release := chatCancels.Begin("sess-block", context.Background())
	defer release()
	q.Enqueue(autoreply.PendingTurn{Text: "older backlog", EventID: "old-backlog"})
	enqueueActiveRunSteering(mailboxes, settings, activeRunSteeringInput{SessionID: "sess-block", Text: "older steering", EventID: "old-steer", Source: "dm"})
	activeTools.Record(agent.ToolLifecycleEvent{
		Type:       agent.ToolLifecycleEventStart,
		SessionID:  "sess-block",
		ToolCallID: "tool-block",
		ToolName:   "blocking",
		Data: agent.ToolInterruptPolicyDecision{
			Kind:              agent.ToolDecisionKindInterruptPolicy,
			InterruptBehavior: agent.ToolInterruptBehaviorBlock,
		},
	})
	activeTools.Record(agent.ToolLifecycleEvent{
		Type:       agent.ToolLifecycleEventStart,
		SessionID:  "sess-block",
		ToolCallID: "tool-cancel",
		ToolName:   "cancelable",
		Data: agent.ToolInterruptPolicyDecision{
			Kind:              agent.ToolDecisionKindInterruptPolicy,
			InterruptBehavior: agent.ToolInterruptBehaviorCancel,
		},
	})

	deferred := handleBusyInterrupt(chatCancels, activeTools, mailboxes, q, settings, activeRunSteeringInput{
		SessionID: "sess-block",
		Text:      "newest interrupt",
		EventID:   "new-interrupt",
		Source:    "dm",
	})
	if !deferred {
		t.Fatal("expected blocking tool to defer interrupt into urgent steering")
	}
	if ctx.Err() != nil {
		t.Fatalf("expected active turn to continue, got ctx err %v", ctx.Err())
	}
	if q.Len() != 0 {
		t.Fatalf("expected backlog cleared before urgent steering, got len=%d", q.Len())
	}
	pending := drainSteeringAsPending(mailboxes, "sess-block")
	if len(pending) != 1 {
		t.Fatalf("expected only newest urgent interrupt in mailbox, got %d", len(pending))
	}
	if pending[0].Text != "newest interrupt" || pending[0].EventID != "new-interrupt" {
		t.Fatalf("unexpected pending interrupt: %+v", pending[0])
	}
}

func TestActiveToolRegistryResultClearsBlockingTool(t *testing.T) {
	activeTools := newActiveToolRegistry()
	start := agent.ToolLifecycleEvent{
		Type:       agent.ToolLifecycleEventStart,
		SessionID:  "sess-tools",
		ToolCallID: "tool-block",
		ToolName:   "blocking",
		Data: agent.ToolInterruptPolicyDecision{
			Kind:              agent.ToolDecisionKindInterruptPolicy,
			InterruptBehavior: agent.ToolInterruptBehaviorBlock,
		},
	}
	activeTools.Record(start)
	if activeTools.AllInterruptible("sess-tools") {
		t.Fatal("expected blocking tool to make session non-interruptible")
	}
	start.Type = agent.ToolLifecycleEventResult
	activeTools.Record(start)
	if !activeTools.AllInterruptible("sess-tools") {
		t.Fatal("expected session interruptible after tool result")
	}
}

func TestActiveToolRegistryCountsProviderEmptyFallbackKeys(t *testing.T) {
	activeTools := newActiveToolRegistry()
	start := agent.ToolLifecycleEvent{
		Type:      agent.ToolLifecycleEventStart,
		SessionID: "sess-empty-id",
		TurnID:    "turn-1",
		ToolName:  "blocking",
		Data: agent.ToolInterruptPolicyDecision{
			Kind:              agent.ToolDecisionKindInterruptPolicy,
			InterruptBehavior: agent.ToolInterruptBehaviorBlock,
		},
	}
	activeTools.Record(start)
	activeTools.Record(start)
	if activeTools.AllInterruptible("sess-empty-id") {
		t.Fatal("expected duplicate empty-id blocking calls to be tracked")
	}
	start.Type = agent.ToolLifecycleEventResult
	activeTools.Record(start)
	if activeTools.AllInterruptible("sess-empty-id") {
		t.Fatal("expected one remaining empty-id blocking call after first result")
	}
	activeTools.Record(start)
	if !activeTools.AllInterruptible("sess-empty-id") {
		t.Fatal("expected session interruptible after both empty-id calls complete")
	}
}

func TestOperatorAbortUnconditionalWithBlockingToolActive(t *testing.T) {
	chatCancels := newChatAbortRegistry()
	activeTools := newActiveToolRegistry()
	ctx, release := chatCancels.Begin("sess-operator", context.Background())
	defer release()
	activeTools.Record(agent.ToolLifecycleEvent{
		Type:       agent.ToolLifecycleEventStart,
		SessionID:  "sess-operator",
		ToolCallID: "tool-block",
		ToolName:   "blocking",
		Data: agent.ToolInterruptPolicyDecision{
			Kind:              agent.ToolDecisionKindInterruptPolicy,
			InterruptBehavior: agent.ToolInterruptBehaviorBlock,
		},
	})
	if activeTools.AllInterruptible("sess-operator") {
		t.Fatal("expected blocking tool to be active for regression setup")
	}
	if !chatCancels.Abort("sess-operator") {
		t.Fatal("expected operator abort to cancel in-flight session")
	}
	if ctx.Err() == nil {
		t.Fatal("expected operator abort to cancel even with blocking tool active")
	}
}
