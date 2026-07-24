package blossom

import (
	"context"
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

func discoverySigner() nostr.Keyer {
	secret := nostr.Generate()
	return keyer.NewPlainKeySigner([32]byte(secret))
}

func TestBuildServerListEventCurrentWireFormat(t *testing.T) {
	signer := discoverySigner()
	event, err := BuildServerListEvent(context.Background(), signer, []string{
		"https://blossom.self.hosted/",
		"https://cdn.blossom.cloud",
		"https://cdn.blossom.cloud",
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != KindServerList || event.Content != "" {
		t.Fatalf("kind/content = %d/%q", event.Kind, event.Content)
	}
	want := nostr.Tags{
		{"server", "https://blossom.self.hosted"},
		{"server", "https://cdn.blossom.cloud"},
	}
	if len(event.Tags) != len(want) {
		t.Fatalf("tags = %v", event.Tags)
	}
	for i := range want {
		if strings.Join(event.Tags[i], "\x00") != strings.Join(want[i], "\x00") {
			t.Fatalf("tag[%d] = %v, want %v", i, event.Tags[i], want[i])
		}
	}
}

func TestParseServerListEventIgnoresNonStandardTags(t *testing.T) {
	signer := discoverySigner()
	event, err := BuildServerListEvent(context.Background(), signer, []string{"https://one.example"})
	if err != nil {
		t.Fatal(err)
	}
	event.Tags = append(event.Tags, nostr.Tag{"r", "https://legacy.invalid"})
	if err := signer.SignEvent(context.Background(), &event); err != nil {
		t.Fatal(err)
	}
	list, err := ParseServerListEvent(event, event.PubKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Servers) != 1 || list.Servers[0] != "https://one.example" {
		t.Fatalf("servers = %v", list.Servers)
	}
}

type captureServerListTransport struct {
	sub ServerListSubscription
}

func (t *captureServerListTransport) SubscribeServerList(_ context.Context, sub ServerListSubscription) (func(), error) {
	t.sub = sub
	return func() {}, nil
}

func TestDiscoverServerListUsesScopedFilterAndLatestReplaceableEvent(t *testing.T) {
	signer := discoverySigner()
	pubkey, _ := signer.GetPublicKey(context.Background())
	transport := &captureServerListTransport{}
	var received []ServerList
	stop, err := DiscoverServerList(
		context.Background(),
		transport,
		[]string{"wss://relay.example"},
		pubkey,
		func(list ServerList) { received = append(received, list) },
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if len(transport.sub.Filter.Kinds) != 1 || transport.sub.Filter.Kinds[0] != KindServerList ||
		len(transport.sub.Filter.Authors) != 1 || transport.sub.Filter.Authors[0] != pubkey {
		t.Fatalf("filter is not kind+author scoped: %#v", transport.sub.Filter)
	}
	newer, _ := BuildServerListEvent(context.Background(), signer, []string{"https://new.example"})
	newer.CreatedAt = nostr.Now()
	signer.SignEvent(context.Background(), &newer)
	older, _ := BuildServerListEvent(context.Background(), signer, []string{"https://old.example"})
	older.CreatedAt = newer.CreatedAt - 1
	signer.SignEvent(context.Background(), &older)
	transport.sub.OnEvent(newer)
	transport.sub.OnEvent(older)
	if len(received) != 1 || received[0].Servers[0] != "https://new.example" {
		t.Fatalf("received = %#v", received)
	}
}

func TestFallbackURLsRetainHashAndExtension(t *testing.T) {
	hash := strings.Repeat("a1", 32)
	got, err := FallbackURLs(
		"https://dead.example/media/"+hash+".png",
		[]string{"https://blossom.one/", "https://blossom.two"},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://blossom.one/" + hash + ".png",
		"https://blossom.two/" + hash + ".png",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("fallbacks = %v, want %v", got, want)
	}
}

func TestFallbackURLsRejectNonHashMedia(t *testing.T) {
	if _, err := FallbackURLs("https://example.com/image.png", []string{"https://blossom.one"}); err == nil {
		t.Fatal("expected non-hash URL to be rejected")
	}
}
