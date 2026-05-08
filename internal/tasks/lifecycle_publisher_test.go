package tasks

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/nostr/events"
)

type lifecycleTestKeyer struct{}

func (lifecycleTestKeyer) GetPublicKey(context.Context) (nostr.PubKey, error) {
	return nostr.PubKey{}, nil
}

func (lifecycleTestKeyer) SignEvent(ctx context.Context, _ *nostr.Event) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func (lifecycleTestKeyer) Encrypt(context.Context, string, nostr.PubKey) (string, error) {
	return "", nil
}
func (lifecycleTestKeyer) Decrypt(context.Context, string, nostr.PubKey) (string, error) {
	return "", nil
}

func TestBuildLifecycleNostrEventRunProjection(t *testing.T) {
	evt, err := BuildLifecycleNostrEvent(Event{
		Type:      EventRunStarted,
		TaskID:    "task-1",
		RunID:     "run-1",
		Status:    "running",
		Source:    TaskSourceManual,
		Actor:     "agent-main",
		Reason:    "started execution",
		Timestamp: 1712966400,
		Meta: map[string]any{
			"from":       "queued",
			"to":         "running",
			"goal_id":    "goal-1",
			"session_id": "session-1",
			"agent_id":   "agent-main",
		},
	}, time.Unix(1712966400, 0))
	if err != nil {
		t.Fatalf("BuildLifecycleNostrEvent: %v", err)
	}
	if evt.Kind != nostr.Kind(events.KindLifecycle) {
		t.Fatalf("kind = %d, want %d", evt.Kind, events.KindLifecycle)
	}
	wantTags := map[string]string{
		"d":         "task-1:run-1",
		"t":         "task-1",
		"task_id":   "task-1",
		"run":       "run-1",
		"goal":      "goal-1",
		"session":   "session-1",
		"agent":     "agent-main",
		"stage":     "running",
		"status":    "running",
		"lifecycle": "run.started",
		"source":    "manual",
	}
	for key, want := range wantTags {
		if got := firstTagValue(evt.Tags, key); got != want {
			t.Fatalf("tag %q = %q, want %q (tags=%v)", key, got, want, evt.Tags)
		}
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(evt.Content), &payload); err != nil {
		t.Fatalf("content JSON: %v", err)
	}
	if got := payload["event_type"]; got != "run.started" {
		t.Fatalf("event_type = %#v", got)
	}
	if got := payload["from_status"]; got != "queued" {
		t.Fatalf("from_status = %#v", got)
	}
	if got := payload["to_status"]; got != "running" {
		t.Fatalf("to_status = %#v", got)
	}
}

func TestLifecyclePublisherEmitterHandlerIsNonBlockingWhenPublishStalls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	publishStarted := make(chan struct{})
	releasePublish := make(chan struct{})
	publishCalls := make(chan nostr.Event, 4)
	publisher, err := NewLifecyclePublisher(ctx, LifecyclePublisherOptions{
		Keyer:          lifecycleTestKeyer{},
		Relays:         []string{"wss://relay.example"},
		Buffer:         1,
		PublishTimeout: time.Second,
		Logf:           func(string, ...any) {},
		PublishFunc: func(ctx context.Context, _ []string, evt nostr.Event) error {
			publishCalls <- evt
			select {
			case <-publishStarted:
			default:
				close(publishStarted)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-releasePublish:
				return nil
			}
		},
	})
	if err != nil {
		t.Fatalf("NewLifecyclePublisher: %v", err)
	}
	defer publisher.Stop()
	emitter := NewEventEmitter()
	publisher.Subscribe(emitter)

	emitter.Emit(ctx, lifecycleTestEvent("task-1", "run-1"))
	select {
	case <-publishStarted:
	case <-time.After(time.Second):
		t.Fatal("publisher did not start publishing first event")
	}

	// Fill the queue while the worker is blocked publishing the first event.
	emitter.Emit(ctx, lifecycleTestEvent("task-2", "run-2"))

	done := make(chan struct{})
	go func() {
		emitter.Emit(ctx, lifecycleTestEvent("task-3", "run-3"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("emitter handler blocked behind stalled relay publish")
	}

	close(releasePublish)
	select {
	case evt := <-publishCalls:
		if evt.Kind != nostr.Kind(events.KindLifecycle) {
			t.Fatalf("published kind = %d, want %d", evt.Kind, events.KindLifecycle)
		}
	default:
		t.Fatal("expected at least one publish call")
	}
}

func lifecycleTestEvent(taskID, runID string) Event {
	return Event{
		Type:      EventRunStarted,
		TaskID:    taskID,
		RunID:     runID,
		Status:    "running",
		Source:    TaskSourceManual,
		Timestamp: time.Now().Unix(),
		Meta: map[string]any{
			"from": "queued",
			"to":   "running",
		},
	}
}

func firstTagValue(tags nostr.Tags, key string) string {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == key {
			return tag[1]
		}
	}
	return ""
}
