package nostrmeta

import (
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	MaxScannedTags     = 512
	MaxParticipants    = 8
	MaxAttachments     = 4
	MaxHashtags        = 8
	MaxLabels          = 8
	MaxUnknownTagNames = 12
	StringFieldChars   = 256
	ShortFieldChars    = 64
	SecretFieldChars   = 128
	RelayURLChars      = 128
	URLChars           = 256
	PayloadChars       = 1200
	HashtagChars       = 64
)

var (
	hex64Re          = regexp.MustCompile(`^[0-9a-f]{64}$`)
	relayHintRe      = regexp.MustCompile(`^wss?://`)
	httpURLRe        = regexp.MustCompile(`^https?://`)
	unknownTagNameRe = regexp.MustCompile(`^[a-z0-9_\-.:]{1,32}$`)
	mimeTypeRe       = regexp.MustCompile(`^[a-z]+\/[a-z0-9.+_-]+$`)
	dimsRe           = regexp.MustCompile(`^\d+x\d+$`)
	blurhashRe       = regexp.MustCompile(`^[A-Za-z0-9$_+-]+={0,2}$`)
	algorithmNameRe  = regexp.MustCompile(`^[a-z0-9_-]+$`)
	opaqueSecretRe   = regexp.MustCompile(`^[A-Za-z0-9+/=_:-]+$`)
	controlRe        = regexp.MustCompile(`[\x00-\x08\x0B\x0C\x0E-\x1F]`)
)

func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
}

var modelledTagNames = map[string]struct{}{
	"e": {}, "E": {}, "k": {}, "K": {}, "p": {}, "P": {}, "q": {},
	"subject": {}, "expiration": {}, "content-warning": {}, "-": {},
	"alt": {}, "client": {}, "t": {}, "l": {}, "L": {},
	"imeta": {}, "file-type": {}, "m": {}, "size": {}, "dim": {},
	"x": {}, "ox": {}, "blurhash": {}, "thumb": {}, "fallback": {},
	"encryption-algorithm": {}, "decryption-key": {}, "decryption-nonce": {},
}

var ignoredTagNames = map[string]struct{}{
	"h": {}, "d": {}, "a": {}, "A": {}, "g": {}, "i": {}, "r": {},
	"relays": {}, "previous": {}, "ms": {}, "nonce": {}, "plane": {},
	"emoji": {}, "proxy": {},
	"channel": {}, "epoch": {},
}

var allowedPayloadKeys = map[string]struct{}{
	"protocol": {}, "subject": {}, "thread": {}, "scheme": {},
	"root": {}, "parent": {}, "quote": {}, "id": {}, "author": {},
	"relay": {}, "kind": {}, "participants": {}, "direct_recipients": {},
	"multi_party": {}, "omitted": {}, "reply_scope": {}, "community": {},
	"community_id": {}, "channel_id": {}, "channel": {}, "epoch": {},
	"section_kind": {}, "sender_roles": {}, "attachments": {}, "url": {},
	"mime_type": {}, "size_bytes": {}, "dim": {}, "sha256_ciphertext": {},
	"sha256_plaintext": {}, "blurhash": {}, "alt": {}, "encryption": {},
	"algorithm": {}, "key": {}, "nonce": {}, "source": {}, "handling": {},
	"expires_at": {}, "content_warning": {}, "protected": {}, "client": {},
	"hashtags": {}, "labels": {}, "ns": {}, "value": {}, "unknown_tags": {},
	"truncated": {},
}

func IsAllowedPayloadKey(key string) bool {
	_, ok := allowedPayloadKeys[key]
	return ok
}

type Protocol string

const (
	ProtocolNIP04      Protocol = "nip04"
	ProtocolNIP17      Protocol = "nip17"
	ProtocolNIP29      Protocol = "nip29"
	ProtocolNIP28      Protocol = "nip28"
	ProtocolCommunikey Protocol = "communikey"
	ProtocolConcord    Protocol = "concord"
)

type EventRef struct {
	ID     string `json:"id"`
	Author string `json:"author,omitempty"`
	Relay  string `json:"relay,omitempty"`
	Kind   int    `json:"kind,omitempty"`
}

type ThreadRefs struct {
	Root   *EventRef `json:"root,omitempty"`
	Parent *EventRef `json:"parent,omitempty"`
	Quote  *EventRef `json:"quote,omitempty"`
	Scheme string    `json:"scheme,omitempty"`
}

type Participants struct {
	Recipients   []string `json:"direct_recipients,omitempty"`
	IsMultiParty bool     `json:"multi_party,omitempty"`
	Omitted      int      `json:"omitted,omitempty"`
}

func (p Participants) MarshalJSON() ([]byte, error) {
	m := make(map[string]any, 3)
	if len(p.Recipients) > 0 {
		m["direct_recipients"] = p.Recipients
	}
	if p.IsMultiParty {
		m["multi_party"] = true
	}
	if p.Omitted > 0 {
		m["omitted"] = p.Omitted
	}
	if len(m) == 0 {
		return nil, nil
	}
	type alias Participants
	return json.Marshal(alias(p))
}

type Encryption struct {
	Algorithm string `json:"algorithm,omitempty"`
	Key       string `json:"key,omitempty"`
	Nonce     string `json:"nonce,omitempty"`
}

type Attachment struct {
	URL              string      `json:"url,omitempty"`
	MimeType         string      `json:"mime_type,omitempty"`
	SizeBytes        int64       `json:"size_bytes,omitempty"`
	Dim              string      `json:"dim,omitempty"`
	SHA256Ciphertext string      `json:"sha256_ciphertext,omitempty"`
	SHA256Plaintext  string      `json:"sha256_plaintext,omitempty"`
	Blurhash         string      `json:"blurhash,omitempty"`
	Alt              string      `json:"alt,omitempty"`
	Encryption       *Encryption `json:"encryption,omitempty"`
	Source           string      `json:"source"`
}

type Label struct {
	NS    string `json:"ns,omitempty"`
	Value string `json:"value"`
}

type Handling struct {
	ExpiresAt      int64   `json:"expires_at,omitempty"`
	ContentWarning string  `json:"content_warning,omitempty"`
	Protected      bool    `json:"protected,omitempty"`
	AltText        string  `json:"alt,omitempty"`
	Client         string  `json:"client,omitempty"`
	Hashtags       []string `json:"hashtags,omitempty"`
	Labels         []Label `json:"labels,omitempty"`
}

type CommunityFacts struct {
	CommunityID      string   `json:"community_id,omitempty"`
	CommunityAddress string   `json:"community_address,omitempty"`
	OwnerPubkey      string   `json:"owner_pubkey,omitempty"`
	ChannelID        string   `json:"channel_id,omitempty"`
	ChannelName      string   `json:"channel,omitempty"`
	Epoch            int64    `json:"epoch,omitempty"`
	SectionKind      int      `json:"section_kind,omitempty"`
	SenderRoles      []string `json:"sender_roles,omitempty"`
}

func (c CommunityFacts) MarshalJSON() ([]byte, error) {
	m := make(map[string]any)
	if c.CommunityID != "" {
		m["community_id"] = c.CommunityID
	}
	if c.CommunityAddress != "" {
		m["community_address"] = c.CommunityAddress
	}
	if c.OwnerPubkey != "" {
		m["owner_pubkey"] = c.OwnerPubkey
	}
	if c.ChannelID != "" {
		m["channel_id"] = c.ChannelID
	}
	if c.ChannelName != "" {
		m["channel"] = c.ChannelName
	}
	if c.Epoch != 0 {
		m["epoch"] = c.Epoch
	}
	if c.SectionKind != 0 {
		m["section_kind"] = c.SectionKind
	}
	if len(c.SenderRoles) > 0 {
		m["sender_roles"] = c.SenderRoles
	}
	if len(m) == 0 {
		return nil, nil
	}
	return json.Marshal(m)
}

type Metadata struct {
	Protocol     Protocol        `json:"protocol"`
	Subject      string          `json:"subject,omitempty"`
	Thread       *ThreadRefs     `json:"thread,omitempty"`
	Participants *Participants   `json:"participants,omitempty"`
	ReplyScope   string          `json:"reply_scope,omitempty"`
	Community    json.RawMessage `json:"community,omitempty"`
	Attachments  []Attachment    `json:"attachments,omitempty"`
	Handling     *Handling       `json:"handling,omitempty"`
	UnknownTags  []string        `json:"unknown_tags,omitempty"`
	Truncated    bool            `json:"truncated,omitempty"`
}

type Input struct {
	Protocol     Protocol
	Community    *CommunityFacts
	Tags         [][]string
	Content      string
	SelfPubkey   string
	SenderPubkey string
	ThreadScheme string
	Subject      string
}

func NormalizeHex64(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) != 64 {
		return "", false
	}
	for _, r := range s {
		if !isHexDigit(r) {
			return "", false
		}
	}
	return s, true
}

func NormalizeEventKind(v any) (int, bool) {
	var n int
	switch val := v.(type) {
	case int:
		n = val
	case int64:
		n = int(val)
	case float64:
		if val != math.Trunc(val) {
			return 0, false
		}
		n = int(val)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil {
			return 0, false
		}
		n = parsed
	default:
		return 0, false
	}
	if n < 0 || n > 65535 {
		return 0, false
	}
	return n, true
}

func NormalizeUnixSeconds(v any) (int64, bool) {
	var n int64
	switch val := v.(type) {
	case int:
		n = int64(val)
	case int64:
		n = val
	case float64:
		if val != math.Trunc(val) || val < 1 || val >= 4102444800 {
			return 0, false
		}
		n = int64(val)
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
		if err != nil {
			return 0, false
		}
		n = parsed
	default:
		return 0, false
	}
	if n < 1 || n >= 4102444800 {
		return 0, false
	}
	return n, true
}

func NormalizeByteSize(v any) (int64, bool) {
	var n int64
	switch val := v.(type) {
	case int:
		n = int64(val)
	case int64:
		n = val
	case float64:
		if val != math.Trunc(val) || val < 0 {
			return 0, false
		}
		n = int64(val)
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
		if err != nil {
			return 0, false
		}
		n = parsed
	default:
		return 0, false
	}
	if n < 0 {
		return 0, false
	}
	return n, true
}

func NormalizeRelayHint(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	if !relayHintRe.MatchString(s) {
		return "", false
	}
	scheme := "wss://"
	if strings.HasPrefix(s, "ws://") {
		scheme = "ws://"
	}
	rest := strings.TrimPrefix(s, scheme)
	if atIdx := strings.IndexByte(rest, '@'); atIdx >= 0 {
		rest = rest[atIdx+1:]
	}
	if qIdx := strings.IndexByte(rest, '?'); qIdx >= 0 {
		rest = rest[:qIdx]
	}
	if hIdx := strings.IndexByte(rest, '#'); hIdx >= 0 {
		rest = rest[:hIdx]
	}
	full := scheme + rest
	if len(full) > RelayURLChars {
		return "", false
	}
	for _, r := range full {
		if r <= 0x20 || r >= 0x7F {
			return "", false
		}
	}
	return full, true
}

func NormalizeHTTPURL(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	if !httpURLRe.MatchString(s) {
		return "", false
	}
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = "https://" + s
	if qIdx := strings.IndexByte(s, '?'); qIdx >= 0 {
		s = s[:qIdx]
	}
	if hIdx := strings.IndexByte(s, '#'); hIdx >= 0 {
		s = s[:hIdx]
	}
	if len(s) > URLChars {
		return "", false
	}
	for _, r := range s {
		if r <= 0x20 || r >= 0x7F {
			return "", false
		}
	}
	return s, true
}

func NormalizeText(s string, maxChars ...int) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	s = controlRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\x00", "")
	if s == "" {
		return "", false
	}
	max := StringFieldChars
	if len(maxChars) > 0 && maxChars[0] > 0 {
		max = maxChars[0]
	}
	runes := []rune(s)
	if len(runes) > max {
		runes = runes[:max]
	}
	s = string(runes)
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	s = NeutralizeFences(s)
	return s, true
}

func StripUnsafeChars(s string) string {
	return controlRe.ReplaceAllString(s, "")
}

func NeutralizeFences(s string) string {
	s = strings.ReplaceAll(s, "```", "\uFFFD")
	s = strings.ReplaceAll(s, "`", "\uFFFD")
	return s
}

func cappedTags(tags [][]string) [][]string {
	if len(tags) > MaxScannedTags {
		return tags[:MaxScannedTags]
	}
	return tags
}

func tagsNamed(tags [][]string, name string) [][]string {
	var result [][]string
	for _, tag := range cappedTags(tags) {
		if len(tag) > 0 && tag[0] == name {
			result = append(result, tag)
		}
	}
	return result
}

func firstTagNamed(tags [][]string, name string) []string {
	for _, tag := range cappedTags(tags) {
		if len(tag) > 0 && tag[0] == name {
			return tag
		}
	}
	return nil
}

func isModelled(name string) bool {
	_, ok := modelledTagNames[name]
	return ok
}

func isIgnored(name string) bool {
	_, ok := ignoredTagNames[name]
	return ok
}

func isValidUnknownTagName(s string) bool {
	return unknownTagNameRe.MatchString(s)
}

func CollectUnknownTagNames(tags [][]string) []string {
	seen := make(map[string]struct{})
	var names []string
	for _, tag := range cappedTags(tags) {
		if len(tag) == 0 {
			continue
		}
		name := tag[0]
		if isModelled(name) || isIgnored(name) {
			continue
		}
		if !isValidUnknownTagName(name) {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > MaxUnknownTagNames {
		names = names[:MaxUnknownTagNames]
	}
	return names
}

func refFromPositional(tag []string, kind ...int) *EventRef {
	if len(tag) < 2 {
		return nil
	}
	id, ok := NormalizeHex64(tag[1])
	if !ok {
		return nil
	}
	ref := &EventRef{ID: id}
	if len(tag) >= 3 {
		if relay, ok := NormalizeRelayHint(tag[2]); ok {
			ref.Relay = relay
		}
	}
	if len(tag) >= 4 {
		if author, ok := NormalizeHex64(tag[3]); ok {
			ref.Author = author
		}
	}
	if len(kind) > 0 {
		ref.Kind = kind[0]
	}
	return ref
}

func refFromMarked(tag []string) *EventRef {
	if tag == nil {
		return nil
	}
	ref := refFromPositional(tag)
	if ref == nil || len(tag) < 5 {
		return ref
	}
	if author, ok := NormalizeHex64(tag[4]); ok {
		ref.Author = author
	}
	return ref
}

func parseNip22ThreadRefs(tags [][]string) *ThreadRefs {
	var rootKind int
	if tag := firstTagNamed(tags, "K"); tag != nil && len(tag) > 1 {
		rootKind, _ = NormalizeEventKind(tag[1])
	}
	var parentKind int
	if tag := firstTagNamed(tags, "k"); tag != nil && len(tag) > 1 {
		parentKind, _ = NormalizeEventKind(tag[1])
	}
	root := refFromPositional(firstTagNamed(tags, "E"), rootKind)
	parent := refFromPositional(firstTagNamed(tags, "e"), parentKind)
	var rootAuthor string
	if root != nil && root.Author != "" {
		rootAuthor = root.Author
	} else if tag := firstTagNamed(tags, "P"); tag != nil {
		rootAuthor, _ = NormalizeHex64(tag[1])
	}
	t := &ThreadRefs{}
	if root != nil {
		if rootAuthor != "" {
			root.Author = rootAuthor
		}
		t.Root = root
	}
	if parent != nil {
		t.Parent = parent
	}
	if t.Root == nil && t.Parent == nil {
		return nil
	}
	return t
}

func parseNip10ThreadRefs(tags [][]string) *ThreadRefs {
	eTags := tagsNamed(tags, "e")
	var rootTag, replyTag []string
	for _, tag := range eTags {
		if len(tag) < 4 {
			continue
		}
		if _, ok := NormalizeHex64(tag[1]); !ok {
			continue
		}
		switch tag[3] {
		case "root":
			rootTag = tag
		case "reply":
			if replyTag == nil {
				replyTag = tag
			}
		}
	}
	if rootTag != nil || replyTag != nil {
		t := &ThreadRefs{}
		if rootTag != nil {
			t.Root = refFromMarked(rootTag)
		}
		if replyTag != nil {
			t.Parent = refFromMarked(replyTag)
		}
		if t.Root == nil && t.Parent == nil {
			return nil
		}
		return t
	}
	var valid [][]string
	for _, tag := range eTags {
		if _, ok := NormalizeHex64(tag[1]); ok {
			valid = append(valid, tag)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	if len(valid) == 1 {
		return &ThreadRefs{Parent: refFromPositional(valid[0])}
	}
	return &ThreadRefs{
		Root:   refFromPositional(valid[0]),
		Parent: refFromPositional(valid[len(valid)-1]),
	}
}

func ParseThreadRefs(tags [][]string, scheme ...string) *ThreadRefs {
	s := "nip10"
	if len(scheme) > 0 && scheme[0] == "nip22" {
		s = "nip22"
	}
	var t *ThreadRefs
	if s == "nip22" {
		t = parseNip22ThreadRefs(tags)
	} else {
		t = parseNip10ThreadRefs(tags)
	}
	if t == nil {
		t = &ThreadRefs{}
	}
	quote := refFromPositional(firstTagNamed(tags, "q"))
	if quote != nil {
		t.Quote = quote
	}
	if t.Root != nil || t.Parent != nil {
		t.Scheme = s
	}
	if t.Root == nil && t.Parent == nil && t.Quote == nil {
		return nil
	}
	return t
}

func ParseParticipants(tags [][]string, selfPubkey, senderPubkey string) *Participants {
	self, _ := NormalizeHex64(selfPubkey)
	sender, _ := NormalizeHex64(senderPubkey)
	var recipients []string
	seen := make(map[string]struct{})
	for _, tag := range cappedTags(tags) {
		if len(tag) == 0 || tag[0] != "p" {
			continue
		}
		pubkey, ok := NormalizeHex64(tag[1])
		if !ok {
			continue
		}
		if pubkey == self || pubkey == sender {
			continue
		}
		if _, ok := seen[pubkey]; ok {
			continue
		}
		seen[pubkey] = struct{}{}
		recipients = append(recipients, pubkey)
	}
	if len(recipients) == 0 {
		return nil
	}
	capped := recipients
	omitted := 0
	if len(capped) > MaxParticipants {
		omitted = len(capped) - MaxParticipants
		capped = capped[:MaxParticipants]
	}
	return &Participants{
		Recipients:   capped,
		IsMultiParty: true,
		Omitted:      omitted,
	}
}

func ParseImetaTag(tag []string) map[string]string {
	fields := make(map[string]string)
	for _, entry := range tag[1:] {
		split := strings.IndexByte(entry, ' ')
		if split <= 0 {
			continue
		}
		key := entry[:split]
		if _, ok := fields[key]; ok {
			continue
		}
		fields[key] = entry[split+1:]
	}
	return fields
}

func parseImetaFields(tag []string) map[string]string {
	return ParseImetaTag(tag)
}

var flatFileTagNames = []string{
	"file-type", "m", "size", "dim", "x", "ox",
	"blurhash", "encryption-algorithm", "decryption-key", "decryption-nonce",
}

func buildAttachment(fields map[string]string, source string) *Attachment {
	a := &Attachment{Source: source}
	if url, ok := fields["url"]; ok {
		if u, valid := NormalizeHTTPURL(url); valid {
			a.URL = u
		}
	}
	if mime, ok := fields["m"]; ok {
		mimeTypeRe.MatchString(mime)
		if mimeTypeRe.MatchString(mime) && len(mime) <= ShortFieldChars {
			a.MimeType = strings.ToLower(mime)
		}
	}
	if sizeStr, ok := fields["size"]; ok {
		if size, valid := NormalizeByteSize(sizeStr); valid {
			a.SizeBytes = size
		}
	}
	if dim, ok := fields["dim"]; ok {
		if dimsRe.MatchString(dim) && len(dim) <= 16 {
			a.Dim = dim
		}
	}
	if x, ok := fields["x"]; ok {
		if h, valid := NormalizeHex64(x); valid {
			a.SHA256Ciphertext = h
		}
	}
	if ox, ok := fields["ox"]; ok {
		if h, valid := NormalizeHex64(ox); valid {
			a.SHA256Plaintext = h
		}
	}
	if bh, ok := fields["blurhash"]; ok {
		if blurhashRe.MatchString(bh) && len(bh) <= 128 {
			a.Blurhash = bh
		}
	}
	if alt, ok := fields["alt"]; ok {
		if t, valid := NormalizeText(alt, 128); valid {
			a.Alt = t
		}
	}
	enc := &Encryption{}
	hasEnc := false
	if alg, ok := fields["encryption-algorithm"]; ok {
		if algorithmNameRe.MatchString(alg) && len(alg) <= ShortFieldChars {
			enc.Algorithm = alg
			hasEnc = true
		}
	}
	if keyVal, ok := fields["decryption-key"]; ok {
		if opaqueSecretRe.MatchString(keyVal) && len(keyVal) <= SecretFieldChars {
			enc.Key = keyVal
			hasEnc = true
		}
	}
	if nonceVal, ok := fields["decryption-nonce"]; ok {
		if opaqueSecretRe.MatchString(nonceVal) && len(nonceVal) <= SecretFieldChars {
			enc.Nonce = nonceVal
			hasEnc = true
		}
	}
	if hasEnc {
		a.Encryption = enc
	}
	if a.Source == "" {
		return nil
	}
	hasField := a.URL != "" || a.MimeType != "" || a.SizeBytes != 0 || a.Dim != "" ||
		a.SHA256Ciphertext != "" || a.SHA256Plaintext != "" || a.Blurhash != "" ||
		a.Alt != "" || a.Encryption != nil
	if !hasField {
		return nil
	}
	return a
}

func ParseAttachments(tags [][]string, content ...string) []Attachment {
	var attachments []Attachment
	for _, tag := range tagsNamed(tags, "imeta") {
		if len(attachments) >= MaxAttachments {
			break
		}
		fields := parseImetaFields(tag)
		a := buildAttachment(fields, "imeta")
		if a != nil {
			attachments = append(attachments, *a)
		}
	}
	hasFlatFile := false
	for _, name := range flatFileTagNames {
		if firstTagNamed(tags, name) != nil {
			hasFlatFile = true
			break
		}
	}
	if hasFlatFile && len(attachments) < MaxAttachments {
		flatFields := make(map[string]string)
		if len(content) > 0 {
			flatFields["url"] = content[0]
		}
		if tag := firstTagNamed(tags, "file-type"); tag != nil {
			flatFields["m"] = tag[1]
		} else if tag := firstTagNamed(tags, "m"); tag != nil {
			flatFields["m"] = tag[1]
		}
		if tag := firstTagNamed(tags, "size"); tag != nil {
			flatFields["size"] = tag[1]
		}
		if tag := firstTagNamed(tags, "dim"); tag != nil {
			flatFields["dim"] = tag[1]
		}
		if tag := firstTagNamed(tags, "x"); tag != nil {
			flatFields["x"] = tag[1]
		}
		if tag := firstTagNamed(tags, "ox"); tag != nil {
			flatFields["ox"] = tag[1]
		}
		if tag := firstTagNamed(tags, "blurhash"); tag != nil {
			flatFields["blurhash"] = tag[1]
		}
		if tag := firstTagNamed(tags, "encryption-algorithm"); tag != nil {
			flatFields["encryption-algorithm"] = tag[1]
		}
		if tag := firstTagNamed(tags, "decryption-key"); tag != nil {
			flatFields["decryption-key"] = tag[1]
		}
		if tag := firstTagNamed(tags, "decryption-nonce"); tag != nil {
			flatFields["decryption-nonce"] = tag[1]
		}
		a := buildAttachment(flatFields, "flat")
		if a != nil {
			attachments = append(attachments, *a)
		}
	}
	if len(attachments) == 0 {
		return nil
	}
	return attachments
}

func parseHashtags(tags [][]string) []string {
	var hashtags []string
	seen := make(map[string]struct{})
	for _, tag := range cappedTags(tags) {
		if len(tag) < 2 || tag[0] != "t" {
			continue
		}
		val := strings.TrimLeft(tag[1], "#")
		v, ok := NormalizeText(val, HashtagChars)
		if !ok {
			continue
		}
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		hashtags = append(hashtags, v)
		if len(hashtags) >= MaxHashtags {
			break
		}
	}
	if len(hashtags) == 0 {
		return nil
	}
	return hashtags
}

func parseLabels(tags [][]string) []Label {
	var labels []Label
	for _, tag := range cappedTags(tags) {
		if len(tag) < 2 || tag[0] != "l" {
			continue
		}
		v, ok := NormalizeText(tag[1], ShortFieldChars)
		if !ok {
			continue
		}
		l := Label{Value: v}
		if len(tag) >= 3 {
			if ns, valid := NormalizeText(tag[2], ShortFieldChars); valid && unknownTagNameRe.MatchString(ns) {
				l.NS = ns
			}
		}
		labels = append(labels, l)
		if len(labels) >= MaxLabels {
			break
		}
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
}

func ParseHandling(tags [][]string) *Handling {
	h := &Handling{}
	hasAny := false
	if tag := firstTagNamed(tags, "expiration"); tag != nil {
		if exp, ok := NormalizeUnixSeconds(tag[1]); ok {
			h.ExpiresAt = exp
			hasAny = true
		}
	}
	if tag := firstTagNamed(tags, "content-warning"); tag != nil {
		txt, ok := NormalizeText(tag[1])
		if ok {
			h.ContentWarning = txt
		} else {
			h.ContentWarning = ""
		}
		hasAny = true
	}
	if firstTagNamed(tags, "-") != nil {
		h.Protected = true
		hasAny = true
	}
	if tag := firstTagNamed(tags, "alt"); tag != nil {
		if t, ok := NormalizeText(tag[1]); ok {
			h.AltText = t
			hasAny = true
		}
	}
	if tag := firstTagNamed(tags, "client"); tag != nil {
		if t, ok := NormalizeText(tag[1], ShortFieldChars); ok {
			h.Client = t
			hasAny = true
		}
	}
	if ht := parseHashtags(tags); ht != nil {
		h.Hashtags = ht
		hasAny = true
	}
	if lb := parseLabels(tags); lb != nil {
		h.Labels = lb
		hasAny = true
	}
	if !hasAny {
		return nil
	}
	return h
}

func Build(in Input) Metadata {
	m := Metadata{Protocol: in.Protocol}
	tags := in.Tags
	if tags == nil {
		tags = [][]string{}
	}
	if s, ok := NormalizeText(in.Subject); ok {
		m.Subject = s
	} else if tag := firstTagNamed(tags, "subject"); tag != nil {
		if s, ok := NormalizeText(tag[1]); ok {
			m.Subject = s
		}
	}
	scheme := in.ThreadScheme
	if scheme == "" {
		scheme = "nip10"
	}
	if t := ParseThreadRefs(tags, scheme); t != nil {
		m.Thread = t
	}
	if p := ParseParticipants(tags, in.SelfPubkey, in.SenderPubkey); p != nil {
		m.Participants = p
	}
	if a := ParseAttachments(tags, in.Content); a != nil {
		m.Attachments = a
	}
	if h := ParseHandling(tags); h != nil {
		m.Handling = h
	}
	if ut := CollectUnknownTagNames(tags); len(ut) > 0 {
		m.UnknownTags = ut
	}
	if in.Community != nil {
		data, err := json.Marshal(in.Community)
		if err == nil {
			m.Community = data
		}
	}
	return m
}