package toolbuiltin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"fiatjaf.com/nostr/nip04"
	"github.com/coder/websocket"

	"metiq/internal/agent"
	"metiq/internal/config"
	"metiq/internal/nostr/nip51"
)

type relayFixture struct {
	srv    *httptest.Server
	mu     sync.Mutex
	events []nostr.Event
}

func newRelayFixture(t *testing.T) *relayFixture {
	t.Helper()
	f := &relayFixture{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		for {
			_, raw, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var frame []json.RawMessage
			if err := json.Unmarshal(raw, &frame); err != nil || len(frame) == 0 {
				continue
			}
			var typ string
			if err := json.Unmarshal(frame[0], &typ); err != nil {
				continue
			}
			switch typ {
			case "EVENT":
				if len(frame) < 2 {
					continue
				}
				var evt nostr.Event
				if err := json.Unmarshal(frame[1], &evt); err != nil {
					continue
				}
				f.mu.Lock()
				f.events = append(f.events, evt)
				f.mu.Unlock()
				_ = relayWrite(conn, []any{"OK", evt.ID.Hex(), true, ""})
			case "REQ":
				if len(frame) < 3 {
					continue
				}
				var subID string
				var filter nostr.Filter
				if err := json.Unmarshal(frame[1], &subID); err != nil {
					continue
				}
				if err := json.Unmarshal(frame[2], &filter); err != nil {
					continue
				}
				events := f.find(filter)
				for _, evt := range events {
					_ = relayWrite(conn, []any{"EVENT", subID, evt})
				}
				_ = relayWrite(conn, []any{"EOSE", subID})
			case "CLOSE":
				return
			}
		}
	}))
	return f
}

func (f *relayFixture) Close() { f.srv.Close() }

func (f *relayFixture) wsURL() string {
	return strings.Replace(f.srv.URL, "http://", "ws://", 1)
}

func relayWrite(conn *websocket.Conn, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return conn.Write(context.Background(), websocket.MessageText, raw)
}

func (f *relayFixture) find(filter nostr.Filter) []nostr.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []nostr.Event
	for _, evt := range f.events {
		if !relayMatchFilter(filter, evt) {
			continue
		}
		out = append(out, evt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	limit := int(filter.Limit)
	if limit <= 0 || limit >= len(out) {
		return out
	}
	return out[:limit]
}

func relayMatchFilter(f nostr.Filter, ev nostr.Event) bool {
	if len(f.Kinds) > 0 {
		found := false
		for _, kind := range f.Kinds {
			if ev.Kind == kind {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(f.Authors) > 0 {
		found := false
		for _, author := range f.Authors {
			if ev.PubKey == author {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(f.IDs) > 0 {
		found := false
		for _, id := range f.IDs {
			if ev.ID == id {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if f.Since > 0 && ev.CreatedAt < f.Since {
		return false
	}
	if f.Until > 0 && ev.CreatedAt > f.Until {
		return false
	}
	for tagKey, vals := range f.Tags {
		if len(vals) == 0 {
			continue
		}
		matched := false
		for _, tag := range ev.Tags {
			if len(tag) < 2 || tag[0] != tagKey {
				continue
			}
			for _, wanted := range vals {
				if tag[1] == wanted {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func testSignerWithHex(t *testing.T, skHex string) nostr.Keyer {
	t.Helper()
	sk, err := nostr.SecretKeyFromHex(skHex)
	if err != nil {
		t.Fatalf("parse generated key: %v", err)
	}
	return keyer.NewPlainKeySigner([32]byte(sk))
}

func testExtendedSignerWithHex(t *testing.T, skHex string) nostr.Keyer {
	t.Helper()
	sk, err := nostr.SecretKeyFromHex(skHex)
	if err != nil {
		t.Fatalf("parse generated key: %v", err)
	}
	return config.NewExtendedSigner(sk)
}

func encryptLegacyNIP04(t *testing.T, senderSKHex string, recipient nostr.PubKey, plaintext string) string {
	t.Helper()
	sk, err := nostr.SecretKeyFromHex(senderSKHex)
	if err != nil {
		t.Fatalf("parse sender sk: %v", err)
	}
	shared, err := nip04.ComputeSharedSecret(recipient, [32]byte(sk))
	if err != nil {
		t.Fatalf("compute shared secret: %v", err)
	}
	ct, err := nip04.Encrypt(plaintext, shared)
	if err != nil {
		t.Fatalf("nip04 encrypt: %v", err)
	}
	return ct
}

func TestRuntimeSmoke_NewNostrWriteHelpers_RelayBacked(t *testing.T) {
	relay := newRelayFixture(t)
	defer relay.Close()

	signer := testExtendedSignerWithHex(t, "8f2a559490f4f35f4b2f8a8e02b2b3ec0ed0098f0d8b0f5e53f62f8c33f1f4a1")
	relays := []string{relay.wsURL()}

	tools := agent.NewToolRegistry()
	nostrOpts := NostrToolOpts{Keyer: signer, Relays: relays}
	tools.RegisterWithDef("nostr_dm_decrypt", NostrDMDecryptTool(nostrOpts), NostrDMDecryptDef)
	tools.RegisterWithDef("nostr_relay_list_set", NostrRelayListSetTool(nostrOpts), NostrRelayListSetDef)
	RegisterNIPTools(tools, nostrOpts)

	listOpts := NostrListToolOpts{Keyer: signer, Relays: relays, Store: nip51.NewListStore()}
	RegisterNostrListSemanticTools(tools, listOpts)

	pub, err := signer.GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("derive pubkey: %v", err)
	}

	// DM decrypt fallback matrix: auto should fall back to nip04 for legacy ciphertext.
	legacySKHex := "5cdbf0f703f2e58f6e767f4793b62483195f9f554f2f44efff66e7f8f1c8a2ea"
	legacySender := testSignerWithHex(t, legacySKHex)
	legacySenderPub, err := legacySender.GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("legacy sender pubkey: %v", err)
	}
	enc04 := encryptLegacyNIP04(t, legacySKHex, pub, "legacy dm")
	dmRes := runToolCallThroughRuntime(t, tools, agent.ToolCall{
		Name: "nostr_dm_decrypt",
		Args: map[string]any{
			"ciphertext":    enc04,
			"sender_pubkey": legacySenderPub.Hex(),
			"scheme":        "auto",
		},
	})
	if dmRes.ToolTraces[0].Error != "" {
		t.Fatalf("dm decrypt fallback failed: %s", dmRes.ToolTraces[0].Error)
	}
	var dmBody map[string]any
	if err := json.Unmarshal([]byte(dmRes.ToolTraces[0].Result), &dmBody); err != nil {
		t.Fatalf("dm result json: %v", err)
	}
	if dmBody["scheme"] != "nip04" || dmBody["plaintext"] != "legacy dm" {
		t.Fatalf("expected nip04 fallback decrypt result, got: %#v", dmBody)
	}

	writeCalls := []agent.ToolCall{
		{Name: "nostr_list_put", Args: map[string]any{"list_type": "allow", "values": []any{legacySenderPub.Hex()}}},
		{Name: "nostr_list_get", Args: map[string]any{"list_type": "allow"}},
		{Name: "nostr_relay_list_set", Args: map[string]any{"both_relays": relays}},
		{Name: "nostr_event_delete", Args: map[string]any{"ids": []any{"deadbeef"}}},
		{Name: "nostr_article_publish", Args: map[string]any{"title": "Smoke", "content": "# Hello\n\nBody #nostr"}},
		{Name: "nostr_report", Args: map[string]any{"report_type": "spam", "target_event_ids": []any{"deadbeef"}}},
	}
	for _, call := range writeCalls {
		res := runToolCallThroughRuntime(t, tools, call)
		tr := res.ToolTraces[0]
		if tr.Error != "" {
			t.Fatalf("%s returned error: %s", call.Name, tr.Error)
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(tr.Result), &body); err != nil {
			t.Fatalf("%s result json: %v (%q)", call.Name, err, tr.Result)
		}
		if call.Name == "nostr_list_get" {
			if _, ok := body["entries"]; !ok {
				t.Fatalf("nostr_list_get expected entries, got: %#v", body)
			}
			continue
		}
		if body["ok"] != true || strings.TrimSpace(anyToString(body["event_id"])) == "" {
			t.Fatalf("%s expected standardized success envelope, got: %#v", call.Name, body)
		}
		if _, ok := body["targets"]; !ok {
			t.Fatalf("%s expected targets field, got: %#v", call.Name, body)
		}
		if _, ok := body["meta"]; !ok {
			t.Fatalf("%s expected meta field, got: %#v", call.Name, body)
		}
	}
	time.Sleep(50 * time.Millisecond)
}
