package main

import (
	"context"
	"strings"
	"testing"

	"metiq/internal/gateway/channels"
	"metiq/internal/nostr/refresolve"
	"metiq/internal/store/state"
	nostr "fiatjaf.com/nostr"

	nostrmeta "metiq/internal/nostr/metadata"
)

func hex64(b byte) string {
	s := make([]byte, 64)
	for i := range s {
		s[i] = b
	}
	return string(s)
}

type countingFakeFetcher struct {
	fetchCount int
}

func (c *countingFakeFetcher) Fetch(ctx context.Context, relays []string, filter nostr.Filter) <-chan nostr.RelayEvent {
	c.fetchCount++
	ch := make(chan nostr.RelayEvent)
	close(ch)
	return ch
}

type countingFakeLookup struct {
	getCount int
}

func (c *countingFakeLookup) GetEntryByNostrEventID(ctx context.Context, eventID string) (state.TranscriptEntryDoc, error) {
	c.getCount++
	idA := hex64('a')
	if eventID == idA {
		return state.TranscriptEntryDoc{
			EntryID: idA,
			Text:    "local reply body",
			Unix:    1700000000,
			Meta: map[string]any{
				"nostr_event_id": idA,
				"nostr_pubkey":   hex64('b'),
				"nostr_kind":     1,
			},
		}, nil
	}
	return state.TranscriptEntryDoc{}, state.ErrNotFound
}

func TestRenderReferencedBlock_NIP17LocalOnly(t *testing.T) {
	fakeLookup := &countingFakeLookup{}
	fakeFetcher := &countingFakeFetcher{}

	chained := &refresolve.Chained{
		Local: refresolve.NewLocalResolver(fakeLookup),
		Relay: refresolve.NewRelayResolver(fakeFetcher, nil),
	}
	localOnly := &refresolve.Chained{Local: refresolve.NewLocalResolver(fakeLookup)}

	selfID := hex64('z')
	parentID := hex64('a')
	thread := &nostrmeta.ThreadRefs{
		Parent: &nostrmeta.EventRef{ID: parentID},
	}
	cfg := referenceContextConfig{Enabled: true, MaxRefs: 3, MaxBlockChars: 2000, RelayFetch: true}

	// NIP-17 uses ResolverFor which returns localOnly when localOnly is
	// non-nil, so the relay resolver must never be called.
	sel := refresolve.ResolverFor(nostrmeta.ProtocolNIP17, chained, localOnly)
	block := renderReferencedBlock(context.Background(), sel, thread, selfID, cfg)
	if block == "" {
		t.Fatal("expected non-empty referenced block for NIP-17 reply")
	}
	if !strings.Contains(block, "local reply body") {
		t.Errorf("block missing reply body: %s", block)
	}
	if !strings.Contains(block, "[reply target]") {
		t.Errorf("block missing role label: %s", block)
	}
	if fakeFetcher.fetchCount != 0 {
		t.Errorf("expected zero relay fetches for NIP-17 local-only, got %d", fakeFetcher.fetchCount)
	}
	if fakeLookup.getCount == 0 {
		t.Error("expected at least one local lookup")
	}
}

func TestRenderReferencedBlock_NIP29Chained(t *testing.T) {
	fakeLookup := &countingFakeLookup{}
	fakeFetcher := &countingFakeFetcher{}

	chained := &refresolve.Chained{
		Local: refresolve.NewLocalResolver(fakeLookup),
		Relay: refresolve.NewRelayResolver(fakeFetcher, nil),
	}
	localOnly := &refresolve.Chained{Local: refresolve.NewLocalResolver(fakeLookup)}

	selfID := hex64('z')
	parentID := hex64('a')
	quoteID := hex64('c')
	rootID := hex64('d')
	thread := &nostrmeta.ThreadRefs{
		Root:   &nostrmeta.EventRef{ID: rootID},
		Parent: &nostrmeta.EventRef{ID: parentID},
		Quote:  &nostrmeta.EventRef{ID: quoteID},
	}
	cfg := referenceContextConfig{Enabled: true, MaxRefs: 3, MaxBlockChars: 2000, RelayFetch: true}

	// NIP-29 uses ResolverFor which returns chained (has relay).
	// parentID resolves locally; quoteID and rootID miss local and get relay
	// fetches.
	sel := refresolve.ResolverFor(nostrmeta.ProtocolNIP29, chained, localOnly)
	block := renderReferencedBlock(context.Background(), sel, thread, selfID, cfg)
	if block == "" {
		t.Fatal("expected non-empty referenced block for NIP-29 reply")
	}
	if !strings.Contains(block, "local reply body") {
		t.Errorf("block missing local reply body: %s", block)
	}
	// relay should have been called for the two missing refs.
	if fakeFetcher.fetchCount == 0 {
		t.Error("expected at least one relay fetch for NIP-29")
	}
}

func TestRenderReferencedBlock_Disabled(t *testing.T) {
	thread := &nostrmeta.ThreadRefs{
		Parent: &nostrmeta.EventRef{ID: hex64('a')},
	}
	cfg := referenceContextConfig{Enabled: false, MaxRefs: 3, MaxBlockChars: 2000, RelayFetch: true}
	block := renderReferencedBlock(context.Background(), nil, thread, hex64('z'), cfg)
	if block != "" {
		t.Errorf("expected empty block when enabled=false, got: %s", block)
	}
}

func TestRenderReferencedBlock_MaxRefsZero(t *testing.T) {
	thread := &nostrmeta.ThreadRefs{
		Parent: &nostrmeta.EventRef{ID: hex64('a')},
	}
	cfg := referenceContextConfig{Enabled: true, MaxRefs: 0, MaxBlockChars: 2000, RelayFetch: true}
	block := renderReferencedBlock(context.Background(), nil, thread, hex64('z'), cfg)
	if block != "" {
		t.Errorf("expected empty block when max_refs=0, got: %s", block)
	}
}

func TestRenderReferencedBlock_NilThread(t *testing.T) {
	cfg := referenceContextConfig{Enabled: true, MaxRefs: 3, MaxBlockChars: 2000, RelayFetch: true}
	block := renderReferencedBlock(context.Background(), nil, nil, "", cfg)
	if block != "" {
		t.Errorf("expected empty block for nil thread, got: %s", block)
	}
}

func TestRenderReferencedBlock_SelfSkip(t *testing.T) {
	fakeLookup := &countingFakeLookup{}
	selfID := hex64('a') // same as the parent ID the fake lookup has
	thread := &nostrmeta.ThreadRefs{
		Parent: &nostrmeta.EventRef{ID: selfID},
	}
	cfg := referenceContextConfig{Enabled: true, MaxRefs: 3, MaxBlockChars: 2000, RelayFetch: false}
	localOnly := &refresolve.Chained{Local: refresolve.NewLocalResolver(fakeLookup)}
	block := renderReferencedBlock(context.Background(), localOnly, thread, selfID, cfg)
	if block != "" {
		t.Errorf("expected empty block when self skipped, got: %s", block)
	}
}

func TestParseRepostBody_FullJSON(t *testing.T) {
	content := `{"id":"a1b2c3","pubkey":"d4e5f6","created_at":1700000000,"kind":1,"tags":[],"content":"hello from repost","sig":"g7h8i9"}`
	body := parseRepostBody(content)
	if body != "hello from repost" {
		t.Errorf("expected 'hello from repost', got %q", body)
	}
}

func TestParseRepostBody_BoundedScan(t *testing.T) {
	// Simulate truncation by embedding the JSON in a body that looks like a
	// truncated repost JSON: missing the closing } but still has "content".
	body := `{"id":"a1b2","pubkey":"d4e5","created_at":1700000000,"kind":1,"tags":[],"content":"found content","sig":"g7h8"}`
	// Truncate to just past the content field.
	truncated := body[:len(`{"id":"a1b2","pubkey":"d4e5","created_at":1700000000,"kind":1,"tags":[],"content":"found content"`)] // cuts before sig
	result := parseRepostBody(truncated)
	if result != "found content" {
		t.Errorf("expected 'found content' from bounded scan, got %q", result)
	}
}

func TestParseRepostBody_NoMatch(t *testing.T) {
	result := parseRepostBody("not json at all")
	if result != "" {
		t.Errorf("expected empty for non-JSON, got %q", result)
	}
}

func TestRepostInRenderReferencedBlock(t *testing.T) {
	// Create a relay event that is kind 6 with a repost JSON body.
	sk := nostr.Generate()
	innerContent := "original reposted message"
	innerJSON := `{"id":"aaaa","kind":1,"content":"` + innerContent + `"}`
	ev := nostr.Event{
		Kind:      6,
		CreatedAt: nostr.Now(),
		Content:   innerJSON,
	}
	ev.Sign(sk)

	fakeFetcher := &fakeFetcher{events: []nostr.RelayEvent{{Event: ev}}}
	r := refresolve.NewRelayResolver(fakeFetcher, nil)
	selfID := hex64('z')
	refID := ev.ID.Hex()
	thread := &nostrmeta.ThreadRefs{
		Parent: &nostrmeta.EventRef{ID: refID},
	}
	cfg := referenceContextConfig{Enabled: true, MaxRefs: 3, MaxBlockChars: 2000, RelayFetch: true}
	block := renderReferencedBlock(context.Background(), r, thread, selfID, cfg)
	if block == "" {
		t.Fatal("expected non-empty block for repost")
	}
	if !strings.Contains(block, "repost of: "+innerContent) {
		t.Errorf("block missing repost content: %s", block)
	}
	if !strings.Contains(block, "kind:6") {
		t.Errorf("block missing kind:6: %s", block)
	}
}

func TestResolveReferenceContextConfig_Defaults(t *testing.T) {
	cfg := resolveReferenceContextConfig(state.ConfigDoc{})
	if !cfg.Enabled {
		t.Error("expected enabled default true")
	}
	if cfg.MaxRefs != 3 {
		t.Errorf("expected max_refs default 3, got %d", cfg.MaxRefs)
	}
	if cfg.MaxBlockChars != 2000 {
		t.Errorf("expected max_block_chars default 2000, got %d", cfg.MaxBlockChars)
	}
	if !cfg.RelayFetch {
		t.Error("expected relay_fetch default true")
	}
}

func TestResolveReferenceContextConfig_Override(t *testing.T) {
	cfg := resolveReferenceContextConfig(state.ConfigDoc{
		Extra: map[string]any{
			"nostr": map[string]any{
				"reference_context": map[string]any{
					"enabled":       false,
					"max_refs":      1,
					"max_block_chars": 100,
					"relay_fetch":   false,
				},
			},
		},
	})
	if cfg.Enabled {
		t.Error("expected enabled false")
	}
	if cfg.MaxRefs != 1 {
		t.Errorf("expected max_refs 1, got %d", cfg.MaxRefs)
	}
	if cfg.MaxBlockChars != 100 {
		t.Errorf("expected max_block_chars 100, got %d", cfg.MaxBlockChars)
	}
	if cfg.RelayFetch {
		t.Error("expected relay_fetch false")
	}
}

func TestResolveReferenceContextConfig_ExtraNil(t *testing.T) {
	cfg := resolveReferenceContextConfig(state.ConfigDoc{Extra: nil})
	if !cfg.Enabled {
		t.Error("expected defaults when Extra is nil")
	}
}

// fakeFetcher that produces the given events after a small delay, used by
// TestRepostInRenderReferencedBlock.
type fakeFetcher struct {
	events []nostr.RelayEvent
}

func (f *fakeFetcher) Fetch(_ context.Context, relays []string, filter nostr.Filter) <-chan nostr.RelayEvent {
	ch := make(chan nostr.RelayEvent)
	go func() {
		for _, ev := range f.events {
			ch <- ev
		}
		close(ch)
	}()
	return ch
}

// TestBlockSelection: DMs get local-only, rooms get chained.
// The protocols and how the resolver is selected is tested in
// the refresolve package's TestResolverFor. This test validates
// that the concatenation of meta block and ref block works end-to-end.

func TestRoomTurnBlockIncludesRef(t *testing.T) {
	fakeLookup := &countingFakeLookup{}
	bot := hex64('b')
	sender := hex64('s')
	parentID := hex64('a')
	msg := channels.InboundMessage{
		Protocol:   nostrmeta.ProtocolNIP29,
		FromPubKey: sender,
		Text:       "hello in room",
		EventID:    hex64('z'),
		Tags: []nostr.Tag{
			{"e", hex64('c'), "", "root"},
			{"e", parentID, "", "reply"},
			{"p", bot},
			{"p", sender},
		},
	}
	cfg := referenceContextConfig{Enabled: true, MaxRefs: 3, MaxBlockChars: 2000, RelayFetch: false}
	chained := &refresolve.Chained{Local: refresolve.NewLocalResolver(fakeLookup)}
	localOnly := &refresolve.Chained{Local: refresolve.NewLocalResolver(fakeLookup)}

	block := buildRoomTurnBlock(context.Background(), msg, bot, cfg, chained, localOnly)
	if block == "" {
		t.Fatal("expected non-empty room turn block")
	}
	// Should contain the metadata block (Nostr message metadata header).
	if !strings.Contains(block, "Nostr message metadata") {
		t.Errorf("block missing metadata header: %s", block)
	}
	// Should contain the referenced block (local reply body).
	if !strings.Contains(block, "local reply body") {
		t.Errorf("block missing ref body: %s", block)
	}
}

func TestRoomTurnBlockDisabledDoesNotCrash(t *testing.T) {
	msg := channels.InboundMessage{
		Protocol:   nostrmeta.ProtocolNIP29,
		FromPubKey: hex64('s'),
		Text:       "hello",
		EventID:    hex64('z'),
		Tags: []nostr.Tag{
			{"e", hex64('a'), "", "root"},
			{"p", hex64('b')},
		},
	}
	cfg := referenceContextConfig{Enabled: false, MaxRefs: 3, MaxBlockChars: 2000, RelayFetch: false}
	block := buildRoomTurnBlock(context.Background(), msg, hex64('b'), cfg, nil, nil)
	if block == "" {
		t.Fatal("expected non-empty room turn block (meta block still renders)")
	}
	if !strings.Contains(block, "Nostr message metadata") {
		t.Errorf("block missing metadata header when ref disabled: %s", block)
	}
}