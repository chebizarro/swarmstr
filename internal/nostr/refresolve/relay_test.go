package refresolve

import (
	"context"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	nostrmeta "metiq/internal/nostr/metadata"
)

type fakeFetchCall struct {
	relays []string
	filter nostr.Filter
}

type fakeFetcher struct {
	events   []nostr.RelayEvent
	delay    time.Duration
	fetchCalls []fakeFetchCall
	mu       chan struct{}
}

func (f *fakeFetcher) Fetch(_ context.Context, relays []string, filter nostr.Filter) <-chan nostr.RelayEvent {
	f.fetchCalls = append(f.fetchCalls, fakeFetchCall{relays: relays, filter: filter})
	ch := make(chan nostr.RelayEvent)
	go func() {
		if f.delay > 0 {
			time.Sleep(f.delay)
		}
		for _, ev := range f.events {
			ch <- ev
		}
		close(ch)
	}()
	return ch
}

func signedEvent(t *testing.T, content string) nostr.RelayEvent {
	t.Helper()
	sk := nostr.Generate()
	ev := nostr.Event{Kind: 1, CreatedAt: nostr.Now(), Content: content}
	ev.Sign(sk)
	return nostr.RelayEvent{Event: ev}
}

func TestRelayResolver_NilFetcher(t *testing.T) {
	r := NewRelayResolver(nil, nil)
	refs := []Ref{{Role: "parent", Event: nostrmeta.EventRef{ID: hex64('a')}}}
	if got := r.Resolve(context.Background(), refs); got != nil {
		t.Errorf("expected nil for nil fetcher, got %v", got)
	}
}

func TestRelayResolver_EmptyRefs(t *testing.T) {
	r := NewRelayResolver(&fakeFetcher{}, nil)
	if got := r.Resolve(context.Background(), nil); got != nil {
		t.Errorf("expected nil for empty refs, got %v", got)
	}
}

func TestRelayResolver_FetchMiss(t *testing.T) {
	fake := &fakeFetcher{}
	r := NewRelayResolver(fake, []string{"wss://lane.example"})
	evt := signedEvent(t, "hello")
	matchingID := evt.ID.Hex()
	refs := []Ref{{Role: "parent", Event: nostrmeta.EventRef{ID: matchingID}}}
	results := r.Resolve(context.Background(), refs)
	if len(results) != 0 {
		t.Fatalf("expected 0 results (relay returned nothing), got %d", len(results))
	}
}

func TestRelayResolver_BatchedFilter(t *testing.T) {
	fake := &fakeFetcher{}
	r := NewRelayResolver(fake, []string{"wss://lane.example"})

	var evts []nostr.RelayEvent
	idHexes := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		ev := signedEvent(t, "msg")
		evts = append(evts, ev)
		idHexes = append(idHexes, ev.ID.Hex())
	}
	fake.events = evts

	refs := []Ref{
		{Role: "parent", Event: nostrmeta.EventRef{ID: idHexes[0]}},
		{Role: "quote", Event: nostrmeta.EventRef{ID: idHexes[1]}},
		{Role: "root", Event: nostrmeta.EventRef{ID: idHexes[2]}},
	}
	results := r.Resolve(context.Background(), refs)

	if len(fake.fetchCalls) != 1 {
		t.Fatalf("expected exactly 1 fetch call (batched), got %d", len(fake.fetchCalls))
	}
	call := fake.fetchCalls[0]
	if len(call.filter.IDs) != 3 {
		t.Errorf("expected filter with 3 IDs, got %d", len(call.filter.IDs))
	}
	want := map[string]bool{}
	for _, id := range call.filter.IDs {
		want[id.Hex()] = true
	}
	for _, h := range idHexes {
		if !want[h] {
			t.Errorf("filter missing requested ID %s", h)
		}
	}
	if call.filter.Limit != 3 {
		t.Errorf("expected filter limit 3, got %d", call.filter.Limit)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].Via != "relay" || results[0].Body != "msg" {
		t.Errorf("unexpected first result: %+v", results[0])
	}
}

func TestRelayResolver_RelayUnionCap(t *testing.T) {
	fake := &fakeFetcher{}
	r := NewRelayResolver(fake, []string{"l1", "l2", "l3"})

	var evts []nostr.RelayEvent
	idHexes := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		ev := signedEvent(t, "x")
		evts = append(evts, ev)
		idHexes = append(idHexes, ev.ID.Hex())
	}
	fake.events = evts

	refs := []Ref{
		{Role: "parent", Event: nostrmeta.EventRef{ID: idHexes[0], Relay: "h1"}},
		{Role: "quote", Event: nostrmeta.EventRef{ID: idHexes[1], Relay: "h2"}},
		{Role: "root", Event: nostrmeta.EventRef{ID: idHexes[2], Relay: "h3"}},
	}
	r.Resolve(context.Background(), refs)

	if len(fake.fetchCalls) != 1 {
		t.Fatal("expected 1 fetch call")
	}
	relays := fake.fetchCalls[0].relays
	if n := len(relays); n > 4 || n < 1 {
		t.Errorf("relay set cap 4: got %d relays", n)
	}
	seen := make(map[string]bool, len(relays))
	for _, rl := range relays {
		seen[rl] = true
	}
	if !seen["h1"] && !seen["h2"] && !seen["h3"] {
		t.Errorf("expected at least one per-ref relay hint in union, got %v", relays)
	}
	if !seen["l1"] && !seen["l2"] && !seen["l3"] {
		t.Errorf("expected at least one lane relay in union, got %v", relays)
	}
}

func TestRelayResolver_InvalidSignatureDropped(t *testing.T) {
	sk := nostr.Generate()
	ev := nostr.Event{Kind: 1, CreatedAt: nostr.Now(), Content: "originally signed"}
	ev.Sign(sk)
	ev.Sig = [64]byte{}

	fake := &fakeFetcher{events: []nostr.RelayEvent{{Event: ev}}}
	r := NewRelayResolver(fake, nil)
	refs := []Ref{{Role: "parent", Event: nostrmeta.EventRef{ID: ev.ID.Hex()}}}
	results := r.Resolve(context.Background(), refs)
	if !ev.CheckID() {
		t.Fatal("test setup: event must still pass CheckID")
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for invalid signature, got %d", len(results))
	}
}

func TestRelayResolver_BadCheckIDDropped(t *testing.T) {
	sk := nostr.Generate()
	ev := nostr.Event{Kind: 1, CreatedAt: nostr.Now(), Content: "wrong id"}
	ev.Sign(sk)
	ev.ID = nostr.ID{}

	fake := &fakeFetcher{events: []nostr.RelayEvent{{Event: ev}}}
	r := NewRelayResolver(fake, nil)
	refs := []Ref{{Role: "parent", Event: nostrmeta.EventRef{ID: hex64('a')}}}
	results := r.Resolve(context.Background(), refs)
	if len(results) != 0 {
		t.Fatalf("expected 0 results for bad CheckID, got %d", len(results))
	}
}

func TestRelayResolver_UnrequestedEventIgnored(t *testing.T) {
	evt := signedEvent(t, "noise")
	fake := &fakeFetcher{events: []nostr.RelayEvent{evt}}
	r := NewRelayResolver(fake, nil)
	refs := []Ref{{Role: "parent", Event: nostrmeta.EventRef{ID: hex64('b')}}}
	results := r.Resolve(context.Background(), refs)
	if len(results) != 0 {
		t.Fatalf("expected 0 results when relay sends unrequested event, got %d", len(results))
	}
}

func TestRelayResolver_DeadlinePartialResults(t *testing.T) {
	quick := signedEvent(t, "quick")

	// Create a fetcher that sends one event then blocks until context is done.
	// doFetch's internal WithTimeout(RelayTimeout) will fire and we get only quick.
	ch := make(chan nostr.RelayEvent)
	go func() {
		ch <- quick
		<-time.After(10 * time.Second)
		close(ch)
	}()

	fake := &fakeFetcher{}
	delayed := &delayingFetcher{inner: fake, events: ch}

	// Use a known slow ID that the fake fetcher will never send — it will
	// be requested in the batch but never arrive, forcing deadline timeout.
	slowID := hex64('s')

	r := NewRelayResolver(delayed, nil)
	refs := []Ref{
		{Role: "parent", Event: nostrmeta.EventRef{ID: quick.ID.Hex()}},
		{Role: "quote", Event: nostrmeta.EventRef{ID: slowID}},
	}
	start := time.Now()
	results := r.Resolve(context.Background(), refs)
	elapsed := time.Since(start)

	if len(results) != 1 {
		t.Fatalf("expected only the quick event (deadline), got %d", len(results))
	}
	if results[0].ID != quick.ID.Hex() {
		t.Errorf("expected quick event, got %s", results[0].ID)
	}
	if elapsed > RelayTimeout+time.Second {
		t.Errorf("Resolve took too long: %s (expected ~%s)", elapsed, RelayTimeout)
	}
}

type delayingFetcher struct {
	inner  *fakeFetcher
	events chan nostr.RelayEvent
}

func (d *delayingFetcher) Fetch(ctx context.Context, relays []string, filter nostr.Filter) <-chan nostr.RelayEvent {
	d.inner.fetchCalls = append(d.inner.fetchCalls, fakeFetchCall{relays: relays, filter: filter})
	return d.events
}

func TestRelayResolver_NegativeCacheNoSecondFetch(t *testing.T) {
	fake := &fakeFetcher{}
	r := NewRelayResolver(fake, nil)
	refs := []Ref{{Role: "parent", Event: nostrmeta.EventRef{ID: hex64('a')}}}

	r.Resolve(context.Background(), refs)
	if len(fake.fetchCalls) != 1 {
		t.Fatalf("expected 1 fetch call on miss, got %d", len(fake.fetchCalls))
	}

	r.Resolve(context.Background(), refs)
	if len(fake.fetchCalls) != 1 {
		t.Fatalf("expected 0 additional fetches (negative cache), got %d calls", len(fake.fetchCalls))
	}
}

func TestRelayResolver_PositiveCacheNoSecondFetch(t *testing.T) {
	evt := signedEvent(t, "cached body")
	fake := &fakeFetcher{events: []nostr.RelayEvent{evt}}
	r := NewRelayResolver(fake, nil)
	refs := []Ref{{Role: "parent", Event: nostrmeta.EventRef{ID: evt.ID.Hex()}}}

	first := r.Resolve(context.Background(), refs)
	if len(first) != 1 {
		t.Fatalf("expected 1 result on first resolve, got %d", len(first))
	}
	if len(fake.fetchCalls) != 1 {
		t.Fatalf("expected 1 fetch call on first resolve, got %d", len(fake.fetchCalls))
	}

	second := r.Resolve(context.Background(), refs)
	if len(second) != 1 {
		t.Fatalf("expected 1 result from cache, got %d", len(second))
	}
	if len(fake.fetchCalls) != 1 {
		t.Fatalf("expected 0 additional fetches (positive cache), got %d calls", len(fake.fetchCalls))
	}
	if second[0].Body != "cached body" {
		t.Errorf("expected cached body, got %q", second[0].Body)
	}
}

func TestRelayResolver_PositiveCacheExpires(t *testing.T) {
	evt := signedEvent(t, "expiring body")
	fake := &fakeFetcher{events: []nostr.RelayEvent{evt}}
	r := NewRelayResolver(fake, nil)
	refs := []Ref{{Role: "parent", Event: nostrmeta.EventRef{ID: evt.ID.Hex()}}}

	r.Resolve(context.Background(), refs)
	if len(fake.fetchCalls) != 1 {
		t.Fatalf("expected 1 fetch call, got %d", len(fake.fetchCalls))
	}

	// Force cache TTL to be in the past so the next resolve refetches.
	r.mu.Lock()
	if entry, ok := r.cache[evt.ID.Hex()]; ok {
		entry.expiresAt = time.Now().Add(-time.Second)
		r.cache[evt.ID.Hex()] = entry
	}
	r.mu.Unlock()

	r.Resolve(context.Background(), refs)
	if len(fake.fetchCalls) != 2 {
		t.Errorf("expected a refetch after TTL expiry, got %d calls", len(fake.fetchCalls))
	}
}