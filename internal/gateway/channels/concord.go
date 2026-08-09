package channels

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip44"
	"github.com/btcsuite/btcd/btcec/v2"
	"golang.org/x/crypto/hkdf"

	metricspkg "metiq/internal/metrics"
	okpublish "metiq/internal/nostr/publish"
	nostruntime "metiq/internal/nostr/runtime"
)

const (
	ConcordWrapKind           nostr.Kind = 1059
	ConcordSealEncryptedKind  nostr.Kind = 20013
	ConcordSealPlaintextKind  nostr.Kind = 20014
	ConcordControlEditionKind nostr.Kind = 3308
	ConcordGuestbookKind      nostr.Kind = 3306
	ConcordKickKind           nostr.Kind = 3309
	ConcordSnapshotKind       nostr.Kind = 3312
	ConcordDirectInviteKind   nostr.Kind = 3313

	concordMaxPlaintextBytes = 65535
	concordMaxInviteChannels = 256
	concordFutureSkew        = time.Hour
)

const (
	concordPermissionManageRoles uint64 = 1 << iota
	concordPermissionManageChannels
	concordPermissionManageMetadata
	concordPermissionKick
	concordPermissionBan
	concordPermissionManageMessages
	concordPermissionCreateInvite
)

const concordOwnerPermissions = ^uint64(0)

var concordZeroID = strings.Repeat("0", 64)

// ConcordChannelKeyEntry is a private-channel key delivered in CORD-05 join material.
type ConcordChannelKeyEntry struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Epoch uint64 `json:"epoch"`
	Name  string `json:"name,omitempty"`
}

// ConcordJoinMaterial is the self-certifying membership subset of a CORD-05 bundle.
type ConcordJoinMaterial struct {
	CommunityID   string                   `json:"community_id"`
	Owner         string                   `json:"owner"`
	OwnerSalt     string                   `json:"owner_salt"`
	CommunityRoot string                   `json:"community_root"`
	RootEpoch     uint64                   `json:"root_epoch"`
	ControlPK     string                   `json:"control_pk,omitempty"`
	Channels      []ConcordChannelKeyEntry `json:"channels"`
	Relays        []string                 `json:"relays,omitempty"`
	Name          string                   `json:"name,omitempty"`
	ExpiresAt     int64                    `json:"expires_at,omitempty"`
	CreatorPubKey string                   `json:"creator_npub,omitempty"`
	Label         string                   `json:"label,omitempty"`
}

// ParseConcordJoinMaterial validates and self-certifies CORD-05 key material.
func ParseConcordJoinMaterial(raw string) (ConcordJoinMaterial, error) {
	var material ConcordJoinMaterial
	if len(raw) > concordMaxPlaintextBytes {
		return material, fmt.Errorf("concord join material exceeds the NIP-44 plaintext cap")
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&material); err != nil {
		return material, fmt.Errorf("decode concord join material: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return material, fmt.Errorf("decode concord join material: trailing data")
	}
	if err := validateConcordHex(material.CommunityID, "community_id"); err != nil {
		return material, err
	}
	if err := validateConcordHex(material.Owner, "owner"); err != nil {
		return material, err
	}
	if err := validateConcordHex(material.OwnerSalt, "owner_salt"); err != nil {
		return material, err
	}
	if err := validateConcordHex(material.CommunityRoot, "community_root"); err != nil {
		return material, err
	}
	if material.ControlPK != "" {
		if err := validateConcordHex(material.ControlPK, "control_pk"); err != nil {
			return material, err
		}
	}
	want, err := ComputeConcordCommunityID(material.Owner, material.OwnerSalt)
	if err != nil {
		return material, err
	}
	if want != material.CommunityID {
		return material, fmt.Errorf("concord community_id is not self-certifying")
	}
	if len(material.Channels) > concordMaxInviteChannels {
		return material, fmt.Errorf("concord join material has more than %d channels", concordMaxInviteChannels)
	}
	seen := make(map[string]struct{}, len(material.Channels))
	for i := range material.Channels {
		entry := &material.Channels[i]
		if err := validateConcordHex(entry.ID, fmt.Sprintf("channels[%d].id", i)); err != nil {
			return material, err
		}
		if err := validateConcordHex(entry.Key, fmt.Sprintf("channels[%d].key", i)); err != nil {
			return material, err
		}
		if _, exists := seen[entry.ID]; exists {
			return material, fmt.Errorf("duplicate concord channel id %s", entry.ID)
		}
		seen[entry.ID] = struct{}{}
		entry.Name = strings.TrimSpace(entry.Name)
	}
	relays, err := normalizeConcordRelays(material.Relays)
	if err != nil {
		return material, fmt.Errorf("concord join material relays: %w", err)
	}
	material.Relays = relays
	if material.CreatorPubKey != "" {
		if err := validateConcordHex(material.CreatorPubKey, "creator_npub"); err != nil {
			return material, err
		}
	}
	return material, nil
}

// ComputeConcordCommunityID implements CORD-02 Appendix A.4.
func ComputeConcordCommunityID(ownerHex, saltHex string) (string, error) {
	if err := validateConcordHex(ownerHex, "owner"); err != nil {
		return "", err
	}
	if err := validateConcordHex(saltHex, "owner_salt"); err != nil {
		return "", err
	}
	owner, _ := hex.DecodeString(ownerHex)
	salt, _ := hex.DecodeString(saltHex)
	h := sha256.New()
	_, _ = h.Write([]byte("concord/community"))
	_, _ = h.Write(owner)
	_, _ = h.Write(salt)
	return hex.EncodeToString(h.Sum(nil)), nil
}

type concordPlaneKeys struct {
	sk      nostr.SecretKey
	pk      nostr.PubKey
	convKey [32]byte
}

func concordHKDF(secret []byte, label, idHex string, epoch *uint64, counter *byte) ([]byte, error) {
	id, err := hex.DecodeString(idHex)
	if err != nil || len(id) != 32 {
		return nil, fmt.Errorf("invalid concord derivation id")
	}
	info := make([]byte, 0, len(label)+1+32+9)
	info = append(info, label...)
	info = append(info, 0)
	info = append(info, id...)
	if epoch != nil {
		var encoded [8]byte
		binary.BigEndian.PutUint64(encoded[:], *epoch)
		info = append(info, encoded[:]...)
	}
	if counter != nil {
		info = append(info, *counter)
	}
	out := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, secret, nil, info), out); err != nil {
		return nil, err
	}
	return out, nil
}

func concordGroupKey(label string, secret []byte, idHex string, epoch *uint64) (concordPlaneKeys, error) {
	for attempt := 0; attempt <= 256; attempt++ {
		var counter *byte
		var counterValue byte
		if attempt > 0 {
			counterValue = byte(attempt - 1)
			counter = &counterValue
		}
		seed, err := concordHKDF(secret, label, idHex, epoch, counter)
		if err != nil {
			return concordPlaneKeys{}, err
		}
		var scalar btcec.ModNScalar
		if scalar.SetByteSlice(seed) || scalar.IsZero() {
			continue
		}
		var sk nostr.SecretKey
		copy(sk[:], seed)
		pk := sk.Public()
		conv, err := nip44.GenerateConversationKey(pk, sk)
		if err != nil {
			continue
		}
		return concordPlaneKeys{sk: sk, pk: pk, convKey: conv}, nil
	}
	return concordPlaneKeys{}, fmt.Errorf("concord scalar normalization failed")
}

func concordEntityCoordinate(label, communityID, idHex string) (string, error) {
	secret, err := hex.DecodeString(communityID)
	if err != nil {
		return "", err
	}
	plane, err := concordGroupKey(label, secret, idHex, nil)
	if err != nil {
		return "", err
	}
	return plane.pk.Hex(), nil
}

type concordRumor struct {
	ID        string     `json:"id"`
	PubKey    string     `json:"pubkey"`
	CreatedAt int64      `json:"created_at"`
	Kind      int        `json:"kind"`
	Tags      [][]string `json:"tags"`
	Content   string     `json:"content"`
}

func (r *concordRumor) recomputeID() error {
	pk, err := nostr.PubKeyFromHex(r.PubKey)
	if err != nil {
		return err
	}
	tags := make(nostr.Tags, len(r.Tags))
	for i := range r.Tags {
		tags[i] = append(nostr.Tag(nil), r.Tags[i]...)
	}
	event := nostr.Event{PubKey: pk, CreatedAt: nostr.Timestamp(r.CreatedAt), Kind: nostr.Kind(r.Kind), Tags: tags, Content: r.Content}
	r.ID = event.GetID().Hex()
	return nil
}

func parseConcordRumor(raw string) (concordRumor, error) {
	if len(raw) > concordMaxPlaintextBytes {
		return concordRumor{}, fmt.Errorf("concord rumor exceeds the NIP-44 plaintext cap")
	}
	var rumor concordRumor
	if err := json.Unmarshal([]byte(raw), &rumor); err != nil {
		return rumor, err
	}
	if err := validateConcordHex(rumor.PubKey, "rumor pubkey"); err != nil {
		return rumor, err
	}
	if rumor.Kind < 0 || rumor.CreatedAt < 0 {
		return rumor, fmt.Errorf("invalid concord rumor kind or timestamp")
	}
	for _, tag := range rumor.Tags {
		if len(tag) == 0 {
			return rumor, fmt.Errorf("invalid empty concord rumor tag")
		}
	}
	if err := rumor.recomputeID(); err != nil {
		return rumor, err
	}
	return rumor, nil
}

func openConcordStreamWrap(wrap nostr.Event, address concordPlaneKeys, sealKind nostr.Kind) (concordRumor, bool) {
	if wrap.Kind != ConcordWrapKind || wrap.PubKey != address.pk || !wrap.VerifySignature() {
		return concordRumor{}, false
	}
	sealJSON, err := nip44.Decrypt(wrap.Content, address.convKey)
	if err != nil || len(sealJSON) > concordMaxPlaintextBytes {
		return concordRumor{}, false
	}
	var seal nostr.Event
	if json.Unmarshal([]byte(sealJSON), &seal) != nil || seal.Kind != sealKind || !seal.VerifySignature() {
		return concordRumor{}, false
	}
	raw := seal.Content
	if sealKind == ConcordSealEncryptedKind {
		raw, err = nip44.Decrypt(seal.Content, address.convKey)
		if err != nil {
			return concordRumor{}, false
		}
	}
	rumor, err := parseConcordRumor(raw)
	if err != nil || rumor.PubKey != seal.PubKey.Hex() {
		return concordRumor{}, false
	}
	return rumor, true
}

func wrapConcordRumor(ctx context.Context, rumor concordRumor, sealKind nostr.Kind, plane concordPlaneKeys, signer nostr.Keyer) (nostr.Event, error) {
	if err := rumor.recomputeID(); err != nil {
		return nostr.Event{}, err
	}
	raw, err := json.Marshal(rumor)
	if err != nil || len(raw) > concordMaxPlaintextBytes {
		return nostr.Event{}, fmt.Errorf("concord rumor exceeds the NIP-44 plaintext cap")
	}
	content := string(raw)
	if sealKind == ConcordSealEncryptedKind {
		content, err = nip44.Encrypt(content, plane.convKey)
		if err != nil {
			return nostr.Event{}, err
		}
	}
	seal := nostr.Event{Kind: sealKind, CreatedAt: nostr.Timestamp(rumor.CreatedAt), Tags: nostr.Tags{}, Content: content}
	if err := signer.SignEvent(ctx, &seal); err != nil {
		return nostr.Event{}, fmt.Errorf("sign concord seal: %w", err)
	}
	if seal.PubKey.Hex() != rumor.PubKey {
		return nostr.Event{}, fmt.Errorf("concord seal signer does not match rumor author")
	}
	sealRaw, err := json.Marshal(seal)
	if err != nil || len(sealRaw) > concordMaxPlaintextBytes {
		return nostr.Event{}, fmt.Errorf("concord seal exceeds the NIP-44 plaintext cap")
	}
	wrapped, err := nip44.Encrypt(string(sealRaw), plane.convKey)
	if err != nil {
		return nostr.Event{}, err
	}
	eph := nostr.Generate().Public().Hex()
	wrap := nostr.Event{Kind: ConcordWrapKind, CreatedAt: seal.CreatedAt, Tags: nostr.Tags{{"p", eph}}, Content: wrapped}
	if err := wrap.Sign(plane.sk); err != nil {
		return nostr.Event{}, fmt.Errorf("sign concord stream wrap: %w", err)
	}
	return wrap, nil
}

type concordRole struct {
	position    int
	permissions uint64
}

type concordChannelDefinition struct {
	name    string
	private bool
	deleted bool
}

type concordVersion struct {
	version uint64
	rumorID string
}

type concordMembership struct {
	status  string
	atMS    int64
	rumorID string
}

// ConcordSecretResolver is satisfied by the process-wide secrets store.
type ConcordSecretResolver interface {
	Resolve(ref string) (value string, found bool)
}

// ResolveConcordKeyMaterial resolves a required env-backed key reference. Raw
// join material is deliberately rejected so community roots never live inline
// in config or RPC payloads.
func ResolveConcordKeyMaterial(ref string, resolver ConcordSecretResolver) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	if !(strings.HasPrefix(ref, "$") || strings.HasPrefix(ref, "env:") || strings.HasPrefix(ref, "secret:")) {
		return "", fmt.Errorf("concord keys must be an env/secret reference")
	}
	if resolver == nil {
		return "", fmt.Errorf("concord secret resolver is not configured")
	}
	value, found := resolver.Resolve(ref)
	if !found || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("concord key material reference %q did not resolve", ref)
	}
	return strings.TrimSpace(value), nil
}

// ConcordChannelOptions configure an encrypted Concord community channel.
type ConcordChannelOptions struct {
	CommunityID           string
	ChannelName           string
	ChannelID             string
	KeyMaterialJSON       string
	Relays                []string
	Hub                   *nostruntime.NostrHub
	Keyer                 nostr.Keyer
	OnMessage             func(InboundMessage)
	OnError               func(error)
	CommitmentEnforcement bool
}

// ConcordChannel implements the NIP-CAS-0008 optional encrypted community plane.
type ConcordChannel struct {
	id                    string
	communityID           string
	channelName           string
	channelID             string
	relays                []string
	pool                  *nostr.Pool
	publisher             okpublish.Publisher
	ownsPool              bool
	keyer                 nostr.Keyer
	pubkey                string
	onMessage             func(InboundMessage)
	onErr                 func(error)
	commitmentEnforcement bool

	ctx           context.Context
	cancel        context.CancelFunc
	closeOnce     sync.Once
	wg            sync.WaitGroup
	subsMu        sync.Mutex
	controlCancel context.CancelFunc
	chatCancel    context.CancelFunc

	stateMu     sync.RWMutex
	material    *ConcordJoinMaterial
	dissolved   bool
	joinSent    bool
	entities    map[string]concordVersion
	roles       map[string]concordRole
	grants      map[string]map[string]struct{}
	banlist     map[string]struct{}
	definitions map[string]concordChannelDefinition
	membership  map[string]concordMembership
	seen        *SeenCache
	liveSince   nostr.Timestamp
}

// NewConcordChannel validates configuration and starts invite/control/chat subscriptions.
func NewConcordChannel(parent context.Context, opts ConcordChannelOptions) (*ConcordChannel, error) {
	if err := validateConcordHex(opts.CommunityID, "community_id"); err != nil {
		return nil, err
	}
	if opts.ChannelID != "" {
		if err := validateConcordHex(opts.ChannelID, "channel_id"); err != nil {
			return nil, err
		}
	}
	relays, err := normalizeConcordRelays(opts.Relays)
	if err != nil {
		return nil, err
	}
	if len(relays) == 0 {
		return nil, fmt.Errorf("at least one relay is required for concord channel")
	}
	var keyer nostr.Keyer
	var pool *nostr.Pool
	ownsPool := false
	if opts.Hub != nil {
		keyer, pool = opts.Hub.Keyer(), opts.Hub.Pool()
	} else {
		if opts.Keyer == nil {
			return nil, fmt.Errorf("keyer is required (or provide Hub)")
		}
		keyer = opts.Keyer
		pool = nostruntime.NewPoolNIP42(keyer)
		ownsPool = true
	}
	pk, err := keyer.GetPublicKey(parent)
	if err != nil {
		if ownsPool {
			pool.Close("concord initialization failed")
		}
		return nil, fmt.Errorf("concord: get public key: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	c := &ConcordChannel{
		id: "concord:" + opts.CommunityID, communityID: opts.CommunityID,
		channelName: strings.TrimSpace(opts.ChannelName), channelID: opts.ChannelID,
		relays: relays, pool: pool, publisher: pool, ownsPool: ownsPool,
		keyer: keyer, pubkey: pk.Hex(), onMessage: opts.OnMessage, onErr: opts.OnError,
		commitmentEnforcement: opts.CommitmentEnforcement,
		ctx:                   ctx, cancel: cancel, entities: map[string]concordVersion{}, roles: map[string]concordRole{},
		grants: map[string]map[string]struct{}{}, banlist: map[string]struct{}{},
		definitions: map[string]concordChannelDefinition{}, membership: map[string]concordMembership{},
		seen: NewSeenCache(), liveSince: nostr.Now(),
	}
	if strings.TrimSpace(opts.KeyMaterialJSON) != "" {
		material, err := ParseConcordJoinMaterial(opts.KeyMaterialJSON)
		if err != nil {
			cancel()
			if ownsPool {
				pool.Close("concord initialization failed")
			}
			return nil, err
		}
		if material.CommunityID != opts.CommunityID {
			cancel()
			if ownsPool {
				pool.Close("concord initialization failed")
			}
			return nil, fmt.Errorf("concord key material community_id does not match channel")
		}
		c.material = &material
		c.relays = mergeConcordRelays(relays, material.Relays)
	}
	c.startSubscription(ctx, "invites", nostr.Filter{Kinds: []nostr.Kind{ConcordWrapKind}, Tags: nostr.TagMap{"p": []string{c.pubkey}, "k": []string{strconv.Itoa(int(ConcordDirectInviteKind))}}}, c.handleDirectInvite)
	if c.material != nil {
		c.startPlanes()
	}
	return c, nil
}

func (c *ConcordChannel) ID() string   { return c.id }
func (c *ConcordChannel) Type() string { return "concord" }

// Send publishes an encrypted kind-9 rumor to the configured Concord channel.
func (c *ConcordChannel) Send(ctx context.Context, text string) error {
	return c.send(ctx, text, "")
}

func (c *ConcordChannel) send(ctx context.Context, text, replyTo string) error {
	var commitmentBlocked bool
	text, commitmentBlocked = EnforceOutboundCommitment(ctx, text, c.commitmentEnforcement)
	if commitmentBlocked {
		metricspkg.RecordRoomSignal(c.id, metricspkg.RoomSignalCommitmentBlocked)
	}
	if text == "" {
		return fmt.Errorf("message is empty")
	}
	c.stateMu.RLock()
	if c.dissolved {
		c.stateMu.RUnlock()
		return fmt.Errorf("concord community is dissolved (read-only)")
	}
	if _, banned := c.banlist[c.pubkey]; banned {
		c.stateMu.RUnlock()
		return fmt.Errorf("concord publisher is banned")
	}
	material := cloneConcordMaterial(c.material)
	definitions := cloneConcordDefinitions(c.definitions)
	c.stateMu.RUnlock()
	if material == nil {
		return fmt.Errorf("concord key material is not available")
	}
	target, ok := c.resolveTarget(material, definitions)
	if !ok {
		return fmt.Errorf("concord target channel is not resolvable")
	}
	now := time.Now()
	tags := [][]string{{"channel", target.id}, {"epoch", strconv.FormatUint(target.epoch, 10)}, {"ms", strconv.Itoa(now.Nanosecond() / int(time.Millisecond))}}
	if replyTo != "" {
		if err := validateConcordHex(replyTo, "reply rumor id"); err == nil {
			tags = append(tags, []string{"q", replyTo})
		}
	}
	rumor := concordRumor{PubKey: c.pubkey, CreatedAt: now.Unix(), Kind: int(nostr.KindSimpleGroupChatMessage), Tags: tags, Content: text}
	wrap, err := wrapConcordRumor(ctx, rumor, ConcordSealEncryptedKind, target.plane, c.keyer)
	if err != nil {
		return err
	}
	if _, err := okpublish.PublishToAny(ctx, c.publisher, c.relays, wrap); err != nil {
		return fmt.Errorf("publish concord message: %w", err)
	}
	c.seen.Add(wrap.ID.Hex())
	return nil
}

func (c *ConcordChannel) Close() {
	c.closeOnce.Do(func() {
		c.cancel()
		c.wg.Wait()
		if c.ownsPool && c.pool != nil {
			c.pool.Close("concord channel closed")
		}
	})
}

type concordTarget struct {
	id    string
	epoch uint64
	plane concordPlaneKeys
}

func (c *ConcordChannel) resolveTarget(material *ConcordJoinMaterial, defs map[string]concordChannelDefinition) (concordTarget, bool) {
	id := c.channelID
	wanted := c.channelName
	if wanted == "" {
		wanted = "general"
	}
	if id == "" {
		for candidate, def := range defs {
			if !def.deleted && def.name == wanted {
				id = candidate
				break
			}
		}
	}
	var keyEntry *ConcordChannelKeyEntry
	for i := range material.Channels {
		entry := &material.Channels[i]
		if id != "" && entry.ID == id {
			keyEntry = entry
			break
		}
		if id == "" && ((c.channelName != "" && entry.Name == c.channelName) || c.channelName == "") {
			id, keyEntry = entry.ID, entry
			break
		}
	}
	if id == "" {
		return concordTarget{}, false
	}
	def, hasDef := defs[id]
	if hasDef && def.deleted {
		return concordTarget{}, false
	}
	isPrivate := keyEntry != nil
	if hasDef {
		isPrivate = def.private
	}
	var secret []byte
	var epoch uint64
	if isPrivate {
		if keyEntry == nil {
			return concordTarget{}, false
		}
		secret, _ = hex.DecodeString(keyEntry.Key)
		epoch = keyEntry.Epoch
	} else {
		secret, _ = hex.DecodeString(material.CommunityRoot)
		epoch = material.RootEpoch
	}
	plane, err := concordGroupKey("concord/channel", secret, id, &epoch)
	if err != nil {
		return concordTarget{}, false
	}
	return concordTarget{id: id, epoch: epoch, plane: plane}, true
}

func (c *ConcordChannel) derivePlanes(material *ConcordJoinMaterial) (control, dissolved, guestbook concordPlaneKeys, err error) {
	root, _ := hex.DecodeString(material.CommunityRoot)
	community, _ := hex.DecodeString(material.CommunityID)
	control, err = concordGroupKey("concord/control", root, material.CommunityID, &material.RootEpoch)
	if err != nil {
		return
	}
	if material.ControlPK != "" {
		control.pk, err = nostr.PubKeyFromHex(material.ControlPK)
		if err != nil {
			return
		}
	}
	dissolved, err = concordGroupKey("concord/dissolved", community, concordZeroID, nil)
	if err != nil {
		return
	}
	guestbook, err = concordGroupKey("concord/guestbook", root, material.CommunityID, &material.RootEpoch)
	return
}

func (c *ConcordChannel) startPlanes() {
	c.stateMu.RLock()
	material := cloneConcordMaterial(c.material)
	c.stateMu.RUnlock()
	if material == nil {
		return
	}
	control, dissolved, _, err := c.derivePlanes(material)
	if err != nil {
		c.emitErr(err)
		return
	}
	c.subsMu.Lock()
	if c.controlCancel != nil {
		c.controlCancel()
	}
	controlCtx, cancel := context.WithCancel(c.ctx)
	c.controlCancel = cancel
	c.subsMu.Unlock()
	filter := nostr.Filter{Kinds: []nostr.Kind{ConcordWrapKind}, Authors: []nostr.PubKey{control.pk, dissolved.pk}}
	c.startSubscription(controlCtx, "control", filter, func(re nostr.RelayEvent) bool {
		if re.PubKey == dissolved.pk {
			return c.handleDissolution(re.Event, dissolved)
		}
		return c.handleControl(re.Event, control)
	})
	c.restartChat()
}

func (c *ConcordChannel) restartChat() {
	c.stateMu.RLock()
	material := cloneConcordMaterial(c.material)
	defs := cloneConcordDefinitions(c.definitions)
	dissolved := c.dissolved
	c.stateMu.RUnlock()
	if material == nil || dissolved {
		return
	}
	_, _, guestbook, err := c.derivePlanes(material)
	if err != nil {
		c.emitErr(err)
		return
	}
	target, targetOK := c.resolveTarget(material, defs)
	authors := []nostr.PubKey{guestbook.pk}
	if targetOK {
		authors = append(authors, target.plane.pk)
	}
	c.subsMu.Lock()
	if c.chatCancel != nil {
		c.chatCancel()
	}
	chatCtx, cancel := context.WithCancel(c.ctx)
	c.chatCancel = cancel
	c.subsMu.Unlock()
	filter := nostr.Filter{Kinds: []nostr.Kind{ConcordWrapKind}, Authors: authors}
	c.startSubscription(chatCtx, "chat", filter, func(re nostr.RelayEvent) bool {
		if re.PubKey == guestbook.pk {
			return c.handleGuestbook(re.Event, guestbook)
		}
		if !targetOK || re.PubKey != target.plane.pk {
			return false
		}
		return c.handleChat(re, target)
	})
}

func (c *ConcordChannel) startSubscription(ctx context.Context, label string, filter nostr.Filter, handle func(nostr.RelayEvent) bool) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		backoff := channelReconnectInitialBackoff
		for ctx.Err() == nil {
			subCtx, cancel := context.WithCancel(ctx)
			events, closed := channelSubscribeManyNotifyClosed(subCtx, c.pool, c.relays, filter, nostr.SubscriptionOptions{Label: "concord-" + label})
			processed, consume := false, true
			for consume {
				select {
				case re, ok := <-events:
					if !ok {
						consume = false
						continue
					}
					if handle(re) {
						processed = true
					}
				case rc, ok := <-closed:
					if !ok {
						closed = nil
						continue
					}
					if ctx.Err() == nil && !rc.HandledAuth {
						c.emitErr(formatChannelClosed("concord "+label, rc))
					}
					if !rc.HandledAuth {
						consume = false
					}
				case <-subCtx.Done():
					consume = false
				}
			}
			cancel()
			if ctx.Err() != nil {
				return
			}
			if processed {
				backoff = channelReconnectInitialBackoff
			}
			if !channelReconnectDelay(ctx, backoff) {
				return
			}
			backoff = nextChannelReconnectBackoff(backoff)
		}
	}()
}

func (c *ConcordChannel) handleControl(event nostr.Event, plane concordPlaneKeys) bool {
	rumor, ok := openConcordStreamWrap(event, plane, ConcordSealPlaintextKind)
	if !ok {
		return false
	}
	edition, ok := parseConcordEdition(rumor)
	if !ok || c.seen.Add(event.ID.Hex()) || c.seen.Add(rumor.ID) {
		return false
	}
	c.stateMu.Lock()
	changed, channelsChanged := c.foldControlLocked(edition)
	c.stateMu.Unlock()
	if channelsChanged {
		c.restartChat()
	}
	return changed
}

type concordEdition struct {
	vsk                     int
	eid                     string
	version                 uint64
	actor, content, rumorID string
}

func parseConcordEdition(r concordRumor) (concordEdition, bool) {
	if nostr.Kind(r.Kind) != ConcordControlEditionKind {
		return concordEdition{}, false
	}
	vskRaw, ok1 := concordSingleTag(r.Tags, "vsk")
	eid, ok2 := concordSingleTag(r.Tags, "eid")
	if !ok1 || !ok2 || validateConcordHex(eid, "eid") != nil {
		return concordEdition{}, false
	}
	vsk, err := strconv.Atoi(vskRaw)
	if err != nil || vsk < 0 {
		return concordEdition{}, false
	}
	version := uint64(1)
	if vsk != 10 {
		ev, ok := concordSingleTag(r.Tags, "ev")
		if !ok {
			return concordEdition{}, false
		}
		version, err = strconv.ParseUint(ev, 10, 64)
		if err != nil || version < 1 {
			return concordEdition{}, false
		}
	}
	return concordEdition{vsk: vsk, eid: eid, version: version, actor: r.PubKey, content: r.Content, rumorID: r.ID}, true
}

func (c *ConcordChannel) permissionsLocked(pubkey string) uint64 {
	if c.material != nil && pubkey == c.material.Owner {
		return concordOwnerPermissions
	}
	var permissions uint64
	for roleID := range c.grants[pubkey] {
		permissions |= c.roles[roleID].permissions
	}
	return permissions
}

func (c *ConcordChannel) positionLocked(pubkey string) int {
	if c.material != nil && pubkey == c.material.Owner {
		return 0
	}
	position := int(^uint(0) >> 1)
	for roleID := range c.grants[pubkey] {
		if role, ok := c.roles[roleID]; ok && role.position < position {
			position = role.position
		}
	}
	return position
}

func (c *ConcordChannel) foldControlLocked(ed concordEdition) (bool, bool) {
	if c.material == nil {
		return false, false
	}
	owner := c.material.Owner
	var required uint64
	switch ed.vsk {
	case 0:
		required = concordPermissionManageMetadata
	case 1, 3:
		if ed.actor != owner {
			return false, false
		}
	case 2:
		required = concordPermissionManageChannels
	case 4:
		required = concordPermissionBan
	default:
		return false, false
	}
	if required != 0 && c.permissionsLocked(ed.actor)&required == 0 {
		return false, false
	}
	if current, ok := c.entities[ed.eid]; ok && (ed.version < current.version || (ed.version == current.version && ed.rumorID >= current.rumorID)) {
		return false, false
	}
	channelsChanged := false
	switch ed.vsk {
	case 0:
		if ed.eid != c.communityID {
			return false, false
		}
	case 1:
		var payload struct {
			RoleID      string          `json:"role_id"`
			Position    int             `json:"position"`
			Permissions json.RawMessage `json:"permissions"`
		}
		if json.Unmarshal([]byte(ed.content), &payload) != nil || payload.RoleID != ed.eid || payload.Position < 1 {
			return false, false
		}
		bits, ok := parseConcordPermissions(payload.Permissions)
		if !ok {
			return false, false
		}
		c.roles[payload.RoleID] = concordRole{position: payload.Position, permissions: bits}
	case 2:
		var payload struct {
			Name    string `json:"name"`
			Private bool   `json:"private"`
			Deleted bool   `json:"deleted"`
		}
		if json.Unmarshal([]byte(ed.content), &payload) != nil {
			return false, false
		}
		c.definitions[ed.eid] = concordChannelDefinition{name: payload.Name, private: payload.Private, deleted: payload.Deleted}
		channelsChanged = true
	case 3:
		var payload struct {
			Member  string   `json:"member"`
			RoleIDs []string `json:"role_ids"`
		}
		if json.Unmarshal([]byte(ed.content), &payload) != nil || validateConcordHex(payload.Member, "grant member") != nil {
			return false, false
		}
		coordinate, err := concordEntityCoordinate("concord/grant", c.communityID, payload.Member)
		if err != nil || ed.eid != coordinate {
			return false, false
		}
		roles := map[string]struct{}{}
		for _, id := range payload.RoleIDs {
			if validateConcordHex(id, "role id") == nil {
				roles[id] = struct{}{}
			}
		}
		if len(roles) == 0 {
			delete(c.grants, payload.Member)
		} else {
			c.grants[payload.Member] = roles
		}
	case 4:
		coordinate, err := concordEntityCoordinate("concord/banlist", c.communityID, concordZeroID)
		if err != nil || ed.eid != coordinate {
			return false, false
		}
		var entries []string
		if json.Unmarshal([]byte(ed.content), &entries) != nil {
			return false, false
		}
		c.banlist = map[string]struct{}{}
		for _, pk := range entries {
			if validateConcordHex(pk, "banlist pubkey") == nil {
				c.banlist[pk] = struct{}{}
			}
		}
	}
	c.entities[ed.eid] = concordVersion{version: ed.version, rumorID: ed.rumorID}
	return true, channelsChanged
}

func (c *ConcordChannel) handleDissolution(event nostr.Event, plane concordPlaneKeys) bool {
	rumor, ok := openConcordStreamWrap(event, plane, ConcordSealPlaintextKind)
	if !ok || nostr.Kind(rumor.Kind) != ConcordControlEditionKind {
		return false
	}
	vsk, vok := concordSingleTag(rumor.Tags, "vsk")
	eid, eok := concordSingleTag(rumor.Tags, "eid")
	if !vok || !eok || vsk != "10" || eid != c.communityID || c.seen.Add(event.ID.Hex()) || c.seen.Add(rumor.ID) {
		return false
	}
	c.stateMu.Lock()
	valid := c.material != nil && rumor.PubKey == c.material.Owner
	if valid {
		c.dissolved = true
	}
	c.stateMu.Unlock()
	if valid {
		c.subsMu.Lock()
		if c.controlCancel != nil {
			c.controlCancel()
		}
		if c.chatCancel != nil {
			c.chatCancel()
		}
		c.subsMu.Unlock()
	}
	return valid
}

func (c *ConcordChannel) handleGuestbook(event nostr.Event, plane concordPlaneKeys) bool {
	rumor, ok := openConcordStreamWrap(event, plane, ConcordSealEncryptedKind)
	if !ok {
		return false
	}
	atMS, ok := concordRumorInstant(rumor)
	if !ok || c.seen.Add(event.ID.Hex()) || c.seen.Add(rumor.ID) {
		return false
	}
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	switch nostr.Kind(rumor.Kind) {
	case ConcordGuestbookKind:
		if rumor.Content != "join" && rumor.Content != "leave" {
			return false
		}
		c.coalesceMembershipLocked(rumor.PubKey, rumor.Content, atMS, rumor.ID)
		return true
	case ConcordKickKind:
		target, ok := concordSingleTag(rumor.Tags, "p")
		if !ok || validateConcordHex(target, "kick target") != nil || c.material == nil || target == c.material.Owner {
			return false
		}
		if c.permissionsLocked(rumor.PubKey)&concordPermissionKick == 0 || c.positionLocked(rumor.PubKey) >= c.positionLocked(target) {
			return false
		}
		c.coalesceMembershipLocked(target, "kick", atMS, rumor.ID)
		return true
	case ConcordSnapshotKind:
		return false
	}
	return false
}

func (c *ConcordChannel) handleChat(re nostr.RelayEvent, target concordTarget) bool {
	rumor, ok := openConcordStreamWrap(re.Event, target.plane, ConcordSealEncryptedKind)
	if !ok || nostr.Kind(rumor.Kind) != nostr.KindSimpleGroupChatMessage {
		return false
	}
	channel, cok := concordSingleTag(rumor.Tags, "channel")
	epoch, eok := concordSingleTag(rumor.Tags, "epoch")
	if !cok || !eok || channel != target.id || epoch != strconv.FormatUint(target.epoch, 10) {
		return false
	}
	if c.seen.Add(re.ID.Hex()) || c.seen.Add(rumor.ID) {
		return false
	}
	if rumor.PubKey == c.pubkey {
		return true
	}
	c.stateMu.Lock()
	if c.dissolved {
		c.stateMu.Unlock()
		return false
	}
	if _, banned := c.banlist[rumor.PubKey]; banned {
		c.stateMu.Unlock()
		return false
	}
	atMS, validInstant := concordRumorInstant(rumor)
	if !validInstant {
		atMS = rumor.CreatedAt * 1000
	}
	current := c.membership[rumor.PubKey]
	if current.status != "join" && current.atMS < atMS {
		c.membership[rumor.PubKey] = concordMembership{status: "join", atMS: atMS, rumorID: rumor.ID}
	}
	c.stateMu.Unlock()
	if c.onMessage == nil {
		return true
	}
	tags := make(nostr.Tags, len(rumor.Tags))
	for i := range rumor.Tags {
		tags[i] = append(nostr.Tag(nil), rumor.Tags[i]...)
	}
	ev := nostr.Event{CreatedAt: nostr.Timestamp(rumor.CreatedAt), Tags: tags}
	meta := extractNIP29Meta(ev, rumor.ID, c.liveSince)
	if replyID, ok := concordSingleTag(rumor.Tags, "q"); ok && validateConcordHex(replyID, "q") == nil {
		meta.ReplyToEventID = replyID
		meta.ThreadRootEventID = replyID
	}
	relay := ""
	if re.Relay != nil {
		relay = re.Relay.URL
	}
	msg := InboundMessage{ChannelID: c.id, GroupID: c.communityID, Relay: relay, FromPubKey: rumor.PubKey, Text: rumor.Content, EventID: rumor.ID, CreatedAt: rumor.CreatedAt, Meta: meta}
	msg.Reply = func(ctx context.Context, text string) error { return c.send(ctx, text, rumor.ID) }
	c.onMessage(msg)
	return true
}

func (c *ConcordChannel) handleDirectInvite(re nostr.RelayEvent) bool {
	event := re.Event
	if event.Kind != ConcordWrapKind || !event.VerifySignature() {
		return false
	}
	plainSeal, err := c.keyer.Decrypt(c.ctx, event.Content, event.PubKey)
	if err != nil || len(plainSeal) > concordMaxPlaintextBytes {
		return false
	}
	var seal nostr.Event
	if json.Unmarshal([]byte(plainSeal), &seal) != nil || seal.Kind != 13 || !seal.VerifySignature() {
		return false
	}
	plainRumor, err := c.keyer.Decrypt(c.ctx, seal.Content, seal.PubKey)
	if err != nil {
		return false
	}
	rumor, err := parseConcordRumor(plainRumor)
	if err != nil || nostr.Kind(rumor.Kind) != ConcordDirectInviteKind || rumor.PubKey != seal.PubKey.Hex() {
		return false
	}
	recipient, pok := concordSingleTag(rumor.Tags, "p")
	kind, kok := concordSingleTag(rumor.Tags, "k")
	if !pok || !kok || recipient != c.pubkey || kind != strconv.Itoa(int(ConcordDirectInviteKind)) {
		return false
	}
	material, err := ParseConcordJoinMaterial(rumor.Content)
	if err != nil || material.CommunityID != c.communityID || (material.ExpiresAt != 0 && material.ExpiresAt < time.Now().UnixMilli()) {
		return false
	}
	if c.seen.Add(event.ID.Hex()) || c.seen.Add(rumor.ID) {
		return false
	}
	c.stateMu.Lock()
	changed := c.adoptMaterialLocked(material)
	c.stateMu.Unlock()
	if changed {
		c.startPlanes()
		go func() {
			if err := c.publishJoin(c.ctx, material); err != nil {
				c.emitErr(err)
			}
		}()
	}
	return true
}

func (c *ConcordChannel) adoptMaterialLocked(next ConcordJoinMaterial) bool {
	if c.material == nil {
		cp := next
		c.material = &cp
		c.relays = mergeConcordRelays(c.relays, next.Relays)
		return true
	}
	changed := false
	if next.RootEpoch > c.material.RootEpoch {
		c.material.CommunityRoot, c.material.RootEpoch = next.CommunityRoot, next.RootEpoch
		if next.ControlPK != "" {
			c.material.ControlPK = next.ControlPK
		}
		changed = true
	}
	for _, entry := range next.Channels {
		found := false
		for i := range c.material.Channels {
			if c.material.Channels[i].ID == entry.ID {
				found = true
				if entry.Epoch > c.material.Channels[i].Epoch {
					c.material.Channels[i] = entry
					changed = true
				}
				break
			}
		}
		if !found {
			c.material.Channels = append(c.material.Channels, entry)
			changed = true
		}
	}
	return changed
}

func (c *ConcordChannel) publishJoin(ctx context.Context, material ConcordJoinMaterial) error {
	c.stateMu.Lock()
	if c.joinSent {
		c.stateMu.Unlock()
		return nil
	}
	c.joinSent = true
	c.stateMu.Unlock()
	_, _, guestbook, err := c.derivePlanes(&material)
	if err != nil {
		return err
	}
	now := time.Now()
	tags := [][]string{{"ms", strconv.Itoa(now.Nanosecond() / int(time.Millisecond))}}
	if material.CreatorPubKey != "" {
		tags = append(tags, []string{"invite", material.CreatorPubKey, material.Label})
	}
	rumor := concordRumor{PubKey: c.pubkey, CreatedAt: now.Unix(), Kind: int(ConcordGuestbookKind), Tags: tags, Content: "join"}
	wrap, err := wrapConcordRumor(ctx, rumor, ConcordSealEncryptedKind, guestbook, c.keyer)
	if err != nil {
		return err
	}
	if _, err := okpublish.PublishToAny(ctx, c.publisher, c.relays, wrap); err != nil {
		c.stateMu.Lock()
		c.joinSent = false
		c.stateMu.Unlock()
		return fmt.Errorf("publish concord join: %w", err)
	}
	return nil
}

func (c *ConcordChannel) coalesceMembershipLocked(pubkey, status string, atMS int64, rumorID string) {
	current, exists := c.membership[pubkey]
	if exists && (atMS < current.atMS || (atMS == current.atMS && rumorID >= current.rumorID)) {
		return
	}
	c.membership[pubkey] = concordMembership{status: status, atMS: atMS, rumorID: rumorID}
}

func (c *ConcordChannel) emitErr(err error) {
	if err != nil && c.onErr != nil && c.ctx.Err() == nil {
		c.onErr(err)
	}
}

func validateConcordHex(value, field string) error {
	if len(value) != 64 || value != strings.ToLower(value) {
		return fmt.Errorf("%s must be 64-character lowercase hex", field)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("%s must be 64-character lowercase hex", field)
	}
	return nil
}

func normalizeConcordRelays(relays []string) ([]string, error) {
	out, seen := make([]string, 0, len(relays)), map[string]struct{}{}
	for _, raw := range relays {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
			return nil, fmt.Errorf("invalid concord relay URL %q", raw)
		}
		if _, ok := seen[raw]; ok {
			continue
		}
		seen[raw] = struct{}{}
		out = append(out, raw)
	}
	return out, nil
}

func mergeConcordRelays(a, b []string) []string {
	out, _ := normalizeConcordRelays(append(append([]string(nil), a...), b...))
	return out
}

func cloneConcordMaterial(in *ConcordJoinMaterial) *ConcordJoinMaterial {
	if in == nil {
		return nil
	}
	cp := *in
	cp.Channels = append([]ConcordChannelKeyEntry(nil), in.Channels...)
	cp.Relays = append([]string(nil), in.Relays...)
	return &cp
}

func cloneConcordDefinitions(in map[string]concordChannelDefinition) map[string]concordChannelDefinition {
	out := make(map[string]concordChannelDefinition, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func concordSingleTag(tags [][]string, name string) (string, bool) {
	value, found := "", false
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == name {
			if found {
				return "", false
			}
			value, found = tag[1], true
		}
	}
	return value, found
}

func concordRumorInstant(r concordRumor) (int64, bool) {
	ms := int64(0)
	if raw, ok := concordSingleTag(r.Tags, "ms"); ok {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 || parsed > 999 {
			return 0, false
		}
		ms = parsed
	}
	at := r.CreatedAt*1000 + ms
	return at, at <= time.Now().Add(concordFutureSkew).UnixMilli()
}

func parseConcordPermissions(raw json.RawMessage) (uint64, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		value, err := strconv.ParseUint(text, 10, 64)
		return value, err == nil
	}
	var number uint64
	if json.Unmarshal(raw, &number) == nil {
		return number, true
	}
	return 0, false
}
