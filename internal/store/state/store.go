package state

import (
	"context"
	"errors"

	"metiq/internal/nostr/events"
)

type Event struct {
	ID        string
	PubKey    string
	Kind      events.Kind
	CreatedAt int64
	Tags      [][]string
	Content   string
	Sig       string
}

type Address struct {
	Kind   events.Kind
	PubKey string
	DTag   string
}

// EventPageCursor identifies the next page in newest-first tag queries.
// The next query should include events at or before Until while skipping
// SkipIDs already returned at that exact timestamp boundary.
type EventPageCursor struct {
	Until   int64
	SkipIDs []string
}

// EventPage is a single newest-first page from a tag query.
type EventPage struct {
	Events     []Event
	NextCursor *EventPageCursor
}

// NostrStateStore defines the canonical persistence API for metiq.
// Implementations persist state as Nostr events.
type NostrStateStore interface {
	GetLatestReplaceable(ctx context.Context, addr Address) (Event, error)
	PutReplaceable(ctx context.Context, addr Address, content string, extraTags [][]string) (Event, error)
	PutAppend(ctx context.Context, addr Address, content string, extraTags [][]string) (Event, error)
	ListByTag(ctx context.Context, kind events.Kind, tagName, tagValue string, limit int) ([]Event, error)
	ListByTagForAuthor(ctx context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int) ([]Event, error)
	ListByTagPage(ctx context.Context, kind events.Kind, tagName, tagValue string, limit int, cursor *EventPageCursor) (EventPage, error)
	ListByTagForAuthorPage(ctx context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int, cursor *EventPageCursor) (EventPage, error)
}

var ErrNotFound = errors.New("nostr state document not found")
