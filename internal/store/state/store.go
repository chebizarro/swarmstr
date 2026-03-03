package state

import (
	"context"
	"errors"

	"swarmstr/internal/nostr/events"
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

// NostrStateStore defines the canonical persistence API for swarmstr.
// Implementations persist state as Nostr events.
type NostrStateStore interface {
	GetLatestReplaceable(ctx context.Context, addr Address) (Event, error)
	PutReplaceable(ctx context.Context, addr Address, content string, extraTags [][]string) (Event, error)
	PutAppend(ctx context.Context, addr Address, content string, extraTags [][]string) (Event, error)
	ListByTag(ctx context.Context, kind events.Kind, tagName, tagValue string, limit int) ([]Event, error)
	ListByTagForAuthor(ctx context.Context, kind events.Kind, authorPubKey, tagName, tagValue string, limit int) ([]Event, error)
}

var ErrNotFound = errors.New("nostr state document not found")
