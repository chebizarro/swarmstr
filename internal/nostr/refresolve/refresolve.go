package refresolve

import (
	"context"
	"time"

	nostrmeta "metiq/internal/nostr/metadata"
)

const (
	MaxRefs       = 3
	MaxBodyChars  = 500
	MaxBlockChars = 2000
	RelayTimeout  = 1500 * time.Millisecond
)

type ReferencedMessage struct {
	ID        string
	Author    string
	Kind      int
	CreatedAt int64
	Body      string
	Role      string // "parent" | "quote" | "root" — copied from the Ref that requested it
	Via       string // "local" | "relay" | "embedded"; diagnostics only
	Deleted   bool
}

type Ref struct {
	Role  string
	Event nostrmeta.EventRef
}

type Resolver interface {
	Resolve(ctx context.Context, refs []Ref) []ReferencedMessage
}

type Chained struct {
	Local Resolver
	Relay Resolver
}

func (c *Chained) Resolve(ctx context.Context, refs []Ref) []ReferencedMessage {
	if len(refs) == 0 {
		return nil
	}
	if c.Local == nil && c.Relay == nil {
		return nil
	}

	var results []ReferencedMessage
	resolved := make(map[string]bool)

	if c.Local != nil {
		results = c.Local.Resolve(ctx, refs)
		for _, r := range results {
			resolved[r.ID] = true
		}
	}

	if c.Relay == nil {
		return results
	}

	var missing []Ref
	for _, ref := range refs {
		if !resolved[ref.Event.ID] {
			missing = append(missing, ref)
		}
	}
	if len(missing) > 0 {
		relayResults := c.Relay.Resolve(ctx, missing)
		results = append(results, relayResults...)
	}

	return results
}

func SelectRefs(thread *nostrmeta.ThreadRefs, selfEventID string) []Ref {
	if thread == nil {
		return nil
	}
	seen := make(map[string]bool)
	var refs []Ref

	addIfNew := func(role string, ref *nostrmeta.EventRef) {
		if ref == nil {
			return
		}
		if seen[ref.ID] {
			return
		}
		if ref.ID == selfEventID {
			return
		}
		seen[ref.ID] = true
		refs = append(refs, Ref{Role: role, Event: *ref})
	}

	addIfNew("parent", thread.Parent)
	addIfNew("quote", thread.Quote)
	addIfNew("root", thread.Root)

	if len(refs) > MaxRefs {
		refs = refs[:MaxRefs]
	}
	return refs
}

func ResolverFor(protocol nostrmeta.Protocol, chained, localOnly Resolver) Resolver {
	switch protocol {
	case nostrmeta.ProtocolNIP04, nostrmeta.ProtocolNIP17, nostrmeta.ProtocolConcord:
		if localOnly != nil {
			return localOnly
		}
		return chained
	default:
		return chained
	}
}
