package tasks

import (
	"context"
	"encoding/json"
	"time"

	"metiq/internal/store/state"
)

// EventType identifies the kind of task lifecycle event.
type EventType string

const (
	EventTaskCreated    EventType = "task.created"
	EventTaskUpdated    EventType = "task.updated"
	EventTaskCompleted  EventType = "task.completed"
	EventTaskFailed     EventType = "task.failed"
	EventTaskCancelled  EventType = "task.cancelled"
	EventRunCreated     EventType = "run.created"
	EventRunStarted     EventType = "run.started"
	EventRunCompleted   EventType = "run.completed"
	EventRunFailed      EventType = "run.failed"
	EventRunCancelled   EventType = "run.cancelled"
	EventRunBlocked     EventType = "run.blocked"
	EventWorkflowStart  EventType = "workflow.started"
	EventWorkflowStep   EventType = "workflow.step"
	EventWorkflowDone   EventType = "workflow.completed"
	EventWorkflowFailed EventType = "workflow.failed"
)

// Event represents a task lifecycle event for emission to observers.
type Event struct {
	Type      EventType      `json:"type"`
	TaskID    string         `json:"task_id,omitempty"`
	RunID     string         `json:"run_id,omitempty"`
	Status    string         `json:"status,omitempty"`
	Source    TaskSource     `json:"source,omitempty"`
	Actor     string         `json:"actor,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	Timestamp int64          `json:"timestamp"`
	Meta      map[string]any `json:"meta,omitempty"`
}

// EventEmitter broadcasts task events to registered handlers.
type EventEmitter struct {
	handlers []EventHandler
}

// EventHandler processes task events.
type EventHandler func(ctx context.Context, event Event)

// NewEventEmitter creates a new event emitter.
func NewEventEmitter() *EventEmitter {
	return &EventEmitter{}
}

// AddHandler registers an event handler.
func (e *EventEmitter) AddHandler(h EventHandler) {
	e.handlers = append(e.handlers, h)
}

// Emit broadcasts an event to all handlers.
func (e *EventEmitter) Emit(ctx context.Context, event Event) {
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().Unix()
	}
	for _, h := range e.handlers {
		h(ctx, event)
	}
}

// EmitterObserver adapts EventEmitter to the Observer interface.
type EmitterObserver struct {
	emitter *EventEmitter
}

// NewEmitterObserver creates an observer that emits events.
func NewEmitterObserver(emitter *EventEmitter) *EmitterObserver {
	return &EmitterObserver{emitter: emitter}
}

func (o *EmitterObserver) OnTaskCreated(ctx context.Context, entry LedgerEntry) {
	o.emitter.Emit(ctx, Event{
		Type:      EventTaskCreated,
		TaskID:    entry.Task.TaskID,
		Status:    string(entry.Task.Status),
		Source:    entry.Source,
		Timestamp: entry.CreatedAt,
		Meta: map[string]any{
			"title":      entry.Task.Title,
			"agent_id":   entry.Task.AssignedAgent,
			"session_id": entry.Task.SessionID,
		},
	})
}

func (o *EmitterObserver) OnTaskUpdated(ctx context.Context, entry LedgerEntry, transition state.TaskTransition) {
	eventType := EventTaskUpdated
	switch transition.To {
	case state.TaskStatusCompleted:
		eventType = EventTaskCompleted
	case state.TaskStatusFailed:
		eventType = EventTaskFailed
	case state.TaskStatusCancelled:
		eventType = EventTaskCancelled
	}

	o.emitter.Emit(ctx, Event{
		Type:      eventType,
		TaskID:    entry.Task.TaskID,
		Status:    string(transition.To),
		Source:    entry.Source,
		Actor:     transition.Actor,
		Reason:    transition.Reason,
		Timestamp: transition.At,
		Meta: map[string]any{
			"from":    string(transition.From),
			"to":      string(transition.To),
			"title":   entry.Task.Title,
			"runs":    len(entry.Runs),
		},
	})
}

func (o *EmitterObserver) OnRunCreated(ctx context.Context, entry RunEntry) {
	o.emitter.Emit(ctx, Event{
		Type:      EventRunCreated,
		TaskID:    entry.Run.TaskID,
		RunID:     entry.Run.RunID,
		Status:    string(entry.Run.Status),
		Source:    entry.Source,
		Timestamp: entry.CreatedAt,
		Meta: map[string]any{
			"attempt":  entry.Run.Attempt,
			"agent_id": entry.Run.AgentID,
			"trigger":  entry.Run.Trigger,
		},
	})
}

func (o *EmitterObserver) OnRunUpdated(ctx context.Context, entry RunEntry, transition state.TaskRunTransition) {
	eventType := EventType("run.updated")
	switch transition.To {
	case state.TaskRunStatusRunning:
		eventType = EventRunStarted
	case state.TaskRunStatusCompleted:
		eventType = EventRunCompleted
	case state.TaskRunStatusFailed:
		eventType = EventRunFailed
	case state.TaskRunStatusCancelled:
		eventType = EventRunCancelled
	case state.TaskRunStatusBlocked:
		eventType = EventRunBlocked
	}

	o.emitter.Emit(ctx, Event{
		Type:      eventType,
		TaskID:    entry.Run.TaskID,
		RunID:     entry.Run.RunID,
		Status:    string(transition.To),
		Source:    entry.Source,
		Actor:     transition.Actor,
		Reason:    transition.Reason,
		Timestamp: transition.At,
		Meta: map[string]any{
			"from":    string(transition.From),
			"to":      string(transition.To),
			"attempt": entry.Run.Attempt,
		},
	})
}

// ToJSON serializes an event to JSON.
func (e Event) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

// ParseEvent deserializes an event from JSON.
func ParseEvent(data []byte) (Event, error) {
	var e Event
	err := json.Unmarshal(data, &e)
	return e, err
}
