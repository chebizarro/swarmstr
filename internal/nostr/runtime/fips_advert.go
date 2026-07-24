package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"
)

const (
	FIPSOverlayAdvertKind       = 37195
	FIPSOverlayAdvertIdentifier = "fips-overlay-v1"
	FIPSOverlayAdvertVersion    = uint32(1)
	FIPSOverlayProtocolVersion  = "1"
	DefaultFIPSOverlayAdvertTTL = time.Hour
)

type FIPSOverlayTransportKind string

const (
	FIPSOverlayTransportUDP FIPSOverlayTransportKind = "udp"
	FIPSOverlayTransportTCP FIPSOverlayTransportKind = "tcp"
	FIPSOverlayTransportTor FIPSOverlayTransportKind = "tor"
)

type FIPSOverlayEndpointAdvert struct {
	Transport FIPSOverlayTransportKind `json:"transport"`
	Addr      string                   `json:"addr"`
}

type FIPSOverlayAdvert struct {
	Identifier   string                      `json:"identifier"`
	Version      uint32                      `json:"version"`
	Endpoints    []FIPSOverlayEndpointAdvert `json:"endpoints"`
	SignalRelays []string                    `json:"signalRelays,omitempty"`
	STUNServers  []string                    `json:"stunServers,omitempty"`
}

// FIPSAdvertAnnouncement keeps kind-37195 event metadata separate from the
// advert body. Kind 30317 and kind 37195 are independently replaceable streams.
type FIPSAdvertAnnouncement struct {
	PubKey    string
	Protocol  string
	Advert    FIPSOverlayAdvert
	EventID   string
	CreatedAt int64
	ExpiresAt int64
}

func defaultFIPSProtocol(protocol string) string {
	protocol = strings.TrimSpace(protocol)
	if protocol == "" {
		return FIPSOverlayAdvertIdentifier
	}
	return protocol
}

func cloneFIPSOverlayAdvert(in FIPSOverlayAdvert) FIPSOverlayAdvert {
	in.Endpoints = append([]FIPSOverlayEndpointAdvert{}, in.Endpoints...)
	in.SignalRelays = append([]string{}, in.SignalRelays...)
	in.STUNServers = append([]string{}, in.STUNServers...)
	return in
}

func orderedUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ValidateFIPSOverlayAdvert applies the same v1 invariants as the FIPS daemon.
// Unusable direct endpoints are discarded; an advert is rejected if none remain.
func ValidateFIPSOverlayAdvert(in FIPSOverlayAdvert) (FIPSOverlayAdvert, error) {
	if strings.TrimSpace(in.Identifier) != FIPSOverlayAdvertIdentifier {
		return FIPSOverlayAdvert{}, fmt.Errorf("unsupported identifier %q", in.Identifier)
	}
	if in.Version != FIPSOverlayAdvertVersion {
		return FIPSOverlayAdvert{}, fmt.Errorf("unsupported version %d", in.Version)
	}
	out := FIPSOverlayAdvert{
		Identifier: FIPSOverlayAdvertIdentifier,
		Version:    FIPSOverlayAdvertVersion,
	}
	seen := make(map[string]struct{}, len(in.Endpoints))
	hasNAT := false
	for _, endpoint := range in.Endpoints {
		transport := FIPSOverlayTransportKind(strings.ToLower(strings.TrimSpace(string(endpoint.Transport))))
		addr := strings.TrimSpace(endpoint.Addr)
		switch transport {
		case FIPSOverlayTransportUDP, FIPSOverlayTransportTCP, FIPSOverlayTransportTor:
		default:
			return FIPSOverlayAdvert{}, fmt.Errorf("unsupported endpoint transport %q", endpoint.Transport)
		}
		if strings.EqualFold(addr, "nat") {
			if transport != FIPSOverlayTransportUDP {
				continue
			}
			addr = "nat"
			hasNAT = true
		} else if !fipsEndpointPubliclyUsable(transport, addr) {
			continue
		}
		key := string(transport) + "\x00" + addr
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out.Endpoints = append(out.Endpoints, FIPSOverlayEndpointAdvert{Transport: transport, Addr: addr})
	}
	if len(out.Endpoints) == 0 {
		return FIPSOverlayAdvert{}, fmt.Errorf("missing publicly routable endpoints")
	}
	if hasNAT {
		out.SignalRelays = orderedUniqueStrings(in.SignalRelays)
		out.STUNServers = orderedUniqueStrings(in.STUNServers)
		if len(out.SignalRelays) == 0 {
			return FIPSOverlayAdvert{}, fmt.Errorf("udp:nat endpoint requires signalRelays")
		}
		if len(out.STUNServers) == 0 {
			return FIPSOverlayAdvert{}, fmt.Errorf("udp:nat endpoint requires stunServers")
		}
	}
	return out, nil
}

func fipsEndpointPubliclyUsable(transport FIPSOverlayTransportKind, addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" || strings.EqualFold(addr, "nat") {
		return false
	}
	if transport == FIPSOverlayTransportTor {
		return true
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		idx := strings.LastIndex(addr, ":")
		if idx <= 0 || idx == len(addr)-1 {
			return false
		}
		host, port = addr[:idx], addr[idx+1:]
	}
	host = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(host), "["), "]"))
	parsedPort, err := strconv.ParseUint(strings.TrimSpace(port), 10, 16)
	if err != nil || parsedPort == 0 || host == "" || strings.EqualFold(host, "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return true
	}
	return !fipsUnroutableAdvertIP(ip)
}

func fipsUnroutableAdvertIP(ip net.IP) bool {
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	if v4[0] == 255 && v4[1] == 255 && v4[2] == 255 && v4[3] == 255 {
		return true
	}
	if v4[0] == 100 && v4[1]&0xc0 == 64 {
		return true
	}
	return (v4[0] == 192 && v4[1] == 0 && v4[2] == 2) ||
		(v4[0] == 198 && v4[1] == 51 && v4[2] == 100) ||
		(v4[0] == 203 && v4[1] == 0 && v4[2] == 113)
}

func BuildFIPSAdvertContent(advert FIPSOverlayAdvert) (string, error) {
	normalized, err := ValidateFIPSOverlayAdvert(advert)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("marshal advert: %w", err)
	}
	return string(raw), nil
}

func BuildFIPSAdvertTags(protocol string, expiresAt int64) (nostr.Tags, error) {
	protocol = defaultFIPSProtocol(protocol)
	if expiresAt <= 0 {
		return nil, fmt.Errorf("expiration must be positive")
	}
	return nostr.Tags{
		{"d", FIPSOverlayAdvertIdentifier},
		{"protocol", protocol},
		{"version", FIPSOverlayProtocolVersion},
		{"expiration", strconv.FormatInt(expiresAt, 10)},
	}, nil
}

func uniqueFIPSAdvertTag(tags nostr.Tags, name string) (string, error) {
	var value string
	count := 0
	for _, tag := range tags {
		if len(tag) < 2 || strings.TrimSpace(tag[0]) != name {
			continue
		}
		count++
		value = strings.TrimSpace(tag[1])
	}
	if count != 1 || value == "" {
		return "", fmt.Errorf("expected exactly one non-empty %s tag", name)
	}
	return value, nil
}

func ParseFIPSAdvertEvent(ev *nostr.Event, expectedProtocol string, now time.Time) (FIPSAdvertAnnouncement, error) {
	if ev == nil {
		return FIPSAdvertAnnouncement{}, fmt.Errorf("fips advert event is nil")
	}
	if ev.Kind != nostr.Kind(FIPSOverlayAdvertKind) {
		return FIPSAdvertAnnouncement{}, fmt.Errorf("unexpected fips advert kind %d", ev.Kind)
	}
	dTag, err := uniqueFIPSAdvertTag(ev.Tags, "d")
	if err != nil || dTag != FIPSOverlayAdvertIdentifier {
		return FIPSAdvertAnnouncement{}, fmt.Errorf("invalid d tag: %q", dTag)
	}
	protocol, err := uniqueFIPSAdvertTag(ev.Tags, "protocol")
	if err != nil {
		return FIPSAdvertAnnouncement{}, err
	}
	if protocol != defaultFIPSProtocol(expectedProtocol) {
		return FIPSAdvertAnnouncement{}, fmt.Errorf("unsupported protocol %q", protocol)
	}
	version, err := uniqueFIPSAdvertTag(ev.Tags, "version")
	if err != nil || version != FIPSOverlayProtocolVersion {
		return FIPSAdvertAnnouncement{}, fmt.Errorf("unsupported protocol version %q", version)
	}
	expiration, err := uniqueFIPSAdvertTag(ev.Tags, "expiration")
	if err != nil {
		return FIPSAdvertAnnouncement{}, err
	}
	expiresAt, err := strconv.ParseInt(expiration, 10, 64)
	if err != nil || expiresAt <= 0 {
		return FIPSAdvertAnnouncement{}, fmt.Errorf("invalid expiration %q", expiration)
	}
	decoder := json.NewDecoder(strings.NewReader(ev.Content))
	var advert FIPSOverlayAdvert
	if err := decoder.Decode(&advert); err != nil {
		return FIPSAdvertAnnouncement{}, fmt.Errorf("decode advert: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return FIPSAdvertAnnouncement{}, fmt.Errorf("decode advert: trailing JSON value")
		}
		return FIPSAdvertAnnouncement{}, fmt.Errorf("decode advert trailing data: %w", err)
	}
	advert, err = ValidateFIPSOverlayAdvert(advert)
	if err != nil {
		return FIPSAdvertAnnouncement{}, err
	}
	if advert.Identifier != dTag {
		return FIPSAdvertAnnouncement{}, fmt.Errorf("content identifier does not match d tag")
	}
	return FIPSAdvertAnnouncement{
		PubKey:    strings.ToLower(strings.TrimSpace(ev.PubKey.Hex())),
		Protocol:  protocol,
		Advert:    advert,
		EventID:   strings.ToLower(strings.TrimSpace(ev.ID.Hex())),
		CreatedAt: int64(ev.CreatedAt),
		ExpiresAt: expiresAt,
	}, nil
}

func fipsAdvertValidationFailure(ev nostr.Event, allowedAuthors map[string]struct{}, now time.Time) string {
	if ev.Kind != nostr.Kind(FIPSOverlayAdvertKind) {
		return fmt.Sprintf("unexpected_kind:%d", ev.Kind)
	}
	if _, ok := allowedAuthors[ev.PubKey.Hex()]; !ok {
		return "unexpected_author"
	}
	if !ev.CheckID() {
		return "invalid_id"
	}
	if !ev.VerifySignature() {
		return "invalid_signature"
	}
	if timestampTooFarFuture(int64(ev.CreatedAt), now, inboundEventMaxFutureSkew) {
		return "created_at_future"
	}
	return ""
}

func PublishFIPSAdvert(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, protocol string, ttl time.Duration, advert FIPSOverlayAdvert) (string, error) {
	if pool == nil {
		return "", fmt.Errorf("publish fips advert: pool is required")
	}
	if keyer == nil {
		return "", fmt.Errorf("publish fips advert: keyer is required")
	}
	relays = normalizeRelayURLs(relays)
	if len(relays) == 0 {
		return "", fmt.Errorf("publish fips advert: at least one relay is required")
	}
	if ttl <= 0 {
		return "", fmt.Errorf("publish fips advert: ttl must be positive")
	}
	content, err := BuildFIPSAdvertContent(advert)
	if err != nil {
		return "", fmt.Errorf("publish fips advert: %w", err)
	}
	createdAt := time.Now().Unix()
	ttlSeconds := int64((ttl + time.Second - 1) / time.Second)
	if ttlSeconds < 1 {
		ttlSeconds = 1
	}
	tags, err := BuildFIPSAdvertTags(protocol, createdAt+ttlSeconds)
	if err != nil {
		return "", fmt.Errorf("publish fips advert: %w", err)
	}
	evt := nostr.Event{
		Kind:      nostr.Kind(FIPSOverlayAdvertKind),
		CreatedAt: nostr.Timestamp(createdAt),
		Tags:      tags,
		Content:   content,
	}
	if err := keyer.SignEvent(ctx, &evt); err != nil {
		return "", fmt.Errorf("publish fips advert: sign event: %w", err)
	}
	published := false
	var lastErr error
	for result := range pool.PublishMany(ctx, relays, evt) {
		if result.Error == nil {
			published = true
			continue
		}
		lastErr = result.Error
	}
	if !published {
		if lastErr == nil {
			lastErr = fmt.Errorf("no relays accepted the event")
		}
		return "", fmt.Errorf("publish fips advert: %w", lastErr)
	}
	return evt.ID.Hex(), nil
}
