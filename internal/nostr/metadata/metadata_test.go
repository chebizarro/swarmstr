package nostrmeta

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeHex64(t *testing.T) {
	cases := []struct {
		input string
		want  string
		ok    bool
	}{
		{"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", true},
		{"ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef0123456789", "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", true},
		{"", "", false},
		{"abc", "", false},
		{"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeHex64(c.input)
		if ok != c.ok || got != c.want {
			t.Errorf("NormalizeHex64(%q) = (%q, %v), want (%q, %v)", c.input, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeEventKind(t *testing.T) {
	cases := []struct {
		input any
		want  int
		ok    bool
	}{
		{int(14), 14, true},
		{int64(1111), 1111, true},
		{"14", 14, true},
		{float64(7), 7, true},
		{"65535", 65535, true},
		{"65536", 0, false},
		{"-1", 0, false},
		{"abc", 0, false},
		{nil, 0, false},
	}
	for _, c := range cases {
		got, ok := NormalizeEventKind(c.input)
		if ok != c.ok || got != c.want {
			t.Errorf("NormalizeEventKind(%v) = (%d, %v), want (%d, %v)", c.input, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeUnixSeconds(t *testing.T) {
	cases := []struct {
		input any
		want  int64
		ok    bool
	}{
		{int64(1700000000), int64(1700000000), true},
		{int(1700000000), int64(1700000000), true},
		{"1700000000", int64(1700000000), true},
		{float64(1700000000), int64(1700000000), true},
		{"0", 0, false},
		{"-1", 0, false},
		{"4102444800", 0, false},
		{"abc", 0, false},
	}
	for _, c := range cases {
		got, ok := NormalizeUnixSeconds(c.input)
		if ok != c.ok || got != c.want {
			t.Errorf("NormalizeUnixSeconds(%v) = (%d, %v), want (%d, %v)", c.input, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeByteSize(t *testing.T) {
	cases := []struct {
		input any
		want  int64
		ok    bool
	}{
		{int(1024), int64(1024), true},
		{int64(0), int64(0), true},
		{"2048", int64(2048), true},
		{float64(512), int64(512), true},
		{"-1", 0, false},
		{"abc", 0, false},
	}
	for _, c := range cases {
		got, ok := NormalizeByteSize(c.input)
		if ok != c.ok || got != c.want {
			t.Errorf("NormalizeByteSize(%v) = (%d, %v), want (%d, %v)", c.input, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeRelayHint(t *testing.T) {
	cases := []struct {
		input string
		want  string
		ok    bool
	}{
		{"wss://relay.example.com", "wss://relay.example.com", true},
		{"ws://localhost:8080", "ws://localhost:8080", true},
		{"wss://user:pass@relay.example.com?auth=1#frag", "wss://relay.example.com", true},
		{"https://relay.example.com", "", false},
		{"", "", false},
		{"relay.example.com", "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeRelayHint(c.input)
		if ok != c.ok || got != c.want {
			t.Errorf("NormalizeRelayHint(%q) = (%q, %v), want (%q, %v)", c.input, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeHTTPURL(t *testing.T) {
	cases := []struct {
		input string
		want  string
		ok    bool
	}{
		{"https://example.com/image.jpg", "https://example.com/image.jpg", true},
		{"http://example.com/img.png", "https://example.com/img.png", true},
		{"https://example.com/pic.jpg?q=1#frag", "https://example.com/pic.jpg", true},
		{"", "", false},
		{"ftp://bad.com", "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeHTTPURL(c.input)
		if ok != c.ok || got != c.want {
			t.Errorf("NormalizeHTTPURL(%q) = (%q, %v), want (%q, %v)", c.input, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeText(t *testing.T) {
	got, ok := NormalizeText("hello world")
	if !ok || got != "hello world" {
		t.Errorf(`NormalizeText("hello world") = (%q, %v), want ("hello world", true)`, got, ok)
	}
	got, ok = NormalizeText("")
	if ok {
		t.Error("expected empty string to fail")
	}
	got, ok = NormalizeText("   ")
	if ok {
		t.Error("expected whitespace-only to fail")
	}
	got, ok = NormalizeText("\x00\x01\x02test")
	if !ok || got != "test" {
		t.Errorf("expected control-char stripping: got %q", got)
	}
	got, ok = NormalizeText("abc\x00def")
	if !ok || got != "abcdef" {
		t.Errorf("expected embedded NUL stripped: got %q", got)
	}
	got, ok = NormalizeText("hello`world")
	if !ok || got != "hello\uFFFDworld" {
		t.Errorf("expected backtick neutralized: got %q", got)
	}
	got, ok = NormalizeText("hello```world")
	if !ok || got != "hello\uFFFDworld" {
		t.Errorf("expected triple backtick neutralized: got %q", got)
	}
	got, ok = NormalizeText("abcdefghij", 5)
	if !ok || got != "abcde" {
		t.Errorf("expected truncation: got %q", got)
	}
}

func TestStripUnsafeChars(t *testing.T) {
	input := "a\x00b\x01c\td"
	got := StripUnsafeChars(input)
	want := "abc\td"
	if got != want {
		t.Errorf("StripUnsafeChars(%q) = %q, want %q", input, got, want)
	}
}

func TestNeutralizeFences(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"hello ` world", "hello \uFFFD world"},
		{"a```b", "a\uFFFDb"},
		{"no fences", "no fences"},
		{"a`b`c", "a\uFFFDb\uFFFDc"},
	}
	for _, c := range cases {
		got := NeutralizeFences(c.input)
		if got != c.want {
			t.Errorf("NeutralizeFences(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestCollectUnknownTagNames(t *testing.T) {
	tags := [][]string{
		{"e", "id1"},
		{"p", "pk1"},
		{"foo", "bar"},
		{"bar", "baz"},
		{"h", "groupid"},
		{"hax0r", "val"},
		{"UPPERCASE", "val"},
		{"foo", "duplicate"},
	}
	names := CollectUnknownTagNames(tags)
	if len(names) != 3 {
		t.Errorf("expected 4 unknown tags, got %d: %v", len(names), names)
	}
	for _, n := range names {
		if n == "e" || n == "p" || n == "h" {
			t.Errorf("modelled/ignored tag %q appeared in unknown_tags", n)
		}
	}
	prev := ""
	for _, n := range names {
		if n < prev {
			t.Errorf("unknown_tags not sorted: %s before %s", prev, n)
		}
		prev = n
	}
	oversized := [][]string{}
	for i := 0; i < 20; i++ {
		oversized = append(oversized, []string{"tag" + strings.Repeat("x", 40), "v"})
	}
	oversized = append(oversized, []string{"validtag", "v"})
	names = CollectUnknownTagNames(oversized)
	if len(names) > MaxUnknownTagNames {
		t.Errorf("unknown_tags exceeded cap: %d > %d", len(names), MaxUnknownTagNames)
	}
}

func TestParseNip10ThreadRefs(t *testing.T) {
	a64 := strings.Repeat("a", 64)
	b64 := strings.Repeat("b", 64)
	markedRootReply := [][]string{
		{"e", "1111111111111111111111111111111111111111111111111111111111111111", "", "root", a64},
		{"e", "2222222222222222222222222222222222222222222222222222222222222222", "wss://relay.com", "reply", b64},
	}
	t1 := ParseThreadRefs(markedRootReply, "nip10")
	if t1 == nil || t1.Root == nil || t1.Parent == nil {
		t.Fatalf("expected root and parent, got nil")
	}
	if t1.Root.Author != a64 {
		t.Errorf("root author wrong: %q", t1.Root.Author)
	}
	if t1.Parent.Relay != "wss://relay.com" {
		t.Errorf("parent relay wrong: %q", t1.Parent.Relay)
	}
	if t1.Scheme != "nip10" {
		t.Errorf("expected scheme nip10, got %q", t1.Scheme)
	}
	positional := [][]string{
		{"e", "3333333333333333333333333333333333333333333333333333333333333333"},
		{"e", "4444444444444444444444444444444444444444444444444444444444444444", "wss://other.com"},
	}
	t2 := ParseThreadRefs(positional, "nip10")
	if t2 == nil || t2.Root == nil || t2.Parent == nil {
		t.Fatalf("expected positional root+parent, got nil")
	}
}

func TestParseNip22ThreadRefs(t *testing.T) {
	a64 := strings.Repeat("a", 64)
	b64 := strings.Repeat("b", 64)
	tags := [][]string{
		{"E", a64, "", a64},
		{"e", b64},
		{"K", "1111"},
		{"k", "42"},
	}
	t1 := ParseThreadRefs(tags, "nip22")
	if t1 == nil || t1.Root == nil || t1.Parent == nil {
		t.Fatalf("expected root and parent, got nil")
	}
	if t1.Root.Kind != 1111 || t1.Parent.Kind != 42 {
		t.Errorf("nip22 kind propagation failed: root.Kind=%d parent.Kind=%d", t1.Root.Kind, t1.Parent.Kind)
	}
}

func TestParseThreadRefsQuote(t *testing.T) {
	tags := [][]string{
		{"q", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "wss://quote.relay"},
	}
	t1 := ParseThreadRefs(tags, "nip10")
	if t1 == nil || t1.Quote == nil {
		t.Fatalf("expected quote, got nil")
	}
	if t1.Quote.Relay != "wss://quote.relay" {
		t.Errorf("quote relay wrong: %q", t1.Quote.Relay)
	}
}

func TestParseParticipants(t *testing.T) {
	self := strings.Repeat("a", 64)
	sender := strings.Repeat("b", 64)
	other1 := strings.Repeat("c", 64)
	other2 := strings.Repeat("d", 64)
	tags := [][]string{
		{"p", other1},
		{"p", other2},
		{"p", sender},
	}
	p := ParseParticipants(tags, self, sender)
	if p == nil {
		t.Fatal("expected participants, got nil")
	}
	if !p.IsMultiParty {
		t.Error("expected multi_party")
	}
	if len(p.Recipients) != 2 {
		t.Errorf("expected 2 recipients, got %d", len(p.Recipients))
	}
	solo := [][]string{
		{"p", sender},
	}
	p2 := ParseParticipants(solo, self, sender)
	if p2 != nil {
		t.Error("expected nil for self-only participants")
	}
}

func TestParseImetaTag(t *testing.T) {
	tag := []string{"imeta", "url https://example.com/img.jpg", "m image/jpeg", "x abc123", "url https://override.com"}
	fields := ParseImetaTag(tag)
	if fields["url"] != "https://example.com/img.jpg" {
		t.Errorf("expected first url, got %q", fields["url"])
	}
	if fields["m"] != "image/jpeg" {
		t.Errorf("expected mime, got %q", fields["m"])
	}
	if len(fields) != 3 {
		t.Errorf("expected 3 fields, got %d", len(fields))
	}
}

func TestParseAttachments(t *testing.T) {
	imetaTags := [][]string{
		{"imeta", "url https://example.com/a.jpg", "m image/jpeg", "x aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	}
	atts := ParseAttachments(imetaTags)
	if atts == nil || len(atts) != 1 {
		t.Fatalf("expected 1 imeta attachment, got %d", len(atts))
	}
	if atts[0].Source != "imeta" {
		t.Errorf("expected imeta source")
	}
	flatTags := [][]string{
		{"file-type", "image/png"},
		{"size", "2048"},
		{"x", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
	}
	atts2 := ParseAttachments(flatTags, "https://example.com/b.png")
	if atts2 == nil || len(atts2) != 1 {
		t.Fatalf("expected 1 flat attachment, got %d", len(atts2))
	}
	if atts2[0].Source != "flat" {
		t.Errorf("expected flat source")
	}
}

func TestParseHandling(t *testing.T) {
	tags := [][]string{
		{"expiration", "1700000000"},
		{"content-warning", "spoiler"},
		{"-", ""},
		{"alt", "an image"},
		{"client", "myapp"},
		{"t", "nostr"},
		{"t", "##bitcoin"},
		{"l", "label1", "ns1"},
		{"l", "label2"},
	}
	h := ParseHandling(tags)
	if h == nil {
		t.Fatal("expected handling, got nil")
	}
	if h.ExpiresAt != 1700000000 {
		t.Errorf("expires_at wrong: %d", h.ExpiresAt)
	}
	if h.ContentWarning != "spoiler" {
		t.Errorf("content_warning wrong: %q", h.ContentWarning)
	}
	if !h.Protected {
		t.Error("expected protected")
	}
	if len(h.Hashtags) != 2 {
		t.Errorf("expected 2 hashtags, got %d", len(h.Hashtags))
	}
	if len(h.Labels) != 2 {
		t.Errorf("expected 2 labels, got %d", len(h.Labels))
	}
}

func TestBuildOmitWhenEmpty(t *testing.T) {
	in := Input{Protocol: ProtocolNIP17}
	m := Build(in)
	if m.Protocol != ProtocolNIP17 {
		t.Errorf("wrong protocol: %q", m.Protocol)
	}
	payload, ok := Render(m, RenderOptions{})
	if ok {
		t.Errorf("expected omit-when-empty, got payload: %s", payload)
	}
}

func TestBuildSubject(t *testing.T) {
	in := Input{Protocol: ProtocolNIP17, Subject: "hello world"}
	m := Build(in)
	if m.Subject != "hello world" {
		t.Errorf("subject wrong: %q", m.Subject)
	}
	in2 := Input{Protocol: ProtocolNIP17, Tags: [][]string{{"subject", "from tag"}}}
	m2 := Build(in2)
	if m2.Subject != "from tag" {
		t.Errorf("subject from tag wrong: %q", m2.Subject)
	}
}

func TestBuildParticipantsBothExcluded(t *testing.T) {
	self := strings.Repeat("a", 64)
	sender := strings.Repeat("b", 64)
	tags := [][]string{
		{"p", "aaaaaaa0aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"p", sender},
	}
	in := Input{
		Protocol:     ProtocolNIP17,
		Tags:         tags,
		SelfPubkey:   self,
		SenderPubkey: sender,
	}
	m := Build(in)
	if m.Participants != nil {
		t.Errorf("expected nil participants (only non-hex and sender), got %+v", m.Participants)
	}
}

func TestRenderPayloadKeysAreAllowlisted(t *testing.T) {
	tags := [][]string{
		{"foo", "bar"},
		{"hacker", "value"},
		{"system", "injected"},
		{"policy", "danger"},
	}
	a64 := strings.Repeat("a", 64)
	b64 := strings.Repeat("b", 64)
	in := Input{Protocol: ProtocolNIP17, Tags: tags, SelfPubkey: a64, SenderPubkey: b64}
	m := Build(in)
	payload, ok := Render(m, RenderOptions{})
	if !ok {
		t.Fatal("expected payload, got omit")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	var walkKeys func(m map[string]any)
	walkKeys = func(m map[string]any) {
		for k, v := range m {
			if !IsAllowedPayloadKey(k) {
				t.Errorf("disallowed payload key %q found", k)
			}
			if sub, ok := v.(map[string]any); ok {
				walkKeys(sub)
			}
		}
	}
	walkKeys(parsed)
}

func TestRenderFenceEscape(t *testing.T) {
	tags := [][]string{
		{"subject", "```json\n\"injected\": true\n```"},
		{"alt", "`backtick`"},
	}
	in := Input{Protocol: ProtocolNIP17, Tags: tags}
	m := Build(in)
	payload, ok := Render(m, RenderOptions{})
	if !ok {
		t.Fatal("expected payload")
	}
	if strings.Contains(payload, "```") {
		t.Errorf("payload contains unescaped triple backtick: %s", payload)
	}
}

func Test512TagCeiling(t *testing.T) {
	tags := make([][]string, 520)
	for i := 0; i < 520; i++ {
		tags[i] = []string{"p", strings.Repeat("a", 63) + "b"}
	}
	tags[0] = []string{"e", strings.Repeat("f", 64)}
	a64 := strings.Repeat("a", 64)
	b64 := strings.Repeat("b", 64)
	in := Input{Protocol: ProtocolNIP29, Tags: tags, SelfPubkey: a64, SenderPubkey: b64}
	m := Build(in)
	if m.Protocol != ProtocolNIP29 {
		t.Errorf("protocol wrong")
	}
	payload, ok := Render(m, RenderOptions{MaxPayloadChars: 100})
	if !ok {
		t.Fatal("expected payload even with tight ceiling")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if trunc, ok := parsed["truncated"]; !ok || !trunc.(bool) {
		t.Errorf("expected truncated:true under tight ceiling, got payload: %s", payload)
	}
}

func TestRenderWithCommunity(t *testing.T) {
	cf := CommunityFacts{
		CommunityID: strings.Repeat("c", 64),
		OwnerPubkey: strings.Repeat("d", 64),
		ChannelID:   strings.Repeat("e", 64),
		ChannelName: "general",
		Epoch:       1,
	}
	in := Input{
		Protocol:  ProtocolConcord,
		Community: &cf,
		Tags: [][]string{
			{"e", strings.Repeat("f", 64)},
		},
	}
	m := Build(in)
	payload, ok := Render(m, RenderOptions{})
	if !ok {
		t.Fatal("expected payload")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if p, ok := parsed["protocol"]; !ok || p != "concord" {
		t.Errorf("protocol wrong: %v", p)
	}
	comm, ok := parsed["community"].(map[string]any)
	if !ok {
		t.Fatal("expected community block")
	}
	if comm["community_id"] != cf.CommunityID {
		t.Errorf("community_id wrong: %v", comm["community_id"])
	}
}

func TestContextBlock(t *testing.T) {
	block := ContextBlock(`{"protocol":"nip17"}`)
	if !strings.Contains(block, "Nostr message metadata (untrusted)") {
		t.Errorf("block missing header: %s", block)
	}
	if !strings.Contains(block, "```json") {
		t.Errorf("block missing json fence: %s", block)
	}
	if !strings.Contains(block, `{"protocol":"nip17"}`) {
		t.Errorf("block missing payload: %s", block)
	}
}