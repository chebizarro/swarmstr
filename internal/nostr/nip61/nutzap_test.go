package nip61_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"

	"metiq/internal/nostr/nip61"
)

var fixedNow = time.Unix(1_800_000_000, 0)

type signer struct{ sk nostr.SecretKey }

func (s signer) Sign(_ context.Context, ev *nostr.Event) error { return ev.Sign(s.sk) }

type store struct{ events []nostr.Event }

func (s *store) publish(_ context.Context, ev nostr.Event) error {
	s.events = append(s.events, ev)
	return nil
}
func (s *store) query(_ context.Context, filter nostr.Filter) ([]*nostr.Event, error) {
	var out []*nostr.Event
	for i := range s.events {
		ev := &s.events[i]
		if len(filter.Kinds) > 0 && !hasKind(filter.Kinds, ev.Kind) {
			continue
		}
		if len(filter.Authors) > 0 && !hasAuthor(filter.Authors, ev.PubKey) {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}
func hasKind(kinds []nostr.Kind, kind nostr.Kind) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}
func hasAuthor(authors []nostr.PubKey, author nostr.PubKey) bool {
	for _, a := range authors {
		if a == author {
			return true
		}
	}
	return false
}

func nostrKey(t *testing.T, digit string) nostr.SecretKey {
	t.Helper()
	sk, err := nostr.SecretKeyFromHex(strings.Repeat(digit, 64))
	if err != nil {
		t.Fatal(err)
	}
	return sk
}

func p2pkXOnly() (string, *secp256k1.PrivateKey) {
	key := secp256k1.PrivKeyFromBytes(bytes32(4))
	return hex.EncodeToString(key.PubKey().SerializeCompressed()[1:]), key
}

func signedInfo(t *testing.T, recipient nostr.SecretKey, relays []string, mints []nip61.MintInfo, p2pk string) nostr.Event {
	t.Helper()
	tags := nostr.Tags{}
	for _, relay := range relays {
		tags = append(tags, nostr.Tag{"relay", relay})
	}
	for _, mint := range mints {
		tag := nostr.Tag{"mint", mint.URL}
		tag = append(tag, mint.Units...)
		tags = append(tags, tag)
	}
	tags = append(tags, nostr.Tag{"pubkey", p2pk})
	ev := nostr.Event{Kind: nip61.KindNutzapInfo, CreatedAt: nostr.Timestamp(fixedNow.Unix()), Tags: tags}
	if err := ev.Sign(recipient); err != nil {
		t.Fatal(err)
	}
	return ev
}

func newSendHarness(t *testing.T) (*nip61.Client, *store, nostr.SecretKey, nostr.SecretKey, string, *secp256k1.PrivateKey, *[][]string) {
	t.Helper()
	sender, recipient := nostrKey(t, "3"), nostrKey(t, "2")
	p2pk, _ := p2pkXOnly()
	mintKey := secp256k1.PrivKeyFromBytes(bytes32(5))
	s := &store{}
	s.events = append(s.events, signedInfo(t, recipient, []string{"wss://wallet.example"}, []nip61.MintInfo{{URL: "https://mint.example", Units: []string{"sat"}}}, p2pk))
	routes := &[][]string{}
	client := nip61.NewClient(signer{sender}, s.publish, s.query,
		nip61.WithClock(func() time.Time { return fixedNow }),
		nip61.WithMintKeyResolver(func(context.Context, string, string, string, int) (string, error) {
			return hex.EncodeToString(mintKey.PubKey().SerializeCompressed()), nil
		}),
		nip61.WithRouting(func(ctx context.Context, relays []string, filter nostr.Filter) ([]*nostr.Event, error) {
			return s.query(ctx, filter)
		},
			func(_ context.Context, relays []string, ev nostr.Event) error {
				*routes = append(*routes, append([]string(nil), relays...))
				return s.publish(context.Background(), ev)
			},
			func(_ context.Context, _ string, purpose nip61.RelayPurpose) ([]string, error) {
				if purpose == nip61.RelayWrite {
					return []string{"wss://author.example"}, nil
				}
				return []string{"wss://read.example"}, nil
			}))
	return client, s, sender, recipient, p2pk, mintKey, routes
}

func TestPublishNutzapInfoRejectsZeroMints(t *testing.T) {
	sk := nostrKey(t, "2")
	p2pk, _ := p2pkXOnly()
	client := nip61.NewClient(signer{sk}, func(context.Context, nostr.Event) error { return nil }, func(context.Context, nostr.Filter) ([]*nostr.Event, error) { return nil, nil }, nip61.WithClock(func() time.Time { return fixedNow }))
	if _, err := client.PublishNutzapInfo(context.Background(), nil, p2pk, "sat"); err == nil {
		t.Fatal("expected zero-mint rejection")
	}
}

func TestPublishAndFetchNutzapInfoNormalizesAndDeduplicates(t *testing.T) {
	sk := nostrKey(t, "2")
	p2pk, _ := p2pkXOnly()
	s := &store{}
	client := nip61.NewClient(signer{sk}, s.publish, s.query, nip61.WithClock(func() time.Time { return fixedNow }))
	ev, err := client.PublishNutzapInfoWithRelays(context.Background(), []string{"wss://Relay.example/", "wss://relay.example"}, []nip61.MintInfo{{URL: "https://MINT.example/", Units: []string{"SAT"}}, {URL: "https://mint.example", Units: []string{"usd", "sat"}}}, p2pk, "sat")
	if err != nil {
		t.Fatal(err)
	}
	info, _, err := client.FetchNutzapInfo(context.Background(), ev.PubKey.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Mints) != 1 || info.Mints[0].URL != "https://mint.example" || len(info.Mints[0].Units) != 2 || len(info.Relays) != 1 || info.P2PKPubkey != p2pk {
		t.Fatalf("unexpected normalized info: %+v", info)
	}
}

func TestSendDiscoversInfoValidatesProofAndRoutes(t *testing.T) {
	client, _, _, recipient, p2pk, mintKey, routes := newSendHarness(t)
	proof := makeProof(t, p2pk, mintKey, 1)
	ev, err := client.SendNutzap(context.Background(), nostr.GetPublicKey(recipient).Hex(), "https://MINT.example/", []nip61.Proof{proof}, "thanks", "")
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != nip61.KindNutzap || !ev.CheckID() || !ev.VerifySignature() {
		t.Fatalf("invalid event: %+v", ev)
	}
	if len(*routes) != 1 || len((*routes)[0]) != 1 || (*routes)[0][0] != "wss://wallet.example" {
		t.Fatalf("unexpected publish routes: %v", *routes)
	}
	validated, err := client.ValidateNutzap(context.Background(), nostr.GetPublicKey(recipient).Hex(), ev)
	if err != nil {
		t.Fatal(err)
	}
	if validated.Amount != 1 || validated.Mint != "https://mint.example" || validated.RecipientPubkeyHex != nostr.GetPublicKey(recipient).Hex() {
		t.Fatalf("unexpected validated nutzap: %+v", validated)
	}
}

func TestSendRejectsUnadvertisedMintAndWrongP2PK(t *testing.T) {
	client, _, _, recipient, p2pk, mintKey, _ := newSendHarness(t)
	proof := makeProof(t, p2pk, mintKey, 1)
	if _, err := client.SendNutzap(context.Background(), nostr.GetPublicKey(recipient).Hex(), "https://other.example", []nip61.Proof{proof}, "", ""); err == nil {
		t.Fatal("expected mint allowlist rejection")
	}
	wrong, _ := p2pkXOnly()
	wrong = strings.Repeat("0", 63) + "1"
	proof = makeProof(t, wrong, mintKey, 1)
	if _, err := client.SendNutzap(context.Background(), nostr.GetPublicKey(recipient).Hex(), "https://mint.example", []nip61.Proof{proof}, "", ""); err == nil || !strings.Contains(err.Error(), "02 prefix") {
		t.Fatalf("expected P2PK lock rejection, got %v", err)
	}
}

func TestValidateNutzapRejectsTamperedDLEQ(t *testing.T) {
	client, _, sender, recipient, p2pk, mintKey, _ := newSendHarness(t)
	proof := makeProof(t, p2pk, mintKey, 1)
	proof.DLEQ.E = strings.Repeat("1", 64)
	event := nutzapEvent(t, sender, nostr.GetPublicKey(recipient).Hex(), proof)
	if _, err := client.ValidateNutzap(context.Background(), nostr.GetPublicKey(recipient).Hex(), &event); err == nil || !strings.Contains(err.Error(), "DLEQ") {
		t.Fatalf("expected DLEQ rejection, got %v", err)
	}
}

func TestInvalidEnvelopeRejectedBeforeMintKeyResolution(t *testing.T) {
	client, _, sender, recipient, p2pk, mintKey, _ := newSendHarness(t)
	proof := makeProof(t, p2pk, mintKey, 1)
	event := nutzapEvent(t, sender, nostr.GetPublicKey(recipient).Hex(), proof)
	event.Content = "tampered"
	calls := 0
	client = nip61.NewClient(signer{sender}, func(context.Context, nostr.Event) error { return nil }, func(context.Context, nostr.Filter) ([]*nostr.Event, error) { return nil, nil },
		nip61.WithClock(func() time.Time { return fixedNow }), nip61.WithMintKeyResolver(func(context.Context, string, string, string, int) (string, error) { calls++; return "", nil }))
	if _, err := client.ValidateNutzap(context.Background(), nostr.GetPublicKey(recipient).Hex(), &event); err == nil {
		t.Fatal("expected invalid envelope rejection")
	}
	if calls != 0 {
		t.Fatalf("mint key resolver called %d times", calls)
	}
}

func TestVerifyProofDLEQOfficialVector(t *testing.T) {
	proof := nip61.Proof{Amount: 1, ID: "00882760bfa2eb41", Secret: "daf4dd00a2b68a0858a80450f52c8a7d2ccf87d375e43e216e0c571f089f63e9", C: "024369d2d22a80ecf78f3937da9d5f30c1b9f74f0c32684d583cca0fa6a61cdcfc", DLEQ: &nip61.DLEQProof{E: "b31e58ac6527f34975ffab13e70a48b6d2b0d35abc4b03f0151f09ee1a9763d4", S: "8fbae004c59e754d71df67e392b6ae4e29293113ddc2ec86592a0431d16306d8", R: "a6d13fcd7a18442e6076f5e1e7c887ad5de40a019824bdfa9fe740d302e8d861"}}
	if err := nip61.VerifyProofDLEQ(proof, "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"); err != nil {
		t.Fatalf("official NUT-12 vector failed: %v", err)
	}
}

type historyStub struct {
	relays                    []string
	amount                    int
	created, redeemed, sender string
}

func (h *historyStub) PublishNutzapRedemption(_ context.Context, relays []string, amount int, _ string, created, _ string, redeemed, _ string, sender string) (*nostr.Event, error) {
	h.relays, h.amount, h.created, h.redeemed, h.sender = append([]string(nil), relays...), amount, created, redeemed, sender
	return &nostr.Event{Kind: nip61.KindTokenHistory}, nil
}

func TestRedemptionWorkflowPublishesMarkerToSenderReadRelays(t *testing.T) {
	client, s, sender, recipient, p2pk, mintKey, _ := newSendHarness(t)
	proof := makeProof(t, p2pk, mintKey, 1)
	nutzap := nutzapEvent(t, sender, nostr.GetPublicKey(recipient).Hex(), proof)
	s.events = append(s.events, nutzap)
	history := &historyStub{}
	client = nip61.NewClient(signer{recipient}, s.publish, s.query,
		nip61.WithClock(func() time.Time { return fixedNow }),
		nip61.WithMintKeyResolver(func(context.Context, string, string, string, int) (string, error) {
			return hex.EncodeToString(mintKey.PubKey().SerializeCompressed()), nil
		}),
		nip61.WithRedemptionHistoryPublisher(history),
		nip61.WithRouting(func(ctx context.Context, _ []string, filter nostr.Filter) ([]*nostr.Event, error) {
			return s.query(ctx, filter)
		}, nil,
			func(_ context.Context, pubkey string, purpose nip61.RelayPurpose) ([]string, error) {
				if pubkey == nostr.GetPublicKey(sender).Hex() && purpose == nip61.RelayRead {
					return []string{"wss://sender-read.example"}, nil
				}
				return []string{"wss://recipient-write.example"}, nil
			}))
	created := nostr.Event{Kind: nip61.KindToken, CreatedAt: nostr.Timestamp(fixedNow.Unix()), Content: "encrypted"}
	if err := created.Sign(recipient); err != nil {
		t.Fatal(err)
	}
	result, err := client.RedeemNutzap(context.Background(), nostr.GetPublicKey(recipient).Hex(), &nutzap, func(context.Context, *nip61.ReceivedNutzap) (*nostr.Event, error) { return &created, nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.History == nil || len(history.relays) != 1 || history.relays[0] != "wss://sender-read.example" || history.amount != 1 || history.created != created.ID.Hex() || history.redeemed != nutzap.ID.Hex() || history.sender != nostr.GetPublicKey(sender).Hex() {
		t.Fatalf("unexpected redemption history: result=%+v history=%+v", result, history)
	}
}

func TestRedemptionStopsWhenSwapFails(t *testing.T) {
	client, _, sender, recipient, p2pk, mintKey, _ := newSendHarness(t)
	nutzap := nutzapEvent(t, sender, nostr.GetPublicKey(recipient).Hex(), makeProof(t, p2pk, mintKey, 1))
	result, err := client.RedeemNutzap(context.Background(), nostr.GetPublicKey(recipient).Hex(), &nutzap, func(context.Context, *nip61.ReceivedNutzap) (*nostr.Event, error) { return nil, errors.New("spent") })
	if err == nil || result == nil || result.CreatedToken != nil {
		t.Fatalf("unexpected result=%+v err=%v", result, err)
	}
}

func nutzapEvent(t *testing.T, sender nostr.SecretKey, recipient string, proof nip61.Proof) nostr.Event {
	t.Helper()
	proofJSON, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	ev := nostr.Event{Kind: nip61.KindNutzap, CreatedAt: nostr.Timestamp(fixedNow.Unix()), Content: "hello", Tags: nostr.Tags{{"unit", "sat"}, {"u", "https://mint.example"}, {"p", recipient}, {"proof", string(proofJSON)}}}
	if err := ev.Sign(sender); err != nil {
		t.Fatal(err)
	}
	return ev
}

func makeProof(t *testing.T, p2pk string, mintKey *secp256k1.PrivateKey, amount int) nip61.Proof {
	t.Helper()
	secretBytes, err := json.Marshal([]any{"P2PK", map[string]any{"nonce": strings.Repeat("a", 64), "data": "02" + p2pk, "tags": [][]string{{"sigflag", "SIG_INPUTS"}}}})
	if err != nil {
		t.Fatal(err)
	}
	secret := string(secretBytes)
	r := secp256k1.PrivKeyFromBytes(bytes32(6))
	j := secp256k1.PrivKeyFromBytes(bytes32(7))
	y := hashToCurve(t, []byte(secret))
	b := add(t, y, r.PubKey())
	cBlind := multiply(t, &mintKey.Key, b)
	var negR secp256k1.ModNScalar
	negR.NegateVal(&r.Key)
	c := add(t, cBlind, multiply(t, &negR, mintKey.PubKey()))
	r1 := j.PubKey()
	r2 := multiply(t, &j.Key, b)
	eBytes := challenge(r1, r2, mintKey.PubKey(), cBlind)
	var e secp256k1.ModNScalar
	if e.SetByteSlice(eBytes[:]) {
		t.Fatal("challenge overflow")
	}
	ea := e
	ea.Mul(&mintKey.Key)
	s := j.Key
	s.Add(&ea)
	sBytes := s.Bytes()
	return nip61.Proof{Amount: amount, ID: "keyset-1", Secret: secret, C: hex.EncodeToString(c.SerializeCompressed()), DLEQ: &nip61.DLEQProof{E: hex.EncodeToString(eBytes[:]), S: hex.EncodeToString(sBytes[:]), R: hex.EncodeToString(r.Serialize())}}
}

func bytes32(last byte) []byte { b := make([]byte, 32); b[31] = last; return b }

func hashToCurve(t *testing.T, message []byte) *secp256k1.PublicKey {
	t.Helper()
	seed := sha256.Sum256(append([]byte("Secp256k1_HashToCurve_Cashu_"), message...))
	for counter := uint32(0); counter < 1<<16; counter++ {
		var suffix [4]byte
		binary.LittleEndian.PutUint32(suffix[:], counter)
		candidate := sha256.Sum256(append(append([]byte(nil), seed[:]...), suffix[:]...))
		if point, err := secp256k1.ParsePubKey(append([]byte{0x02}, candidate[:]...)); err == nil {
			return point
		}
	}
	t.Fatal("hash to curve failed")
	return nil
}

func multiply(t *testing.T, scalar *secp256k1.ModNScalar, point *secp256k1.PublicKey) *secp256k1.PublicKey {
	t.Helper()
	var in, out secp256k1.JacobianPoint
	point.AsJacobian(&in)
	secp256k1.ScalarMultNonConst(scalar, &in, &out)
	out.ToAffine()
	return secp256k1.NewPublicKey(&out.X, &out.Y)
}
func add(t *testing.T, left, right *secp256k1.PublicKey) *secp256k1.PublicKey {
	t.Helper()
	var a, b, out secp256k1.JacobianPoint
	left.AsJacobian(&a)
	right.AsJacobian(&b)
	secp256k1.AddNonConst(&a, &b, &out)
	out.ToAffine()
	return secp256k1.NewPublicKey(&out.X, &out.Y)
}
func challenge(points ...*secp256k1.PublicKey) [32]byte {
	var encoded strings.Builder
	for _, point := range points {
		encoded.WriteString(hex.EncodeToString(point.SerializeUncompressed()))
	}
	return sha256.Sum256([]byte(encoded.String()))
}
