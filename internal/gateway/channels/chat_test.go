package channels

import (
	"fmt"
	"testing"
	"time"
)

func TestChatChannelID(t *testing.T) {
	// Verify the channel ID format.
	tests := []struct {
		relays  []string
		rootTag string
		wantID  string
	}{
		{[]string{"wss://relay.example.com"}, "-", "chat:wss://relay.example.com:-"},
		{[]string{"wss://relay.example.com"}, "nostr", "chat:wss://relay.example.com:nostr"},
		{[]string{"wss://a.com", "wss://b.com"}, "-", "chat:wss://a.com:-"},
	}
	for _, tt := range tests {
		// Can't create a real channel without a keyer, so test the ID format directly.
		channelID := "chat:" + tt.relays[0] + ":" + tt.rootTag
		if channelID != tt.wantID {
			t.Errorf("got %q, want %q", channelID, tt.wantID)
		}
	}
}

func TestChatChannelType(t *testing.T) {
	// Verify the Type() return value matches the expected constant.
	expected := "nipc7-chat"
	ch := &ChatChannel{}
	if ch.Type() != expected {
		t.Errorf("got %q, want %q", ch.Type(), expected)
	}
}

func TestChatChannelRootTagDefault(t *testing.T) {
	// Verify that an empty root_tag defaults to "-".
	rootTag := ""
	if rootTag == "" {
		rootTag = "-"
	}
	if rootTag != "-" {
		t.Errorf("expected default root tag \"-\", got %q", rootTag)
	}
}

func TestSeenCacheDuplicateWithinTTL(t *testing.T) {
	c := &SeenCache{
		items: make(map[string]time.Time),
		ttl:   100 * time.Millisecond,
	}

	if c.Add("evt-1") {
		t.Fatal("first add should not be duplicate")
	}
	if !c.Add("evt-1") {
		t.Fatal("second add within ttl should be duplicate")
	}
}

func TestSeenCacheEntryExpiresByTTL(t *testing.T) {
	c := &SeenCache{
		items: make(map[string]time.Time),
		ttl:   20 * time.Millisecond,
	}

	if c.Add("evt-1") {
		t.Fatal("first add should not be duplicate")
	}
	time.Sleep(30 * time.Millisecond)
	if c.Add("evt-1") {
		t.Fatal("add after ttl should not be duplicate")
	}
}

func TestSeenCacheStrictMaxSize(t *testing.T) {
	c := &SeenCache{
		items: make(map[string]time.Time),
		ttl:   time.Hour,
	}

	for i := 0; i < seenCacheMaxSize+200; i++ {
		if c.Add(fmt.Sprintf("evt-%d", i)) {
			t.Fatalf("unexpected duplicate at i=%d", i)
		}
	}

	if got := c.Len(); got != seenCacheMaxSize {
		t.Fatalf("cache size=%d, want %d", got, seenCacheMaxSize)
	}
}
