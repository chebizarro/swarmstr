package channels

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type rateLimitRoundTrip func(*http.Request) (*http.Response, error)

func (f rateLimitRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestTokenBucket_ExhaustionHonorsContext(t *testing.T) {
	bucket := NewTokenBucket(1, 0.01)
	if err := bucket.Wait(context.Background()); err != nil {
		t.Fatalf("first token should be available: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := bucket.Wait(ctx); err == nil {
		t.Fatal("expected canceled context after bucket exhaustion")
	}
}

func TestTokenBucket_NotifyRetryAfterBlocks(t *testing.T) {
	bucket := NewTokenBucket(10, 10)
	bucket.NotifyRetryAfter(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := bucket.Wait(ctx); err == nil {
		t.Fatal("expected retry-after block to wait and observe canceled context")
	}
}

func TestParseRetryAfter_SecondsAndHTTPDate(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "1.5")
	if got := ParseRetryAfter(h); got < 1400*time.Millisecond || got > 1600*time.Millisecond {
		t.Fatalf("expected about 1.5s, got %s", got)
	}
	h = http.Header{}
	h.Set("Retry-After", time.Now().Add(2*time.Second).UTC().Format(http.TimeFormat))
	if got := ParseRetryAfter(h); got <= 0 || got > 3*time.Second {
		t.Fatalf("expected positive date retry-after, got %s", got)
	}
}

func TestRESTScheduler_Retries429AndPreservesRouteIsolation(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: rateLimitRoundTrip(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			h := make(http.Header)
			h.Set("Retry-After", "0.001")
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: h, Body: io.NopCloser(strings.NewReader(`rate limited`)), Request: req}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`ok`)), Request: req}, nil
	})}
	s := NewRESTScheduler(10, 10)
	req, err := http.NewRequest(http.MethodGet, "https://discord.test/api/v10/channels/123/messages", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := s.Do(context.Background(), client, "GET /channels/:id/messages", req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if calls != 2 || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected retry then success, calls=%d status=%d", calls, resp.StatusCode)
	}
	if s.bucket("route-a") == s.bucket("route-b") {
		t.Fatal("expected separate buckets per route")
	}
}

func TestDiscordRESTRoute_NormalizesMajorParameters(t *testing.T) {
	got := DiscordRESTRoute(http.MethodDelete, "/api/v10/channels/123/messages/456")
	want := "DELETE /api/v10/channels/:id/messages/:id"
	if got != want {
		t.Fatalf("route mismatch: got %q want %q", got, want)
	}
	got = DiscordRESTRoute(http.MethodPost, "/api/v10/channels/123/messages")
	want = "POST /api/v10/channels/:id/messages"
	if got != want {
		t.Fatalf("post route mismatch: got %q want %q", got, want)
	}
}
