// Package nip46 implements the current NIP-46 remote-signer protocol.
package nip46

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	nostr "fiatjaf.com/nostr"
)

const Kind nostr.Kind = 24133

const (
	MethodConnect      = "connect"
	MethodSignEvent    = "sign_event"
	MethodPing         = "ping"
	MethodGetPublicKey = "get_public_key"
	MethodNIP04Encrypt = "nip04_encrypt"
	MethodNIP04Decrypt = "nip04_decrypt"
	MethodNIP44Encrypt = "nip44_encrypt"
	MethodNIP44Decrypt = "nip44_decrypt"
	MethodSwitchRelays = "switch_relays"
	MethodLogout       = "logout"
)

type Request struct {
	ID     string   `json:"id"`
	Method string   `json:"method"`
	Params []string `json:"params"`
}

type Response struct {
	ID     string `json:"id"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type ClientMetadata struct {
	Name  string `json:"name,omitempty"`
	URL   string `json:"url,omitempty"`
	Image string `json:"image,omitempty"`
}

type BunkerToken struct {
	RemoteSigner nostr.PubKey
	Relays       []string
	Secret       string
}

type NostrConnectToken struct {
	ClientPubKey nostr.PubKey
	Relays       []string
	Secret       string
	Permissions  PermissionSet
	Metadata     ClientMetadata
}

func ParseBunkerURL(raw string) (BunkerToken, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return BunkerToken{}, fmt.Errorf("parse bunker URL: %w", err)
	}
	if strings.ToLower(u.Scheme) != "bunker" {
		return BunkerToken{}, fmt.Errorf("NIP-46 bunker URL requires bunker:// scheme")
	}
	pk, err := nostr.PubKeyFromHex(u.Host)
	if err != nil {
		return BunkerToken{}, fmt.Errorf("invalid remote-signer pubkey: %w", err)
	}
	relays, err := normalizeRelayURLs(u.Query()["relay"])
	if err != nil {
		return BunkerToken{}, err
	}
	if len(relays) == 0 {
		return BunkerToken{}, fmt.Errorf("bunker URL requires at least one relay")
	}
	return BunkerToken{RemoteSigner: pk, Relays: relays, Secret: u.Query().Get("secret")}, nil
}

func ParseNostrConnectURL(raw string) (NostrConnectToken, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return NostrConnectToken{}, fmt.Errorf("parse nostrconnect URL: %w", err)
	}
	if strings.ToLower(u.Scheme) != "nostrconnect" {
		return NostrConnectToken{}, fmt.Errorf("NIP-46 client URL requires nostrconnect:// scheme")
	}
	pk, err := nostr.PubKeyFromHex(u.Host)
	if err != nil {
		return NostrConnectToken{}, fmt.Errorf("invalid client pubkey: %w", err)
	}
	relays, err := normalizeRelayURLs(u.Query()["relay"])
	if err != nil {
		return NostrConnectToken{}, err
	}
	if len(relays) == 0 {
		return NostrConnectToken{}, fmt.Errorf("nostrconnect URL requires at least one relay")
	}
	secret := strings.TrimSpace(u.Query().Get("secret"))
	if secret == "" {
		return NostrConnectToken{}, fmt.Errorf("nostrconnect URL requires a secret")
	}
	perms, err := ParsePermissions(u.Query().Get("perms"))
	if err != nil {
		return NostrConnectToken{}, err
	}
	return NostrConnectToken{
		ClientPubKey: pk, Relays: relays, Secret: secret, Permissions: perms,
		Metadata: ClientMetadata{Name: u.Query().Get("name"), URL: u.Query().Get("url"), Image: u.Query().Get("image")},
	}, nil
}

func GenerateNostrConnectURL(clientKey nostr.SecretKey, relays []string, permissions PermissionSet, metadata ClientMetadata) (string, string, error) {
	relays, err := normalizeRelayURLs(relays)
	if err != nil || len(relays) == 0 {
		if err == nil {
			err = fmt.Errorf("at least one relay is required")
		}
		return "", "", err
	}
	secretBytes := make([]byte, 16)
	if _, err := rand.Read(secretBytes); err != nil {
		return "", "", fmt.Errorf("generate connection secret: %w", err)
	}
	secret := hex.EncodeToString(secretBytes)
	q := url.Values{}
	for _, relay := range relays {
		q.Add("relay", relay)
	}
	q.Set("secret", secret)
	if p := permissions.String(); p != "" {
		q.Set("perms", p)
	}
	if metadata.Name != "" {
		q.Set("name", metadata.Name)
	}
	if metadata.URL != "" {
		q.Set("url", metadata.URL)
	}
	if metadata.Image != "" {
		q.Set("image", metadata.Image)
	}
	return "nostrconnect://" + clientKey.Public().Hex() + "?" + q.Encode(), secret, nil
}

func normalizeRelayURLs(in []string) ([]string, error) {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || (u.Scheme != "wss" && u.Scheme != "ws") || u.Host == "" {
			return nil, fmt.Errorf("invalid relay URL %q", raw)
		}
		u.Fragment = ""
		normalized := strings.TrimRight(u.String(), "/")
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}

// PermissionSet contains method-wide grants and optional sign_event kind grants.
type PermissionSet struct {
	methods   map[string]struct{}
	signKinds map[nostr.Kind]struct{}
}

func ParsePermissions(raw string) (PermissionSet, error) {
	p := PermissionSet{methods: map[string]struct{}{}, signKinds: map[nostr.Kind]struct{}{}}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.Split(item, ":")
		method := parts[0]
		if !knownMethod(method) || method == MethodConnect || method == MethodLogout {
			return PermissionSet{}, fmt.Errorf("invalid NIP-46 permission %q", item)
		}
		if len(parts) == 1 {
			p.methods[method] = struct{}{}
			continue
		}
		if len(parts) != 2 || method != MethodSignEvent {
			return PermissionSet{}, fmt.Errorf("invalid NIP-46 permission parameter %q", item)
		}
		kind, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || kind < 0 || kind > int64(^uint16(0)) {
			return PermissionSet{}, fmt.Errorf("invalid sign_event kind in permission %q", item)
		}
		p.signKinds[nostr.Kind(kind)] = struct{}{}
	}
	return p, nil
}

func (p PermissionSet) Allows(method string, kind nostr.Kind) bool {
	if method == MethodConnect || method == MethodLogout {
		return true
	}
	if _, ok := p.methods[method]; ok {
		return true
	}
	if method == MethodSignEvent {
		_, ok := p.signKinds[kind]
		return ok
	}
	return false
}

func (p PermissionSet) String() string {
	items := make([]string, 0, len(p.methods)+len(p.signKinds))
	for method := range p.methods {
		items = append(items, method)
	}
	for kind := range p.signKinds {
		items = append(items, MethodSignEvent+":"+strconv.Itoa(int(kind)))
	}
	sort.Strings(items)
	return strings.Join(items, ",")
}

func (p PermissionSet) Empty() bool { return len(p.methods) == 0 && len(p.signKinds) == 0 }

func knownMethod(method string) bool {
	switch method {
	case MethodConnect, MethodSignEvent, MethodPing, MethodGetPublicKey, MethodNIP04Encrypt,
		MethodNIP04Decrypt, MethodNIP44Encrypt, MethodNIP44Decrypt, MethodSwitchRelays, MethodLogout:
		return true
	default:
		return false
	}
}

func encodeJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
