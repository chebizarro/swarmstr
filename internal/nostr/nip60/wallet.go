// Package nip60 implements NIP-60: Cashu Wallet on Nostr.
//
// NIP-60 defines a protocol for storing Cashu ecash tokens in Nostr events:
//   - kind 17375: encrypted wallet event (NIP-44 encrypted, replaceable)
//     Contains wallet privkey and mint tags as an encrypted array of tags.
//   - kind 7375: unspent token event (NIP-44 encrypted)
//     Stores Cashu token bundles with mint, unit, proofs, and optional del.
//   - kind 7376: token history event (NIP-44 encrypted)
//     Records spend/receive history as an encrypted array of tags.
//
// See: https://github.com/nostr-protocol/nips/blob/master/60.md
package nip60

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	nostr "fiatjaf.com/nostr"
)

const (
	// KindWallet is the NIP-60 wallet event kind (replaceable).
	KindWallet = 17375

	// KindUnspentToken is the NIP-60 unspent token bundle event kind.
	KindUnspentToken = 7375

	// KindTokenHistory is the NIP-60 token history event kind.
	KindTokenHistory = 7376

	// KindDeletion is the NIP-09 deletion event kind used to delete spent tokens.
	KindDeletion = 5
)

// WalletContent is the decrypted content of a kind:17375 wallet event.
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

type WalletClient struct {
	enc     Encryptor
	signer  Signer
	publish PublishFunc
	query   QueryFunc
}

func NewWalletClient(enc Encryptor, signer Signer, publish PublishFunc, query QueryFunc) *WalletClient {
	return &WalletClient{enc: enc, signer: signer, publish: publish, query: query}
}

// PublishWallet creates or replaces the kind:17375 wallet event. For backward
// compatibility, the name parameter is used as the encrypted wallet privkey.
func (w *WalletClient) PublishWallet(ctx context.Context, name string, mints []MintEntry, unit string) (*nostr.Event, error) {
	return w.PublishWalletWithPrivkey(ctx, name, mints, unit)
}

// PublishWalletWithPrivkey creates or replaces the kind:17375 wallet event.
func (w *WalletClient) PublishWalletWithPrivkey(ctx context.Context, privkey string, mints []MintEntry, unit string) (*nostr.Event, error) {
	if privkey == "" {
		return nil, fmt.Errorf("nip60: wallet privkey is required")
	}
	if len(mints) == 0 {
		return nil, fmt.Errorf("nip60: at least one mint is required")
	}
	tags := [][]string{{"privkey", privkey}}
	for _, m := range mints {
		if m.URL == "" {
			continue
		}
		tag := []string{"mint", m.URL}
		tag = append(tag, m.Units...)
		tags = append(tags, tag)
	}
	contentJSON, err := json.Marshal(tags)
	if err != nil {
		return nil, fmt.Errorf("nip60: marshal wallet tags: %w", err)
	}
	encrypted, err := w.enc.Encrypt(ctx, w.enc.PublicKeyHex(), string(contentJSON))
	if err != nil {
		return nil, fmt.Errorf("nip60: encrypt wallet content: %w", err)
	}
	ev := &nostr.Event{Kind: nostr.Kind(KindWallet), Content: encrypted, CreatedAt: nostr.Timestamp(time.Now().Unix()), Tags: nostr.Tags{}}
	if err := w.signer.Sign(ctx, ev); err != nil {
		return nil, fmt.Errorf("nip60: sign wallet event: %w", err)
	}
	if err := w.publish(ctx, *ev); err != nil {
		return nil, fmt.Errorf("nip60: publish wallet event: %w", err)
	}
	return ev, nil
}

func (w *WalletClient) FetchWallet(ctx context.Context, pubkeyHex string) (*WalletContent, *nostr.Event, error) {
	pk, err := nostr.PubKeyFromHex(pubkeyHex)
	if err != nil {
		return nil, nil, fmt.Errorf("nip60: invalid pubkey: %w", err)
	}
	filter := nostr.Filter{Authors: []nostr.PubKey{pk}, Kinds: []nostr.Kind{nostr.Kind(KindWallet)}, Limit: 1}
	events, err := w.query(ctx, filter)
	if err != nil {
		return nil, nil, fmt.Errorf("nip60: query wallet event: %w", err)
	}
	if len(events) == 0 {
		return nil, nil, fmt.Errorf("nip60: no wallet event found for pubkey %s", pubkeyHex)
	}
	ev := events[0]
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
				content.Privkey = tag[1]
				content.Name = tag[1]
			case "mint":
				content.Mints = append(content.Mints, MintEntry{URL: tag[1], Units: append([]string(nil), tag[2:]...)})
			}
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

func (w *WalletClient) PublishUnspentTokenWithRollover(ctx context.Context, mint, unit string, proofs []Proof, del []string) (*nostr.Event, error) {
	content := UnspentTokenContent{Mint: mint, Unit: unit, Proofs: proofs, Del: del}
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return nil, fmt.Errorf("nip60: marshal token content: %w", err)
	}
	encrypted, err := w.enc.Encrypt(ctx, w.enc.PublicKeyHex(), string(contentJSON))
	if err != nil {
		return nil, fmt.Errorf("nip60: encrypt token content: %w", err)
	}
	ev := &nostr.Event{Kind: nostr.Kind(KindUnspentToken), Content: encrypted, CreatedAt: nostr.Timestamp(time.Now().Unix()), Tags: nostr.Tags{}}
	if err := w.signer.Sign(ctx, ev); err != nil {
		return nil, fmt.Errorf("nip60: sign token event: %w", err)
	}
	if err := w.publish(ctx, *ev); err != nil {
		return nil, fmt.Errorf("nip60: publish token event: %w", err)
	}
	return ev, nil
}

func (w *WalletClient) PublishTokenDeletion(ctx context.Context, tokenEventIDs ...string) (*nostr.Event, error) {
	tags := nostr.Tags{{"k", strconv.Itoa(KindUnspentToken)}}
	for _, id := range tokenEventIDs {
		if id != "" {
			tags = append(tags, nostr.Tag{"e", id})
		}
	}
	ev := &nostr.Event{Kind: nostr.Kind(KindDeletion), CreatedAt: nostr.Timestamp(time.Now().Unix()), Tags: tags, Content: ""}
	if err := w.signer.Sign(ctx, ev); err != nil {
		return nil, fmt.Errorf("nip60: sign token deletion event: %w", err)
	}
	if err := w.publish(ctx, *ev); err != nil {
		return nil, fmt.Errorf("nip60: publish token deletion event: %w", err)
	}
	return ev, nil
}

func (w *WalletClient) FetchUnspentTokens(ctx context.Context, pubkeyHex string) ([]UnspentTokenContent, []*nostr.Event, error) {
	pk, err := nostr.PubKeyFromHex(pubkeyHex)
	if err != nil {
		return nil, nil, fmt.Errorf("nip60: invalid pubkey: %w", err)
	}
	events, err := w.query(ctx, nostr.Filter{Authors: []nostr.PubKey{pk}, Kinds: []nostr.Kind{nostr.Kind(KindUnspentToken)}})
	if err != nil {
		return nil, nil, fmt.Errorf("nip60: query token events: %w", err)
	}
	var tokens []UnspentTokenContent
	var rawEvents []*nostr.Event
	for _, ev := range events {
		decrypted, err := w.enc.Decrypt(ctx, pubkeyHex, ev.Content)
		if err != nil {
			continue
		}
		var content UnspentTokenContent
		if err := json.Unmarshal([]byte(decrypted), &content); err != nil {
			continue
		}
		if content.Unit == "" {
			content.Unit = "sat"
		}
		tokens = append(tokens, content)
		rawEvents = append(rawEvents, ev)
	}
	return tokens, rawEvents, nil
}

func (w *WalletClient) PublishTokenHistory(ctx context.Context, direction string, amount int, unit, mint, memo string) (*nostr.Event, error) {
	tags := [][]string{{"direction", direction}, {"amount", strconv.Itoa(amount)}, {"unit", unit}}
	if mint != "" {
		tags = append(tags, []string{"mint", mint})
	}
	if memo != "" {
		tags = append(tags, []string{"memo", memo})
	}
	return w.PublishTokenHistoryTags(ctx, tags, nil)
}

func (w *WalletClient) PublishTokenHistoryTags(ctx context.Context, encryptedTags [][]string, publicTags nostr.Tags) (*nostr.Event, error) {
	contentJSON, err := json.Marshal(encryptedTags)
	if err != nil {
		return nil, fmt.Errorf("nip60: marshal history tags: %w", err)
	}
	encrypted, err := w.enc.Encrypt(ctx, w.enc.PublicKeyHex(), string(contentJSON))
	if err != nil {
		return nil, fmt.Errorf("nip60: encrypt history content: %w", err)
	}
	ev := &nostr.Event{Kind: nostr.Kind(KindTokenHistory), Content: encrypted, CreatedAt: nostr.Timestamp(time.Now().Unix()), Tags: publicTags}
	if err := w.signer.Sign(ctx, ev); err != nil {
		return nil, fmt.Errorf("nip60: sign history event: %w", err)
	}
	if err := w.publish(ctx, *ev); err != nil {
		return nil, fmt.Errorf("nip60: publish history event: %w", err)
	}
	return ev, nil
}
