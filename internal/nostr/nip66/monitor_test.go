package nip66

import (
	"context"
	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"testing"
	"time"
)

func TestRelayStatusAnnouncementAndConsensus(t *testing.T) {
	ctx := context.Background()
	s1 := keyer.NewPlainKeySigner(nostr.Generate())
	s2 := keyer.NewPlainKeySigner(nostr.Generate())
	status := RelayStatus{Relay: "WSS://Relay.Example:443", RTTOpen: 100 * time.Millisecond, RTTRead: 80 * time.Millisecond, Network: "clearnet", NIPs: []string{"42", "11"}, Requirements: []string{"auth", "!payment"}, NIP11: []byte(`{"name":"relay"}`)}
	e1, err := BuildRelayStatus(ctx, s1, status)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseRelayStatus(e1)
	if err != nil || parsed.Relay != "wss://relay.example/" || parsed.RTTOpen != 100*time.Millisecond {
		t.Fatalf("parsed %#v %v", parsed, err)
	}
	status.RTTOpen = 120 * time.Millisecond
	e2, _ := BuildRelayStatus(ctx, s2, status)
	consensus := Consensus([]nostr.Event{e1, e2}, nil, 2)
	c, ok := consensus["wss://relay.example/"]
	if !ok || c.Monitors != 2 || c.MedianOpen != 120*time.Millisecond {
		t.Fatalf("consensus %#v", consensus)
	}
	a, err := BuildMonitorAnnouncement(ctx, s1, time.Hour, map[string]time.Duration{"open": 5 * time.Second}, []string{"open", "nip11"}, "ww8p")
	if err != nil {
		t.Fatal(err)
	}
	announcement, err := ParseMonitorAnnouncement(a)
	if err != nil || announcement.Frequency != time.Hour || announcement.Timeouts["open"] != 5*time.Second {
		t.Fatal(announcement, err)
	}
}
func TestConsensusRequiresMultipleSources(t *testing.T) {
	s := keyer.NewPlainKeySigner(nostr.Generate())
	e, _ := BuildRelayStatus(context.Background(), s, RelayStatus{Relay: "wss://one.example", RTTOpen: time.Second})
	if len(Consensus([]nostr.Event{e}, nil, 1)) != 0 {
		t.Fatal("trusted one monitor")
	}
	f := Filter([]nostr.PubKey{e.PubKey}, 10)
	if f.Kinds[0] != KindRelayDiscovery || f.Since != 10 {
		t.Fatal(f)
	}
}
