package nip05

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	nostr "fiatjaf.com/nostr"
	"golang.org/x/net/idna"
)

var localPartRE = regexp.MustCompile(`^[a-z0-9_.-]+$`)

type Document struct {
	Names  map[string]string   `json:"names"`
	Relays map[string][]string `json:"relays,omitempty"`
	NIP46  *struct {
		Relays          []string `json:"relays"`
		NostrConnectURL string   `json:"nostrconnect_url"`
	} `json:"nip46,omitempty"`
}

type Result struct {
	Identifier string
	Name       string
	Domain     string
	PubKey     nostr.PubKey
	Relays     []string
	Document   Document
}

type Resolver struct {
	Client   *http.Client
	Endpoint func(domain, name string) (*url.URL, error)
}

func ParseIdentifier(identifier string) (name, domain string, err error) {
	parts := strings.Split(strings.TrimSpace(identifier), "@")
	if len(parts) != 2 || !localPartRE.MatchString(parts[0]) {
		return "", "", fmt.Errorf("invalid NIP-05 identifier")
	}
	ascii, err := idna.Lookup.ToASCII(strings.ToLower(parts[1]))
	if err != nil || ascii == "" || strings.ContainsAny(ascii, "/:@") || !strings.Contains(ascii, ".") {
		return "", "", fmt.Errorf("invalid NIP-05 domain")
	}
	return parts[0], ascii, nil
}

func (r Resolver) Resolve(ctx context.Context, identifier string) (Result, error) {
	name, domain, err := ParseIdentifier(identifier)
	if err != nil {
		return Result{}, err
	}
	endpoint := r.Endpoint
	if endpoint == nil {
		endpoint = func(domain, name string) (*url.URL, error) {
			return url.Parse("https://" + domain + "/.well-known/nostr.json?name=" + url.QueryEscape(name))
		}
	}
	u, err := endpoint(domain, name)
	if err != nil || u == nil || u.Scheme != "https" {
		return Result{}, fmt.Errorf("NIP-05 endpoint must use HTTPS")
	}
	client := r.Client
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	clone.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	resp, err := clone.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("fetch NIP-05 document: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("NIP-05 endpoint returned %s", resp.Status)
	}
	var doc Document
	dec := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := dec.Decode(&doc); err != nil {
		return Result{}, fmt.Errorf("decode NIP-05 document: %w", err)
	}
	raw, ok := doc.Names[name]
	if !ok || raw != strings.ToLower(raw) || len(raw) != 64 {
		return Result{}, fmt.Errorf("NIP-05 name not found or pubkey is not lowercase hex")
	}
	pk, err := nostr.PubKeyFromHex(raw)
	if err != nil {
		return Result{}, fmt.Errorf("invalid NIP-05 pubkey: %w", err)
	}
	relays := make([]string, 0)
	for _, relay := range doc.Relays[raw] {
		ru, err := url.Parse(relay)
		if err != nil || (ru.Scheme != "wss" && ru.Scheme != "ws") || ru.Host == "" {
			return Result{}, fmt.Errorf("invalid NIP-05 relay URL %q", relay)
		}
		relays = append(relays, relay)
	}
	return Result{Identifier: name + "@" + domain, Name: name, Domain: domain, PubKey: pk, Relays: relays, Document: doc}, nil
}

func (r Resolver) Verify(ctx context.Context, identifier string, expected nostr.PubKey) (Result, error) {
	result, err := r.Resolve(ctx, identifier)
	if err != nil {
		return Result{}, err
	}
	if result.PubKey != expected {
		return Result{}, fmt.Errorf("NIP-05 pubkey mismatch")
	}
	return result, nil
}

func IdentifierFromMetadata(event nostr.Event) (string, error) {
	if event.Kind != 0 || !event.CheckID() || !event.VerifySignature() {
		return "", fmt.Errorf("invalid kind-0 metadata event")
	}
	var metadata struct {
		NIP05 string `json:"nip05"`
	}
	if json.Unmarshal([]byte(event.Content), &metadata) != nil || metadata.NIP05 == "" {
		return "", fmt.Errorf("metadata has no NIP-05 identifier")
	}
	return metadata.NIP05, nil
}
