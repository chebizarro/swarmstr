// Package nip61 implements NIP-61 Nutzaps.
package nip61

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

const (
	KindNutzapInfo   = 10019
	KindNutzap       = 9321
	KindToken        = 7375
	KindTokenHistory = 7376
)

const maxFutureSkew = 10 * time.Minute

var ErrAlreadyRedeemed = errors.New("nip61: nutzap already marked redeemed")

type NutzapInfo struct {
	Mints      []MintInfo `json:"mints,omitempty"`
	Relays     []string   `json:"relays,omitempty"`
	P2PKPubkey string     `json:"p2pk,omitempty"`
	Unit       string     `json:"unit,omitempty"`
}

type MintInfo struct {
	URL   string   `json:"url"`
	Units []string `json:"units,omitempty"`
}

type DLEQProof struct {
	E string `json:"e"`
	S string `json:"s"`
	R string `json:"r"`
}

type Proof struct {
	Amount int        `json:"amount"`
	ID     string     `json:"id"`
	Secret string     `json:"secret"`
	C      string     `json:"C"`
	DLEQ   *DLEQProof `json:"dleq,omitempty"`
}

type NutzapContent struct {
	Comment string `json:"comment,omitempty"`
}

type Signer interface {
	Sign(ctx context.Context, ev *nostr.Event) error
}

type QueryFunc func(ctx context.Context, filter nostr.Filter) ([]*nostr.Event, error)
type PublishFunc func(ctx context.Context, ev nostr.Event) error
type RoutedQueryFunc func(ctx context.Context, relays []string, filter nostr.Filter) ([]*nostr.Event, error)
type RoutedPublishFunc func(ctx context.Context, relays []string, ev nostr.Event) error

type RelayPurpose uint8

const (
	RelayRead RelayPurpose = iota
	RelayWrite
)

type RelayResolver func(ctx context.Context, pubkeyHex string, purpose RelayPurpose) ([]string, error)
type MintKeyResolver func(ctx context.Context, mint, unit, keysetID string, amount int) (compressedPubkeyHex string, err error)

type RedemptionHistoryPublisher interface {
	PublishNutzapRedemption(ctx context.Context, relays []string, amount int, unit, createdEventID, createdRelayHint, nutzapEventID, nutzapRelayHint, senderPubkeyHex string) (*nostr.Event, error)
}

type Redeemer func(ctx context.Context, nutzap *ReceivedNutzap) (*nostr.Event, error)

type RedemptionResult struct {
	Nutzap       *ReceivedNutzap
	CreatedToken *nostr.Event
	History      *nostr.Event
}

type ClientOption func(*Client)

func WithRouting(query RoutedQueryFunc, publish RoutedPublishFunc, resolver RelayResolver) ClientOption {
	return func(c *Client) {
		c.routedQuery = query
		c.routedPublish = publish
		c.resolveRelays = resolver
	}
}

func WithMintKeyResolver(resolver MintKeyResolver) ClientOption {
	return func(c *Client) { c.resolveMintKey = resolver }
}

func WithRedemptionHistoryPublisher(publisher RedemptionHistoryPublisher) ClientOption {
	return func(c *Client) { c.history = publisher }
}

func WithClock(now func() time.Time) ClientOption {
	return func(c *Client) {
		if now != nil {
			c.now = now
		}
	}
}

type Client struct {
	signer         Signer
	publish        PublishFunc
	query          QueryFunc
	routedPublish  RoutedPublishFunc
	routedQuery    RoutedQueryFunc
	resolveRelays  RelayResolver
	resolveMintKey MintKeyResolver
	history        RedemptionHistoryPublisher
	now            func() time.Time
}

func NewClient(signer Signer, publish PublishFunc, query QueryFunc, opts ...ClientOption) *Client {
	c := &Client{signer: signer, publish: publish, query: query, now: time.Now}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) PublishNutzapInfo(ctx context.Context, mints []MintInfo, p2pkPubkey, unit string) (*nostr.Event, error) {
	return c.PublishNutzapInfoWithRelays(ctx, nil, mints, p2pkPubkey, unit)
}

func (c *Client) PublishNutzapInfoWithRelays(ctx context.Context, relays []string, mints []MintInfo, p2pkPubkey, unit string) (*nostr.Event, error) {
	p2pkPubkey, err := normalizeP2PKPubkey(p2pkPubkey)
	if err != nil {
		return nil, err
	}
	mints, err = normalizeMints(mints, unit)
	if err != nil {
		return nil, err
	}
	if len(mints) == 0 {
		return nil, fmt.Errorf("nip61: at least one mint is required")
	}
	tags := nostr.Tags{}
	for _, relay := range normalizeRelays(relays) {
		tags = append(tags, nostr.Tag{"relay", relay})
	}
	for _, mint := range mints {
		tag := nostr.Tag{"mint", mint.URL}
		tag = append(tag, mint.Units...)
		tags = append(tags, tag)
	}
	tags = append(tags, nostr.Tag{"pubkey", p2pkPubkey})
	ev := &nostr.Event{Kind: KindNutzapInfo, Content: "", CreatedAt: nostr.Timestamp(c.now().Unix()), Tags: tags}
	if err := c.sign(ctx, ev); err != nil {
		return nil, fmt.Errorf("nip61: sign nutzap info: %w", err)
	}
	publishRelays, err := c.relays(ctx, ev.PubKey.Hex(), RelayWrite)
	if err != nil {
		return nil, fmt.Errorf("nip61: resolve info publish relays: %w", err)
	}
	if err := c.publishOn(ctx, publishRelays, *ev); err != nil {
		return nil, fmt.Errorf("nip61: publish nutzap info: %w", err)
	}
	return ev, nil
}

func (c *Client) FetchNutzapInfo(ctx context.Context, pubkeyHex string) (*NutzapInfo, *nostr.Event, error) {
	pk, err := nostr.PubKeyFromHex(pubkeyHex)
	if err != nil {
		return nil, nil, fmt.Errorf("nip61: invalid pubkey: %w", err)
	}
	relays, err := c.relays(ctx, pubkeyHex, RelayWrite)
	if err != nil {
		return nil, nil, fmt.Errorf("nip61: resolve info query relays: %w", err)
	}
	events, err := c.queryOn(ctx, relays, nostr.Filter{Authors: []nostr.PubKey{pk}, Kinds: []nostr.Kind{KindNutzapInfo}})
	if err != nil {
		return nil, nil, fmt.Errorf("nip61: query nutzap info: %w", err)
	}
	ev := latestValidEvent(events, pk, KindNutzapInfo, c.now())
	if ev == nil {
		return nil, nil, fmt.Errorf("nip61: no valid nutzap info found for pubkey %s", pubkeyHex)
	}
	info, err := parseNutzapInfo(ev)
	if err != nil {
		return nil, ev, err
	}
	return info, ev, nil
}

func parseNutzapInfo(ev *nostr.Event) (*NutzapInfo, error) {
	info := &NutzapInfo{}
	pubkeys := 0
	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "relay":
			info.Relays = append(info.Relays, tag[1])
		case "mint":
			info.Mints = append(info.Mints, MintInfo{URL: tag[1], Units: append([]string(nil), tag[2:]...)})
		case "pubkey":
			pubkeys++
			info.P2PKPubkey = tag[1]
		}
	}
	if pubkeys != 1 {
		return nil, fmt.Errorf("nip61: nutzap info requires exactly one wallet pubkey")
	}
	var err error
	info.P2PKPubkey, err = normalizeP2PKPubkey(info.P2PKPubkey)
	if err != nil {
		return nil, err
	}
	info.Mints, err = normalizeMints(info.Mints, "sat")
	if err != nil {
		return nil, err
	}
	if len(info.Mints) == 0 {
		return nil, fmt.Errorf("nip61: nutzap info has no valid mints")
	}
	info.Relays = normalizeRelays(info.Relays)
	return info, nil
}

func (c *Client) SendNutzap(ctx context.Context, recipientPubkeyHex, mint string, proofs []Proof, comment, zappedEventID string) (*nostr.Event, error) {
	return c.SendNutzapWithContext(ctx, recipientPubkeyHex, mint, "sat", proofs, comment, zappedEventID, "", "")
}

func (c *Client) SendNutzapWithContext(ctx context.Context, recipientPubkeyHex, mint, unit string, proofs []Proof, comment, zappedEventID, relayHint, zappedKind string) (*nostr.Event, error) {
	if _, err := nostr.PubKeyFromHex(recipientPubkeyHex); err != nil {
		return nil, fmt.Errorf("nip61: invalid recipient pubkey: %w", err)
	}
	info, _, err := c.FetchNutzapInfo(ctx, recipientPubkeyHex)
	if err != nil {
		return nil, fmt.Errorf("nip61: discover recipient kind 10019: %w", err)
	}
	mint, mintInfo, err := allowedMint(info, mint)
	if err != nil {
		return nil, err
	}
	unit = strings.ToLower(defaultUnit(unit))
	if !supportsUnit(mintInfo, unit) {
		return nil, fmt.Errorf("nip61: recipient mint %s does not advertise unit %s", mint, unit)
	}
	if err := c.validateProofs(ctx, mint, unit, info.P2PKPubkey, proofs); err != nil {
		return nil, err
	}
	tags := nostr.Tags{{"unit", unit}, {"u", mint}, {"p", recipientPubkeyHex}}
	for _, proof := range proofs {
		proofJSON, err := json.Marshal(proof)
		if err != nil {
			return nil, fmt.Errorf("nip61: marshal proof: %w", err)
		}
		tags = append(tags, nostr.Tag{"proof", string(proofJSON)})
	}
	if zappedEventID != "" {
		if !validEventID(zappedEventID) {
			return nil, fmt.Errorf("nip61: invalid zapped event id")
		}
		tag := nostr.Tag{"e", strings.ToLower(zappedEventID)}
		if relayHint != "" {
			tag = append(tag, relayHint)
		}
		tags = append(tags, tag)
	}
	if zappedKind != "" {
		if _, err := strconv.Atoi(zappedKind); err != nil {
			return nil, fmt.Errorf("nip61: invalid zapped kind")
		}
		tags = append(tags, nostr.Tag{"k", zappedKind})
	}
	ev := &nostr.Event{Kind: KindNutzap, Content: comment, CreatedAt: nostr.Timestamp(c.now().Unix()), Tags: tags}
	if err := c.sign(ctx, ev); err != nil {
		return nil, fmt.Errorf("nip61: sign nutzap: %w", err)
	}
	publishRelays := info.Relays
	if len(publishRelays) == 0 {
		publishRelays, err = c.relays(ctx, recipientPubkeyHex, RelayRead)
		if err != nil {
			return nil, fmt.Errorf("nip61: resolve recipient read relays: %w", err)
		}
	}
	if c.routedPublish != nil && len(publishRelays) == 0 {
		return nil, fmt.Errorf("nip61: recipient has no nutzap inbox relays")
	}
	if err := c.publishOn(ctx, publishRelays, *ev); err != nil {
		return nil, fmt.Errorf("nip61: publish nutzap: %w", err)
	}
	return ev, nil
}

type ReceivedNutzap struct {
	Event              *nostr.Event
	SenderPubkeyHex    string
	RecipientPubkeyHex string
	Mint               string
	Unit               string
	Proofs             []Proof
	Amount             int
	Comment            string
	ZappedEventID      string
	ZappedKind         string
}

// ParseNutzap validates the Nostr envelope and tag cardinality. Balance or
// redemption decisions must use Client.ValidateNutzap, which also enforces the
// recipient's kind-10019 policy and Cashu DLEQ proofs.
func ParseNutzap(ev *nostr.Event) (*ReceivedNutzap, error) {
	return parseNutzap(ev, "", time.Now())
}

func (c *Client) ValidateNutzap(ctx context.Context, recipientPubkeyHex string, ev *nostr.Event) (*ReceivedNutzap, error) {
	if _, err := nostr.PubKeyFromHex(recipientPubkeyHex); err != nil {
		return nil, fmt.Errorf("nip61: invalid recipient pubkey: %w", err)
	}
	result, err := parseNutzap(ev, recipientPubkeyHex, c.now())
	if err != nil {
		return nil, err
	}
	info, _, err := c.FetchNutzapInfo(ctx, recipientPubkeyHex)
	if err != nil {
		return nil, err
	}
	canonicalMint, mintInfo, err := allowedMint(info, result.Mint)
	if err != nil {
		return nil, err
	}
	if canonicalMint != result.Mint {
		return nil, fmt.Errorf("nip61: u tag is not the exact advertised mint URL")
	}
	if !supportsUnit(mintInfo, result.Unit) {
		return nil, fmt.Errorf("nip61: nutzap unit is not advertised by recipient")
	}
	if err := c.validateProofs(ctx, canonicalMint, result.Unit, info.P2PKPubkey, result.Proofs); err != nil {
		return nil, err
	}
	return result, nil
}

func parseNutzap(ev *nostr.Event, expectedRecipient string, now time.Time) (*ReceivedNutzap, error) {
	if ev == nil {
		return nil, fmt.Errorf("nip61: nil nutzap event")
	}
	if err := validateEvent(ev, ev.PubKey, KindNutzap, now); err != nil {
		return nil, fmt.Errorf("nip61: invalid nutzap event: %w", err)
	}
	result := &ReceivedNutzap{Event: ev, SenderPubkeyHex: ev.PubKey.Hex(), Unit: "sat", Comment: ev.Content}
	counts := map[string]int{}
	seenProofs := map[string]struct{}{}
	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "p":
			counts["p"]++
			if _, err := nostr.PubKeyFromHex(tag[1]); err != nil {
				return nil, fmt.Errorf("nip61: invalid recipient p tag")
			}
			result.RecipientPubkeyHex = tag[1]
		case "u":
			counts["u"]++
			normalized, err := normalizeMintURL(tag[1])
			if err != nil {
				return nil, err
			}
			if normalized != tag[1] {
				return nil, fmt.Errorf("nip61: u tag is not normalized")
			}
			result.Mint = normalized
		case "unit":
			counts["unit"]++
			result.Unit = strings.ToLower(strings.TrimSpace(tag[1]))
		case "proof":
			var proof Proof
			if err := json.Unmarshal([]byte(tag[1]), &proof); err != nil {
				return nil, fmt.Errorf("nip61: parse proof tag: %w", err)
			}
			key := proof.ID + "\x00" + proof.Secret + "\x00" + proof.C
			if _, ok := seenProofs[key]; ok {
				return nil, fmt.Errorf("nip61: duplicate proof")
			}
			seenProofs[key] = struct{}{}
			if proof.Amount <= 0 || proof.ID == "" || proof.Secret == "" || proof.C == "" || proof.DLEQ == nil {
				return nil, fmt.Errorf("nip61: malformed proof")
			}
			if _, err := parseP2PKSecret(proof.Secret); err != nil {
				return nil, err
			}
			if proof.Amount > math.MaxInt-result.Amount {
				return nil, fmt.Errorf("nip61: proof amount overflow")
			}
			result.Proofs = append(result.Proofs, proof)
			result.Amount += proof.Amount
		case "e":
			counts["e"]++
			if !validEventID(tag[1]) {
				return nil, fmt.Errorf("nip61: invalid e tag")
			}
			result.ZappedEventID = tag[1]
		case "k":
			counts["k"]++
			if _, err := strconv.Atoi(tag[1]); err != nil {
				return nil, fmt.Errorf("nip61: invalid k tag")
			}
			result.ZappedKind = tag[1]
		}
	}
	if counts["p"] != 1 || counts["u"] != 1 || counts["unit"] > 1 || counts["e"] > 1 || counts["k"] > 1 {
		return nil, fmt.Errorf("nip61: invalid nutzap tag cardinality")
	}
	if expectedRecipient != "" && result.RecipientPubkeyHex != expectedRecipient {
		return nil, fmt.Errorf("nip61: nutzap recipient mismatch")
	}
	if len(result.Proofs) == 0 {
		return nil, fmt.Errorf("nip61: nutzap missing proofs")
	}
	if result.Unit == "" {
		return nil, fmt.Errorf("nip61: empty unit")
	}
	return result, nil
}

func (c *Client) RedeemNutzap(ctx context.Context, recipientPubkeyHex string, ev *nostr.Event, redeem Redeemer) (*RedemptionResult, error) {
	if redeem == nil {
		return nil, fmt.Errorf("nip61: redeemer is required")
	}
	received, err := c.ValidateNutzap(ctx, recipientPubkeyHex, ev)
	if err != nil {
		return nil, err
	}
	created, err := redeem(ctx, received)
	if err != nil {
		return &RedemptionResult{Nutzap: received}, fmt.Errorf("nip61: redeem Cashu proofs: %w", err)
	}
	history, err := c.MarkNutzapRedeemed(ctx, recipientPubkeyHex, received, created, "", "")
	result := &RedemptionResult{Nutzap: received, CreatedToken: created, History: history}
	if err != nil {
		return result, err
	}
	return result, nil
}

func (c *Client) MarkNutzapRedeemed(ctx context.Context, recipientPubkeyHex string, received *ReceivedNutzap, createdToken *nostr.Event, createdRelayHint, nutzapRelayHint string) (*nostr.Event, error) {
	if received == nil || received.Event == nil {
		return nil, fmt.Errorf("nip61: validated nutzap is required")
	}
	validated, err := c.ValidateNutzap(ctx, recipientPubkeyHex, received.Event)
	if err != nil {
		return nil, err
	}
	recipient, _ := nostr.PubKeyFromHex(recipientPubkeyHex)
	if err := validateEvent(createdToken, recipient, KindToken, c.now()); err != nil {
		return nil, fmt.Errorf("nip61: invalid created token event: %w", err)
	}
	if c.history == nil {
		return nil, fmt.Errorf("nip61: redemption history publisher is required")
	}
	if c.resolveRelays == nil {
		return nil, fmt.Errorf("nip61: NIP-65 relay resolver is required for redemption markers")
	}
	senderRelays, err := c.relays(ctx, validated.SenderPubkeyHex, RelayRead)
	if err != nil {
		return nil, fmt.Errorf("nip61: resolve sender NIP-65 read relays: %w", err)
	}
	if len(senderRelays) == 0 {
		return nil, fmt.Errorf("nip61: sender has no NIP-65 read relays")
	}
	if already, err := c.hasRedeemedMarker(ctx, senderRelays, recipient, validated.Event.ID.Hex()); err != nil {
		return nil, err
	} else if already {
		return nil, ErrAlreadyRedeemed
	}
	return c.history.PublishNutzapRedemption(ctx, senderRelays, validated.Amount, validated.Unit, createdToken.ID.Hex(), createdRelayHint, validated.Event.ID.Hex(), nutzapRelayHint, validated.SenderPubkeyHex)
}

func (c *Client) hasRedeemedMarker(ctx context.Context, relays []string, author nostr.PubKey, nutzapID string) (bool, error) {
	events, err := c.queryOn(ctx, relays, nostr.Filter{Authors: []nostr.PubKey{author}, Kinds: []nostr.Kind{KindTokenHistory}})
	if err != nil {
		return false, fmt.Errorf("nip61: query redemption markers: %w", err)
	}
	for _, ev := range events {
		if validateEvent(ev, author, KindTokenHistory, c.now()) != nil {
			continue
		}
		for _, tag := range ev.Tags {
			if len(tag) >= 4 && tag[0] == "e" && tag[1] == nutzapID && tag[3] == "redeemed" {
				return true, nil
			}
		}
	}
	return false, nil
}

func (c *Client) validateProofs(ctx context.Context, mint, unit, advertisedPubkey string, proofs []Proof) error {
	if len(proofs) == 0 {
		return fmt.Errorf("nip61: proofs are required")
	}
	if c.resolveMintKey == nil {
		return fmt.Errorf("nip61: mint amount-key resolver is required")
	}
	expectedLock := "02" + advertisedPubkey
	seenSecret, seenC := map[string]struct{}{}, map[string]struct{}{}
	keyCache := map[string]*secp256k1.PublicKey{}
	for _, proof := range proofs {
		if proof.Amount <= 0 || proof.ID == "" || proof.Secret == "" || proof.C == "" || proof.DLEQ == nil {
			return fmt.Errorf("nip61: malformed proof")
		}
		if _, ok := seenSecret[proof.Secret]; ok {
			return fmt.Errorf("nip61: duplicate proof secret")
		}
		if _, ok := seenC[strings.ToLower(proof.C)]; ok {
			return fmt.Errorf("nip61: duplicate proof signature")
		}
		seenSecret[proof.Secret] = struct{}{}
		seenC[strings.ToLower(proof.C)] = struct{}{}
		secret, err := parseP2PKSecret(proof.Secret)
		if err != nil {
			return err
		}
		if secret.Data != expectedLock {
			return fmt.Errorf("nip61: proof is not P2PK-locked to recipient wallet key with 02 prefix")
		}
		cacheKey := proof.ID + "\x00" + strconv.Itoa(proof.Amount)
		mintKey := keyCache[cacheKey]
		if mintKey == nil {
			keyHex, err := c.resolveMintKey(ctx, mint, unit, proof.ID, proof.Amount)
			if err != nil {
				return fmt.Errorf("nip61: resolve mint amount key: %w", err)
			}
			keyBytes, err := hex.DecodeString(keyHex)
			if err != nil {
				return fmt.Errorf("nip61: invalid mint amount key: %w", err)
			}
			mintKey, err = secp256k1.ParsePubKey(keyBytes)
			if err != nil {
				return fmt.Errorf("nip61: invalid mint amount key: %w", err)
			}
			keyCache[cacheKey] = mintKey
		}
		if err := verifyProofDLEQ(proof, mintKey); err != nil {
			return fmt.Errorf("nip61: invalid NUT-12 DLEQ proof: %w", err)
		}
	}
	return nil
}

func (c *Client) sign(ctx context.Context, ev *nostr.Event) error {
	if c.signer == nil {
		return fmt.Errorf("signer is required")
	}
	if err := c.signer.Sign(ctx, ev); err != nil {
		return err
	}
	return validateEvent(ev, ev.PubKey, ev.Kind, c.now())
}

func (c *Client) queryOn(ctx context.Context, relays []string, filter nostr.Filter) ([]*nostr.Event, error) {
	if c.routedQuery != nil {
		return c.routedQuery(ctx, normalizeRelays(relays), filter)
	}
	if c.query == nil {
		return nil, fmt.Errorf("nip61: query function is required")
	}
	return c.query(ctx, filter)
}

func (c *Client) publishOn(ctx context.Context, relays []string, ev nostr.Event) error {
	if c.routedPublish != nil {
		return c.routedPublish(ctx, normalizeRelays(relays), ev)
	}
	if c.publish == nil {
		return fmt.Errorf("nip61: publish function is required")
	}
	return c.publish(ctx, ev)
}

func (c *Client) relays(ctx context.Context, pubkey string, purpose RelayPurpose) ([]string, error) {
	if c.resolveRelays == nil {
		return nil, nil
	}
	relays, err := c.resolveRelays(ctx, pubkey, purpose)
	if err != nil {
		return nil, err
	}
	return normalizeRelays(relays), nil
}

func validateEvent(ev *nostr.Event, author nostr.PubKey, kind nostr.Kind, now time.Time) error {
	if ev == nil {
		return fmt.Errorf("nil event")
	}
	if ev.Kind != kind {
		return fmt.Errorf("unexpected kind %d", ev.Kind)
	}
	if ev.PubKey != author {
		return fmt.Errorf("unexpected author")
	}
	if ev.CreatedAt <= 0 || ev.CreatedAt > nostr.Timestamp(now.Add(maxFutureSkew).Unix()) {
		return fmt.Errorf("invalid timestamp")
	}
	if !ev.CheckID() {
		return fmt.Errorf("invalid event id")
	}
	if !ev.VerifySignature() {
		return fmt.Errorf("invalid event signature")
	}
	return nil
}

func latestValidEvent(events []*nostr.Event, author nostr.PubKey, kind nostr.Kind, now time.Time) *nostr.Event {
	var best *nostr.Event
	for _, ev := range events {
		if validateEvent(ev, author, kind, now) != nil {
			continue
		}
		if best == nil || ev.CreatedAt > best.CreatedAt || (ev.CreatedAt == best.CreatedAt && ev.ID.Hex() < best.ID.Hex()) {
			best = ev
		}
	}
	return best
}

func allowedMint(info *NutzapInfo, requested string) (string, MintInfo, error) {
	normalized, err := normalizeMintURL(requested)
	if err != nil {
		return "", MintInfo{}, err
	}
	for _, mint := range info.Mints {
		if mint.URL == normalized {
			return mint.URL, mint, nil
		}
	}
	return "", MintInfo{}, fmt.Errorf("nip61: mint is not in recipient kind 10019")
}

func supportsUnit(mint MintInfo, unit string) bool {
	for _, supported := range mint.Units {
		if supported == unit {
			return true
		}
	}
	return false
}

func normalizeMints(mints []MintInfo, defaultMintUnit string) ([]MintInfo, error) {
	if defaultMintUnit == "" {
		defaultMintUnit = "sat"
	}
	seen := map[string]int{}
	out := make([]MintInfo, 0, len(mints))
	for _, mint := range mints {
		normalized, err := normalizeMintURL(mint.URL)
		if err != nil {
			return nil, err
		}
		units := uniqueLower(mint.Units)
		if len(units) == 0 {
			units = []string{strings.ToLower(defaultMintUnit)}
		}
		if idx, ok := seen[normalized]; ok {
			out[idx].Units = uniqueLower(append(out[idx].Units, units...))
			continue
		}
		seen[normalized] = len(out)
		out = append(out, MintInfo{URL: normalized, Units: units})
	}
	return out, nil
}

func normalizeP2PKPubkey(raw string) (string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	decoded, err := hex.DecodeString(raw)
	if err != nil || len(decoded) != 32 {
		return "", fmt.Errorf("nip61: wallet P2PK pubkey must be 32-byte x-only hex")
	}
	if _, err := secp256k1.ParsePubKey(append([]byte{0x02}, decoded...)); err != nil {
		return "", fmt.Errorf("nip61: invalid wallet P2PK pubkey: %w", err)
	}
	return raw, nil
}

func normalizeMintURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("nip61: invalid mint URL %q", raw)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return "", fmt.Errorf("nip61: mint URL must use https")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("nip61: mint URL must not contain credentials, query, or fragment")
	}
	u.Host = normalizedURLHost(u)
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "/" {
		u.Path = ""
	}
	return u.String(), nil
}

func normalizedURLHost(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		return net.JoinHostPort(host, port)
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func normalizeRelays(relays []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(relays))
	for _, raw := range relays {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || u.Host == "" || (u.Scheme != "wss" && u.Scheme != "ws") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			continue
		}
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		u.Path = strings.TrimRight(u.Path, "/")
		value := u.String()
		if _, ok := seen[value]; !ok {
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func uniqueLower(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; !ok {
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	return out
}

func validEventID(id string) bool {
	_, err := nostr.IDFromHex(strings.TrimSpace(id))
	return err == nil
}

func defaultUnit(unit string) string {
	if unit == "" {
		return "sat"
	}
	return unit
}
