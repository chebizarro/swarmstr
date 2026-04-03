package main

import (
	"context"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	nostruntime "metiq/internal/nostr/runtime"
)

type stubNostrControlHub struct {
	subscribeOpts  nostruntime.SubOpts
	subscribeErr   error
	publishResults []nostr.PublishResult
	publishHook    func(nostruntime.SubOpts)
	signErr        error
	unsubscribed   []string
}

func (s *stubNostrControlHub) Selector() *nostruntime.RelaySelector { return nil }
func (s *stubNostrControlHub) Pool() *nostr.Pool                    { return nil }
func (s *stubNostrControlHub) Close()                               {}

func (s *stubNostrControlHub) Subscribe(_ context.Context, opts nostruntime.SubOpts) (*nostruntime.ManagedSub, error) {
	s.subscribeOpts = opts
	if s.subscribeErr != nil {
		return nil, s.subscribeErr
	}
	return &nostruntime.ManagedSub{ID: opts.ID, Filter: opts.Filter, Relays: append([]string{}, opts.Relays...)}, nil
}

func (s *stubNostrControlHub) Unsubscribe(id string) bool {
	s.unsubscribed = append(s.unsubscribed, id)
	return true
}

func (s *stubNostrControlHub) Publish(_ context.Context, _ []string, _ nostr.Event) <-chan nostr.PublishResult {
	ch := make(chan nostr.PublishResult, max(len(s.publishResults), 1))
	for _, res := range s.publishResults {
		ch <- res
	}
	close(ch)
	if s.publishHook != nil {
		go s.publishHook(s.subscribeOpts)
	}
	return ch
}

func (s *stubNostrControlHub) SignEvent(_ context.Context, _ *nostr.Event) error {
	return s.signErr
}

func testNostrControlClient(t *testing.T, timeout time.Duration, hook func(nostruntime.SubOpts)) *nostrControlClient {
	t.Helper()
	targetPubHex := nostruntime.MustPublicKeyHex("1111111111111111111111111111111111111111111111111111111111111111")
	targetPub, err := nostruntime.ParsePubKey(targetPubHex)
	if err != nil {
		t.Fatalf("ParsePubKey: %v", err)
	}
	stubHub := &stubNostrControlHub{
		publishResults: []nostr.PublishResult{{RelayURL: "wss://request-relay"}},
		publishHook:    hook,
	}
	return &nostrControlClient{
		hub:            stubHub,
		callerPubKey:   nostruntime.MustPublicKeyHex("2222222222222222222222222222222222222222222222222222222222222222"),
		targetPub:      targetPub,
		targetPubKey:   targetPub.Hex(),
		fallbackRelays: []string{"wss://response-a", "wss://response-b"},
		timeout:        timeout,
	}
}

func TestNostrControlClientCallFailsEarlyWhenAllResponseRelaysClose(t *testing.T) {
	client := testNostrControlClient(t, 500*time.Millisecond, func(opts nostruntime.SubOpts) {
		opts.OnClosed(&nostr.Relay{URL: "wss://response-a"}, "policy", false)
		opts.OnClosed(&nostr.Relay{URL: "wss://response-b"}, "rate-limited", false)
	})

	start := time.Now()
	_, err := client.call("status.get", map[string]any{"verbose": true})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected closure error")
	}
	if !strings.Contains(err.Error(), "closed on all relays") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "wss://response-a=policy") || !strings.Contains(err.Error(), "wss://response-b=rate-limited") {
		t.Fatalf("expected relay close reasons in error, got %v", err)
	}
	if elapsed >= 250*time.Millisecond {
		t.Fatalf("expected early failure, took %v", elapsed)
	}
}

func TestNostrControlClientCallIgnoresHandledAuthCloseUntilTimeout(t *testing.T) {
	client := testNostrControlClient(t, 50*time.Millisecond, func(opts nostruntime.SubOpts) {
		opts.OnClosed(&nostr.Relay{URL: "wss://response-a"}, "auth-required", true)
	})

	_, err := client.call("status.get", map[string]any{"verbose": true})
	if err == nil {
		t.Fatal("expected timeout after handled auth close")
	}
	if !strings.Contains(err.Error(), "timed out waiting for nostr control response") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestNostrControlClientCallFailsEarlyWhenSubscriptionEndsSilently(t *testing.T) {
	client := testNostrControlClient(t, 500*time.Millisecond, func(opts nostruntime.SubOpts) {
		opts.OnEnd()
	})

	start := time.Now()
	_, err := client.call("status.get", map[string]any{"verbose": true})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected subscription-end error")
	}
	if !strings.Contains(err.Error(), "subscription ended before response") {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed >= 250*time.Millisecond {
		t.Fatalf("expected early failure, took %v", elapsed)
	}
}

func TestNostrControlClientCallSubscribesWithSanitizedResponseRelays(t *testing.T) {
	targetPubHex := nostruntime.MustPublicKeyHex("1111111111111111111111111111111111111111111111111111111111111111")
	targetPub, err := nostruntime.ParsePubKey(targetPubHex)
	if err != nil {
		t.Fatalf("ParsePubKey: %v", err)
	}
	stubHub := &stubNostrControlHub{
		publishResults: []nostr.PublishResult{{RelayURL: "wss://request-relay"}},
		publishHook: func(opts nostruntime.SubOpts) {
			opts.OnEnd()
		},
	}
	client := &nostrControlClient{
		hub:            stubHub,
		callerPubKey:   nostruntime.MustPublicKeyHex("2222222222222222222222222222222222222222222222222222222222222222"),
		targetPub:      targetPub,
		targetPubKey:   targetPub.Hex(),
		fallbackRelays: []string{"  wss://response-a  ", "wss://response-a", "wss://response-b", "   "},
		timeout:        500 * time.Millisecond,
	}

	_, err = client.call("status.get", map[string]any{"verbose": true})
	if err == nil {
		t.Fatal("expected subscription-end error")
	}
	if !strings.Contains(err.Error(), "subscription ended before response") {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"wss://response-a", "wss://response-b"}
	if got := stubHub.subscribeOpts.Relays; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("expected sanitized response relays %v, got %v", want, got)
	}
}
