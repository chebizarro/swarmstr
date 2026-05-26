package channels

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TokenBucket is a simple concurrency-safe token bucket for channel REST calls.
type TokenBucket struct {
	mu              sync.Mutex
	capacity        int
	tokens          float64
	refillPerSecond float64
	last            time.Time
	blockedUntil    time.Time
}

func NewTokenBucket(capacity int, refillPerSecond float64) *TokenBucket {
	if capacity <= 0 {
		capacity = 1
	}
	if refillPerSecond <= 0 {
		refillPerSecond = 1
	}
	return &TokenBucket{capacity: capacity, tokens: float64(capacity), refillPerSecond: refillPerSecond, last: time.Now()}
}

func (b *TokenBucket) Wait(ctx context.Context) error {
	for {
		wait := b.reserveDelay()
		if wait <= 0 {
			return nil
		}
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		}
	}
}

func (b *TokenBucket) NotifyRetryAfter(delay time.Duration) {
	if delay <= 0 {
		return
	}
	b.mu.Lock()
	until := time.Now().Add(delay)
	if until.After(b.blockedUntil) {
		b.blockedUntil = until
	}
	b.mu.Unlock()
}

func (b *TokenBucket) reserveDelay() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if now.Before(b.blockedUntil) {
		return b.blockedUntil.Sub(now)
	}
	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens += elapsed * b.refillPerSecond
	if b.tokens > float64(b.capacity) {
		b.tokens = float64(b.capacity)
	}
	if b.tokens >= 1 {
		b.tokens--
		return 0
	}
	missing := 1 - b.tokens
	return time.Duration(missing/b.refillPerSecond*float64(time.Second)) + time.Millisecond
}

// RESTScheduler serializes and rate-limits HTTP calls by route bucket.
type RESTScheduler struct {
	mu              sync.Mutex
	buckets         map[string]*TokenBucket
	capacity        int
	refillPerSecond float64
}

func NewRESTScheduler(capacity int, refillPerSecond float64) *RESTScheduler {
	if capacity <= 0 {
		capacity = 5
	}
	if refillPerSecond <= 0 {
		refillPerSecond = 5
	}
	return &RESTScheduler{buckets: map[string]*TokenBucket{}, capacity: capacity, refillPerSecond: refillPerSecond}
}

func (s *RESTScheduler) bucket(route string) *TokenBucket {
	if route == "" {
		route = "global"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if b := s.buckets[route]; b != nil {
		return b
	}
	b := NewTokenBucket(s.capacity, s.refillPerSecond)
	s.buckets[route] = b
	return b
}

// Do waits for the route bucket, executes the request, and honors Retry-After
// hints by retrying 429 responses until ctx expires.
func (s *RESTScheduler) Do(ctx context.Context, client *http.Client, route string, req *http.Request) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	bucket := s.bucket(route)
	attempt := 0
	for {
		if err := bucket.Wait(ctx); err != nil {
			return nil, err
		}
		if attempt > 0 && req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req.Body = body
		}
		attempt++
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			if delay := ParseRetryAfter(resp.Header); delay > 0 {
				bucket.NotifyRetryAfter(delay)
			}
			return resp, nil
		}
		delay := ParseRetryAfter(resp.Header)
		if delay <= 0 {
			delay = time.Second
		}
		bucket.NotifyRetryAfter(delay)
		resp.Body.Close()
		t := time.NewTimer(delay)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		}
	}
}

func ParseRetryAfter(h http.Header) time.Duration {
	if h == nil {
		return 0
	}
	for _, key := range []string{"Retry-After", "X-RateLimit-Reset-After"} {
		raw := strings.TrimSpace(h.Get(key))
		if raw == "" {
			continue
		}
		if secs, err := strconv.ParseFloat(raw, 64); err == nil {
			return time.Duration(secs * float64(time.Second))
		}
		if when, err := http.ParseTime(raw); err == nil {
			if d := time.Until(when); d > 0 {
				return d
			}
		}
	}
	return 0
}

func DiscordRESTRoute(method, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return strings.ToUpper(method) + " /"
	}
	parts := strings.Split(path, "/")
	for i := 0; i < len(parts)-1; i++ {
		switch parts[i] {
		case "channels", "guilds", "webhooks":
			parts[i+1] = ":id"
		case "messages":
			if strings.ToUpper(method) != http.MethodPost {
				parts[i+1] = ":id"
			}
		}
	}
	return fmt.Sprintf("%s %s", strings.ToUpper(method), strings.Join(parts, "/"))
}
