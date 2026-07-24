// Package nip60 implements NIP-60 Cashu wallets on Nostr.
package nip60

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"
)

const (
	KindQuote        = 7374
	KindUnspentToken = 7375
	KindTokenHistory = 7376
	KindWallet       = 17375
	KindNutzapInfo   = 10019
	KindDeletion     = 5
)

const maxFutureSkew = 10 * time.Minute

type WalletContent struct {
	Name        string      `json:"name,omitempty"`
	Description string      `json:"description,omitempty"`
	Unit        string      `json:"unit,omitempty"`
	Privkey     string      `json:"privkey,omitempty"`
	Mints       []MintEntry `json:"mints,omitempty"`
}

type MintEntry struct {
	URL   string   `json:"url"`
	Units []string `json:"units,omitempty"`
}

type UnspentTokenContent struct {
	Mint   string   `json:"mint"`
	Unit   string   `json:"unit,omitempty"`
	Proofs []Proof  `json:"proofs"`
	Del    []string `json:"del,omitempty"`
}

type Proof struct {
	Amount int    `json:"amount"`
	ID     string `json:"id"`
	Secret string `json:"secret"`
	C      string `json:"C"`
}

type TokenHistoryContent struct {
	Direction string `json:"direction"`
	Amount    int    `json:"amount"`
	Unit      string `json:"unit"`
	Memo      string `json:"memo,omitempty"`
	Mint      string `json:"mint,omitempty"`
}

type HistoryRef struct {
	EventID   string
	RelayHint string
	Marker    string
}

type RolloverRequest struct {
	Mint              string
	Unit              string
	Proofs            []Proof
	DestroyedEventIDs []string
}

type RolloverResult struct {
	CreatedToken *nostr.Event
	Deletion     *nostr.Event
}

type RolloverError struct {
	Stage string
	Err   error
}

func (e *RolloverError) Error() string { return fmt.Sprintf("nip60: rollover %s: %v", e.Stage, e.Err) }
func (e *RolloverError) Unwrap() error { return e.Err }

type QuoteRecord struct {
	QuoteID   string
	Mint      string
	ExpiresAt time.Time
	Event     *nostr.Event
}

type Encryptor interface {
	Encrypt(ctx context.Context, recipientPubkeyHex, plaintext string) (string, error)
	Decrypt(ctx context.Context, senderPubkeyHex, ciphertext string) (string, error)
	PublicKeyHex() string
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
	RelayRead  RelayPurpose = iota // NIP-65 read/inbox relays.
	RelayWrite                     // NIP-65 write/author relays.
)

type RelayResolver func(ctx context.Context, pubkeyHex string, purpose RelayPurpose) ([]string, error)
type WalletOption func(*WalletClient)

func WithRouting(query RoutedQueryFunc, publish RoutedPublishFunc, resolver RelayResolver) WalletOption {
	return func(w *WalletClient) {
		w.routedQuery = query
		w.routedPublish = publish
		w.resolveRelays = resolver
	}
}

func WithClock(now func() time.Time) WalletOption {
	return func(w *WalletClient) {
		if now != nil {
			w.now = now
		}
	}
}

type WalletClient struct {
	enc           Encryptor
	signer        Signer
	publish       PublishFunc
	query         QueryFunc
	routedPublish RoutedPublishFunc
	routedQuery   RoutedQueryFunc
	resolveRelays RelayResolver
	now           func() time.Time
}

func NewWalletClient(enc Encryptor, signer Signer, publish PublishFunc, query QueryFunc, opts ...WalletOption) *WalletClient {
	w := &WalletClient{enc: enc, signer: signer, publish: publish, query: query, now: time.Now}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

func (w *WalletClient) PublishWallet(ctx context.Context, name string, mints []MintEntry, unit string) (*nostr.Event, error) {
	return w.PublishWalletWithPrivkey(ctx, name, mints, unit)
}

func (w *WalletClient) PublishWalletWithPrivkey(ctx context.Context, privkey string, mints []MintEntry, unit string) (*nostr.Event, error) {
	if strings.TrimSpace(privkey) == "" {
		return nil, fmt.Errorf("nip60: wallet privkey is required")
	}
	mints, err := normalizeMints(mints)
	if err != nil {
		return nil, err
	}
	if len(mints) == 0 {
		return nil, fmt.Errorf("nip60: at least one mint is required")
	}
	tags := [][]string{{"privkey", privkey}}
	for _, mint := range mints {
		tag := []string{"mint", mint.URL}
		tag = append(tag, mint.Units...)
		tags = append(tags, tag)
	}
	content, err := w.encryptJSON(ctx, tags)
	if err != nil {
		return nil, fmt.Errorf("nip60: encrypt wallet content: %w", err)
	}
	ev := &nostr.Event{Kind: KindWallet, Content: content, CreatedAt: nostr.Timestamp(w.now().Unix()), Tags: nostr.Tags{}}
	if err := w.sign(ctx, ev); err != nil {
		return nil, fmt.Errorf("nip60: sign wallet event: %w", err)
	}
	if err := w.publishWalletEvent(ctx, *ev, w.enc.PublicKeyHex()); err != nil {
		return nil, fmt.Errorf("nip60: publish wallet event: %w", err)
	}
	return ev, nil
}

func (w *WalletClient) FetchWallet(ctx context.Context, pubkeyHex string) (*WalletContent, *nostr.Event, error) {
	pk, err := nostr.PubKeyFromHex(pubkeyHex)
	if err != nil {
		return nil, nil, fmt.Errorf("nip60: invalid pubkey: %w", err)
	}
	relays, err := w.walletRelays(ctx, pubkeyHex)
	if err != nil {
		return nil, nil, err
	}
	events, err := w.queryOn(ctx, relays, nostr.Filter{Authors: []nostr.PubKey{pk}, Kinds: []nostr.Kind{KindWallet}})
	if err != nil {
		return nil, nil, fmt.Errorf("nip60: query wallet event: %w", err)
	}
	ev := latestValidEvent(events, pk, KindWallet, w.now())
	if ev == nil {
		return nil, nil, fmt.Errorf("nip60: no valid wallet event found for pubkey %s", pubkeyHex)
	}
	decrypted, err := w.enc.Decrypt(ctx, pubkeyHex, ev.Content)
	if err != nil {
		return nil, ev, fmt.Errorf("nip60: decrypt wallet content: %w", err)
	}
	content, err := parseWalletContent(decrypted)
	if err != nil {
		return nil, ev, err
	}
	return content, ev, nil
}

func parseWalletContent(decrypted string) (*WalletContent, error) {
	var tags [][]string
	if err := json.Unmarshal([]byte(decrypted), &tags); err == nil {
		content := &WalletContent{}
		for _, tag := range tags {
			if len(tag) < 2 {
				continue
			}
			switch tag[0] {
			case "privkey":
				content.Privkey, content.Name = tag[1], tag[1]
			case "mint":
				content.Mints = append(content.Mints, MintEntry{URL: tag[1], Units: append([]string(nil), tag[2:]...)})
			}
		}
		if content.Privkey == "" || len(content.Mints) == 0 {
			return nil, fmt.Errorf("nip60: wallet content missing privkey or mints")
		}
		return content, nil
	}
	var legacy WalletContent
	if err := json.Unmarshal([]byte(decrypted), &legacy); err != nil {
		return nil, fmt.Errorf("nip60: unmarshal wallet content: %w", err)
	}
	return &legacy, nil
}

func (w *WalletClient) PublishUnspentToken(ctx context.Context, mint string, proofs []Proof) (*nostr.Event, error) {
	return w.PublishUnspentTokenWithRollover(ctx, mint, "sat", proofs, nil)
}

// PublishUnspentTokenWithRollover publishes a token and, when del is non-empty,
// completes the state transition with the required kind-5 deletion.
func (w *WalletClient) PublishUnspentTokenWithRollover(ctx context.Context, mint, unit string, proofs []Proof, del []string) (*nostr.Event, error) {
	if len(del) > 0 {
		result, err := w.PublishRolloverTransition(ctx, RolloverRequest{Mint: mint, Unit: unit, Proofs: proofs, DestroyedEventIDs: del})
		if result != nil {
			return result.CreatedToken, err
		}
		return nil, err
	}
	return w.publishToken(ctx, mint, unit, proofs, nil)
}

func (w *WalletClient) PublishRolloverTransition(ctx context.Context, req RolloverRequest) (*RolloverResult, error) {
	ids, err := normalizeEventIDs(req.DestroyedEventIDs)
	if err != nil || len(ids) == 0 {
		if err == nil {
			err = fmt.Errorf("at least one destroyed token event is required")
		}
		return nil, &RolloverError{Stage: "create_token", Err: err}
	}
	created, err := w.publishToken(ctx, req.Mint, req.Unit, req.Proofs, ids)
	if err != nil {
		return nil, &RolloverError{Stage: "create_token", Err: err}
	}
	result := &RolloverResult{CreatedToken: created}
	deleted, err := w.PublishTokenDeletion(ctx, ids...)
	if err != nil {
		return result, &RolloverError{Stage: "delete_old_tokens", Err: err}
	}
	result.Deletion = deleted
	return result, nil
}

func (w *WalletClient) publishToken(ctx context.Context, mint, unit string, proofs []Proof, del []string) (*nostr.Event, error) {
	mint, err := normalizeMintURL(mint)
	if err != nil {
		return nil, err
	}
	if unit == "" {
		unit = "sat"
	}
	if err := validateProofs(proofs); err != nil {
		return nil, err
	}
	content, err := w.encryptJSON(ctx, UnspentTokenContent{Mint: mint, Unit: unit, Proofs: proofs, Del: del})
	if err != nil {
		return nil, fmt.Errorf("nip60: encrypt token content: %w", err)
	}
	ev := &nostr.Event{Kind: KindUnspentToken, Content: content, CreatedAt: nostr.Timestamp(w.now().Unix()), Tags: nostr.Tags{}}
	if err := w.sign(ctx, ev); err != nil {
		return nil, fmt.Errorf("nip60: sign token event: %w", err)
	}
	if err := w.publishWalletEvent(ctx, *ev, w.enc.PublicKeyHex()); err != nil {
		return nil, fmt.Errorf("nip60: publish token event: %w", err)
	}
	return ev, nil
}

func (w *WalletClient) PublishTokenDeletion(ctx context.Context, tokenEventIDs ...string) (*nostr.Event, error) {
	ids, err := normalizeEventIDs(tokenEventIDs)
	if err != nil {
		return nil, fmt.Errorf("nip60: invalid deletion: %w", err)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("nip60: token event ids are required")
	}
	tags := nostr.Tags{{"k", strconv.Itoa(KindUnspentToken)}}
	for _, id := range ids {
		tags = append(tags, nostr.Tag{"e", id})
	}
	ev := &nostr.Event{Kind: KindDeletion, CreatedAt: nostr.Timestamp(w.now().Unix()), Tags: tags, Content: ""}
	if err := w.sign(ctx, ev); err != nil {
		return nil, fmt.Errorf("nip60: sign token deletion event: %w", err)
	}
	if err := w.publishWalletEvent(ctx, *ev, w.enc.PublicKeyHex()); err != nil {
		return nil, fmt.Errorf("nip60: publish token deletion event: %w", err)
	}
	return ev, nil
}

func (w *WalletClient) FetchUnspentTokens(ctx context.Context, pubkeyHex string) ([]UnspentTokenContent, []*nostr.Event, error) {
	pk, err := nostr.PubKeyFromHex(pubkeyHex)
	if err != nil {
		return nil, nil, fmt.Errorf("nip60: invalid pubkey: %w", err)
	}
	relays, err := w.walletRelays(ctx, pubkeyHex)
	if err != nil {
		return nil, nil, err
	}
	events, err := w.queryOn(ctx, relays, nostr.Filter{Authors: []nostr.PubKey{pk}, Kinds: []nostr.Kind{KindUnspentToken, KindDeletion}})
	if err != nil {
		return nil, nil, fmt.Errorf("nip60: query token state: %w", err)
	}
	type tokenRecord struct {
		content UnspentTokenContent
		event   *nostr.Event
	}
	dead := make(map[string]struct{})
	records := make(map[string]tokenRecord)
	seen := make(map[string]struct{})
	for _, ev := range events {
		if ev == nil {
			continue
		}
		id := ev.ID.Hex()
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if err := validateEvent(ev, pk, ev.Kind, w.now()); err != nil {
			continue
		}
		switch ev.Kind {
		case KindDeletion:
			if !hasTag(ev.Tags, "k", strconv.Itoa(KindUnspentToken)) {
				continue
			}
			for _, tag := range ev.Tags {
				if len(tag) >= 2 && tag[0] == "e" && validEventID(tag[1]) {
					dead[strings.ToLower(tag[1])] = struct{}{}
				}
			}
		case KindUnspentToken:
			decrypted, err := w.enc.Decrypt(ctx, pubkeyHex, ev.Content)
			if err != nil {
				continue
			}
			var content UnspentTokenContent
			if err := json.Unmarshal([]byte(decrypted), &content); err != nil {
				continue
			}
			mint, err := normalizeMintURL(content.Mint)
			if err != nil || validateProofs(content.Proofs) != nil {
				continue
			}
			content.Mint = mint
			if content.Unit == "" {
				content.Unit = "sat"
			}
			validDel := true
			deletedIDs := make([]string, 0, len(content.Del))
			for _, oldID := range content.Del {
				if !validEventID(oldID) {
					validDel = false
					break
				}
				deletedIDs = append(deletedIDs, strings.ToLower(oldID))
			}
			if validDel {
				for _, oldID := range deletedIDs {
					dead[oldID] = struct{}{}
				}
				records[strings.ToLower(id)] = tokenRecord{content: content, event: ev}
			}
		}
	}
	live := make([]tokenRecord, 0, len(records))
	for id, record := range records {
		if _, deleted := dead[id]; !deleted {
			live = append(live, record)
		}
	}
	sort.Slice(live, func(i, j int) bool {
		if live[i].event.CreatedAt != live[j].event.CreatedAt {
			return live[i].event.CreatedAt > live[j].event.CreatedAt
		}
		return live[i].event.ID.Hex() < live[j].event.ID.Hex()
	})
	contents := make([]UnspentTokenContent, 0, len(live))
	raw := make([]*nostr.Event, 0, len(live))
	for _, record := range live {
		contents = append(contents, record.content)
		raw = append(raw, record.event)
	}
	return contents, raw, nil
}

// PublishTokenHistory requires at least one created, destroyed, or redeemed e reference.
func (w *WalletClient) PublishTokenHistory(ctx context.Context, direction string, amount int, unit, mint, memo string, refs ...HistoryRef) (*nostr.Event, error) {
	if direction != "in" && direction != "out" {
		return nil, fmt.Errorf("nip60: history direction must be in or out")
	}
	if amount <= 0 {
		return nil, fmt.Errorf("nip60: history amount must be positive")
	}
	if unit == "" {
		unit = "sat"
	}
	encryptedTags := [][]string{{"direction", direction}, {"amount", strconv.Itoa(amount)}, {"unit", unit}}
	if mint != "" {
		normalized, err := normalizeMintURL(mint)
		if err != nil {
			return nil, err
		}
		encryptedTags = append(encryptedTags, []string{"mint", normalized})
	}
	if memo != "" {
		encryptedTags = append(encryptedTags, []string{"memo", memo})
	}
	publicTags := nostr.Tags{}
	for _, ref := range refs {
		if !validEventID(ref.EventID) {
			return nil, fmt.Errorf("nip60: invalid history event id %q", ref.EventID)
		}
		tag := []string{"e", strings.ToLower(ref.EventID), ref.RelayHint, ref.Marker}
		switch ref.Marker {
		case "created", "destroyed":
			encryptedTags = append(encryptedTags, tag)
		case "redeemed":
			publicTags = append(publicTags, nostr.Tag(tag))
		default:
			return nil, fmt.Errorf("nip60: invalid history marker %q", ref.Marker)
		}
	}
	return w.PublishTokenHistoryTags(ctx, encryptedTags, publicTags)
}

func (w *WalletClient) PublishTokenHistoryTags(ctx context.Context, encryptedTags [][]string, publicTags nostr.Tags) (*nostr.Event, error) {
	if err := validateHistoryTags(encryptedTags, publicTags); err != nil {
		return nil, err
	}
	content, err := w.encryptJSON(ctx, encryptedTags)
	if err != nil {
		return nil, fmt.Errorf("nip60: encrypt history content: %w", err)
	}
	ev := &nostr.Event{Kind: KindTokenHistory, Content: content, CreatedAt: nostr.Timestamp(w.now().Unix()), Tags: publicTags}
	if err := w.sign(ctx, ev); err != nil {
		return nil, fmt.Errorf("nip60: sign history event: %w", err)
	}
	if err := w.publishWalletEvent(ctx, *ev, w.enc.PublicKeyHex()); err != nil {
		return nil, fmt.Errorf("nip60: publish history event: %w", err)
	}
	return ev, nil
}

// PublishNutzapRedemption is the narrow NIP-61 integration point.
func (w *WalletClient) PublishNutzapRedemption(ctx context.Context, relays []string, amount int, unit, createdEventID, createdRelayHint, nutzapEventID, nutzapRelayHint, senderPubkeyHex string) (*nostr.Event, error) {
	if _, err := nostr.PubKeyFromHex(senderPubkeyHex); err != nil {
		return nil, fmt.Errorf("nip60: invalid nutzap sender: %w", err)
	}
	encrypted := [][]string{{"direction", "in"}, {"amount", strconv.Itoa(amount)}, {"unit", defaultUnit(unit)}, {"e", createdEventID, createdRelayHint, "created"}}
	public := nostr.Tags{{"e", nutzapEventID, nutzapRelayHint, "redeemed"}, {"p", senderPubkeyHex}}
	if err := validateHistoryTags(encrypted, public); err != nil {
		return nil, err
	}
	content, err := w.encryptJSON(ctx, encrypted)
	if err != nil {
		return nil, fmt.Errorf("nip60: encrypt redemption history: %w", err)
	}
	ev := &nostr.Event{Kind: KindTokenHistory, Content: content, CreatedAt: nostr.Timestamp(w.now().Unix()), Tags: public}
	if err := w.sign(ctx, ev); err != nil {
		return nil, err
	}
	if err := w.publishOn(ctx, relays, *ev); err != nil {
		return nil, fmt.Errorf("nip60: publish redemption history: %w", err)
	}
	return ev, nil
}

func (w *WalletClient) PublishQuote(ctx context.Context, quoteID, mint string, expiresAt time.Time) (*nostr.Event, error) {
	quoteID = strings.TrimSpace(quoteID)
	if quoteID == "" {
		return nil, fmt.Errorf("nip60: quote id is required")
	}
	mint, err := normalizeMintURL(mint)
	if err != nil {
		return nil, err
	}
	now := w.now()
	if expiresAt.IsZero() {
		expiresAt = now.Add(14 * 24 * time.Hour)
	}
	if !expiresAt.After(now) || expiresAt.After(now.Add(14*24*time.Hour)) {
		return nil, fmt.Errorf("nip60: quote expiration must be within 14 days")
	}
	encrypted, err := w.enc.Encrypt(ctx, w.enc.PublicKeyHex(), quoteID)
	if err != nil {
		return nil, fmt.Errorf("nip60: encrypt quote id: %w", err)
	}
	ev := &nostr.Event{Kind: KindQuote, Content: encrypted, CreatedAt: nostr.Timestamp(now.Unix()), Tags: nostr.Tags{{"expiration", strconv.FormatInt(expiresAt.Unix(), 10)}, {"mint", mint}}}
	if err := w.sign(ctx, ev); err != nil {
		return nil, fmt.Errorf("nip60: sign quote event: %w", err)
	}
	if err := w.publishWalletEvent(ctx, *ev, w.enc.PublicKeyHex()); err != nil {
		return nil, fmt.Errorf("nip60: publish quote event: %w", err)
	}
	return ev, nil
}

func (w *WalletClient) FetchActiveQuotes(ctx context.Context, pubkeyHex string) ([]QuoteRecord, error) {
	pk, err := nostr.PubKeyFromHex(pubkeyHex)
	if err != nil {
		return nil, fmt.Errorf("nip60: invalid pubkey: %w", err)
	}
	relays, err := w.walletRelays(ctx, pubkeyHex)
	if err != nil {
		return nil, err
	}
	events, err := w.queryOn(ctx, relays, nostr.Filter{Authors: []nostr.PubKey{pk}, Kinds: []nostr.Kind{KindQuote}})
	if err != nil {
		return nil, fmt.Errorf("nip60: query quote events: %w", err)
	}
	now := w.now()
	seen := map[string]struct{}{}
	quotes := make([]QuoteRecord, 0, len(events))
	for _, ev := range events {
		if err := validateEvent(ev, pk, KindQuote, now); err != nil {
			continue
		}
		id := ev.ID.Hex()
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		expRaw, mintRaw := singleTagValue(ev.Tags, "expiration"), singleTagValue(ev.Tags, "mint")
		exp, err := strconv.ParseInt(expRaw, 10, 64)
		if err != nil || exp <= now.Unix() {
			continue
		}
		mint, err := normalizeMintURL(mintRaw)
		if err != nil {
			continue
		}
		quoteID, err := w.enc.Decrypt(ctx, pubkeyHex, ev.Content)
		if err != nil || strings.TrimSpace(quoteID) == "" {
			continue
		}
		quotes = append(quotes, QuoteRecord{QuoteID: quoteID, Mint: mint, ExpiresAt: time.Unix(exp, 0), Event: ev})
	}
	sort.Slice(quotes, func(i, j int) bool { return quotes[i].ExpiresAt.Before(quotes[j].ExpiresAt) })
	return quotes, nil
}

func (w *WalletClient) encryptJSON(ctx context.Context, value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return w.enc.Encrypt(ctx, w.enc.PublicKeyHex(), string(data))
}

func (w *WalletClient) sign(ctx context.Context, ev *nostr.Event) error {
	if w.signer == nil {
		return fmt.Errorf("signer is required")
	}
	if err := w.signer.Sign(ctx, ev); err != nil {
		return err
	}
	expected, err := nostr.PubKeyFromHex(w.enc.PublicKeyHex())
	if err != nil {
		return fmt.Errorf("invalid encryptor pubkey: %w", err)
	}
	return validateEvent(ev, expected, ev.Kind, w.now())
}

func (w *WalletClient) queryOn(ctx context.Context, relays []string, filter nostr.Filter) ([]*nostr.Event, error) {
	if w.routedQuery != nil {
		return w.routedQuery(ctx, normalizeRelays(relays), filter)
	}
	if w.query == nil {
		return nil, fmt.Errorf("nip60: query function is required")
	}
	return w.query(ctx, filter)
}

func (w *WalletClient) publishOn(ctx context.Context, relays []string, ev nostr.Event) error {
	if w.routedPublish != nil {
		return w.routedPublish(ctx, normalizeRelays(relays), ev)
	}
	if w.publish == nil {
		return fmt.Errorf("nip60: publish function is required")
	}
	return w.publish(ctx, ev)
}

func (w *WalletClient) publishWalletEvent(ctx context.Context, ev nostr.Event, owner string) error {
	relays, err := w.walletRelays(ctx, owner)
	if err != nil {
		return err
	}
	return w.publishOn(ctx, relays, ev)
}

func (w *WalletClient) walletRelays(ctx context.Context, owner string) ([]string, error) {
	var fallback []string
	var err error
	if w.resolveRelays != nil {
		fallback, err = w.resolveRelays(ctx, owner, RelayWrite)
		if err != nil {
			return nil, fmt.Errorf("nip60: resolve NIP-65 write relays: %w", err)
		}
	}
	pk, err := nostr.PubKeyFromHex(owner)
	if err != nil {
		return nil, fmt.Errorf("nip60: invalid wallet owner: %w", err)
	}
	events, queryErr := w.queryOn(ctx, fallback, nostr.Filter{Authors: []nostr.PubKey{pk}, Kinds: []nostr.Kind{KindNutzapInfo}})
	if queryErr == nil {
		if info := latestValidEvent(events, pk, KindNutzapInfo, w.now()); info != nil {
			var relays []string
			for _, tag := range info.Tags {
				if len(tag) >= 2 && tag[0] == "relay" {
					relays = append(relays, tag[1])
				}
			}
			if relays = normalizeRelays(relays); len(relays) > 0 {
				return relays, nil
			}
		}
	}
	if queryErr != nil {
		return nil, fmt.Errorf("nip60: query kind 10019 relay routing: %w", queryErr)
	}
	return normalizeRelays(fallback), nil
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

func validateProofs(proofs []Proof) error {
	if len(proofs) == 0 {
		return fmt.Errorf("nip60: at least one proof is required")
	}
	seen := map[string]struct{}{}
	for _, proof := range proofs {
		if proof.Amount <= 0 || proof.ID == "" || proof.Secret == "" || proof.C == "" {
			return fmt.Errorf("nip60: malformed Cashu proof")
		}
		key := proof.ID + "\x00" + proof.Secret
		if _, ok := seen[key]; ok {
			return fmt.Errorf("nip60: duplicate Cashu proof")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateHistoryTags(encrypted [][]string, public nostr.Tags) error {
	directions, amounts, units, refs := 0, 0, 0, 0
	for _, tag := range encrypted {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "direction":
			directions++
			if tag[1] != "in" && tag[1] != "out" {
				return fmt.Errorf("nip60: invalid history direction")
			}
		case "amount":
			amounts++
			amount, err := strconv.Atoi(tag[1])
			if err != nil || amount <= 0 {
				return fmt.Errorf("nip60: invalid history amount")
			}
		case "unit":
			units++
			if tag[1] == "" {
				return fmt.Errorf("nip60: history unit is required")
			}
		case "e":
			if len(tag) < 4 || !validEventID(tag[1]) || (tag[3] != "created" && tag[3] != "destroyed") {
				return fmt.Errorf("nip60: encrypted history e tags must be created or destroyed")
			}
			refs++
		}
	}
	for _, tag := range public {
		if len(tag) >= 1 && tag[0] == "e" {
			if len(tag) < 4 || !validEventID(tag[1]) || tag[3] != "redeemed" {
				return fmt.Errorf("nip60: public history e tags must be redeemed")
			}
			refs++
		}
	}
	if directions != 1 || amounts != 1 || units != 1 {
		return fmt.Errorf("nip60: history requires one direction, amount, and unit")
	}
	if refs == 0 {
		return fmt.Errorf("nip60: history requires at least one e reference")
	}
	return nil
}

func normalizeMints(mints []MintEntry) ([]MintEntry, error) {
	seen := map[string]int{}
	out := make([]MintEntry, 0, len(mints))
	for _, mint := range mints {
		normalized, err := normalizeMintURL(mint.URL)
		if err != nil {
			return nil, err
		}
		units := uniqueLower(mint.Units)
		if idx, ok := seen[normalized]; ok {
			out[idx].Units = uniqueLower(append(out[idx].Units, units...))
			continue
		}
		seen[normalized] = len(out)
		out = append(out, MintEntry{URL: normalized, Units: units})
	}
	return out, nil
}

func normalizeMintURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("nip60: invalid mint URL %q", raw)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return "", fmt.Errorf("nip60: mint URL must use https")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("nip60: mint URL must not contain credentials, query, or fragment")
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

func normalizeEventIDs(ids []string) ([]string, error) {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			continue
		}
		if !validEventID(id) {
			return nil, fmt.Errorf("invalid event id %q", id)
		}
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out, nil
}

func validEventID(id string) bool {
	_, err := nostr.IDFromHex(strings.TrimSpace(id))
	return err == nil
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
		key := u.String()
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			out = append(out, key)
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

func defaultUnit(unit string) string {
	if unit == "" {
		return "sat"
	}
	return unit
}

func hasTag(tags nostr.Tags, name, value string) bool {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == name && tag[1] == value {
			return true
		}
	}
	return false
}

func singleTagValue(tags nostr.Tags, name string) string {
	value := ""
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == name {
			if value != "" {
				return ""
			}
			value = tag[1]
		}
	}
	return value
}
