package publish

import (
	"context"
	"errors"
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"
)

type fakePublisher struct {
	results []nostr.PublishResult
}

func (f fakePublisher) PublishMany(context.Context, []string, nostr.Event) chan nostr.PublishResult {
	ch := make(chan nostr.PublishResult, len(f.results))
	for _, result := range f.results {
		ch <- result
	}
	close(ch)
	return ch
}

func TestPublishToAnyReturnsErrorWhenAllRelaysReject(t *testing.T) {
	publisher := fakePublisher{results: []nostr.PublishResult{
		{RelayURL: "wss://relay.one", Error: errors.New("msg: blocked: policy")},
		{RelayURL: "wss://relay.two", Error: errors.New("msg: rate-limited")},
	}}

	report, err := PublishToAny(context.Background(), publisher, []string{"wss://relay.one", "wss://relay.two"}, nostr.Event{})
	if err == nil {
		t.Fatal("expected aggregate publish error")
	}
	if report.Accepted != 0 {
		t.Fatalf("accepted = %d, want 0", report.Accepted)
	}
	msg := err.Error()
	if !strings.Contains(msg, "accepted=false") || !strings.Contains(msg, "blocked: policy") || !strings.Contains(msg, "rate-limited") {
		t.Fatalf("error did not include OK rejection details: %v", err)
	}
}

func TestPublishToAnySucceedsWhenAnyRelayAccepts(t *testing.T) {
	publisher := fakePublisher{results: []nostr.PublishResult{
		{RelayURL: "wss://relay.one", Error: errors.New("msg: blocked")},
		{RelayURL: "wss://relay.two"},
	}}

	report, err := PublishToAny(context.Background(), publisher, []string{"wss://relay.one", "wss://relay.two"}, nostr.Event{})
	if err != nil {
		t.Fatalf("PublishToAny returned error: %v", err)
	}
	if report.Accepted != 1 {
		t.Fatalf("accepted = %d, want 1", report.Accepted)
	}
	if len(report.Results) != 2 || report.Results[1].Accepted != true || report.Results[1].Message == "" {
		t.Fatalf("unexpected report: %+v", report)
	}
}
