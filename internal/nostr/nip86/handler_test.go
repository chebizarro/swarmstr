package nip86

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/nostr/nip98"
)

type testKeyer struct{ sk [32]byte }

func (k testKeyer) GetPublicKey(context.Context) (nostr.PubKey, error) {
	return nostr.GetPublicKey(k.sk), nil
}
func (k testKeyer) SignEvent(_ context.Context, evt *nostr.Event) error { return evt.Sign(k.sk) }
func keyer(t *testing.T) testKeyer {
	return keyerFromHex(t, "1111111111111111111111111111111111111111111111111111111111111111")
}
func keyerFromHex(t *testing.T, secret string) testKeyer {
	t.Helper()
	sk, err := nostr.SecretKeyFromHex(secret)
	if err != nil {
		t.Fatal(err)
	}
	return testKeyer{sk: [32]byte(sk)}
}

func TestDispatchMethods(t *testing.T) {
	store := NewMemoryStore()
	h := &Handler{Store: store}
	pubkey := strings.Repeat("a", 64)
	raw := []json.RawMessage{json.RawMessage(`"` + pubkey + `"`), json.RawMessage(`"bad"`)}
	res, err := h.Dispatch(context.Background(), "banpubkey", raw)
	if err != nil || res != true {
		t.Fatalf("banpubkey res=%v err=%v", res, err)
	}
	res, err = h.Dispatch(context.Background(), "listbannedpubkeys", nil)
	if err != nil {
		t.Fatal(err)
	}
	entries := res.([]Entry)
	if len(entries) != 1 || entries[0].PubKey != pubkey || entries[0].Reason != "bad" {
		t.Fatalf("unexpected entries %+v", entries)
	}
	if _, err := h.Dispatch(context.Background(), "allowkind", []json.RawMessage{json.RawMessage(`1`)}); err != nil {
		t.Fatal(err)
	}
	res, err = h.Dispatch(context.Background(), "listallowedkinds", nil)
	if err != nil {
		t.Fatal(err)
	}
	kinds := res.([]int)
	if len(kinds) != 1 || kinds[0] != 1 {
		t.Fatalf("unexpected kinds %+v", kinds)
	}
}

func TestDispatchCurrentMethodsAndDropsStaleAdvertisement(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	h := &Handler{Store: store}
	pubkey := strings.Repeat("a", 64)
	eventID := strings.Repeat("b", 64)

	for _, method := range []string{"banpubkey", "unbanpubkey", "allowpubkey", "unallowpubkey"} {
		if result, err := h.Dispatch(ctx, method, rawParams(t, pubkey, "reason")); err != nil || result != true {
			t.Fatalf("%s result=%v err=%v", method, result, err)
		}
	}
	if banned, _ := store.ListBannedPubKeys(ctx); len(banned) != 0 {
		t.Fatalf("unban did not remove pubkey: %+v", banned)
	}
	if allowed, _ := store.ListAllowedPubKeys(ctx); len(allowed) != 0 {
		t.Fatalf("unallow did not remove pubkey: %+v", allowed)
	}

	if _, err := h.Dispatch(ctx, "createrole", rawParams(t, "moderator", "Moderator", "Can moderate", "#112233", 10)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Dispatch(ctx, "editrole", rawParams(t, "moderator", "Senior Moderator", "Can moderate", "#445566", 20)); err != nil {
		t.Fatal(err)
	}
	if role, ok := store.Role("moderator"); !ok || role.Label != "Senior Moderator" || role.Order != 20 {
		t.Fatalf("edited role = %+v ok=%v", role, ok)
	}
	if _, err := h.Dispatch(ctx, "assignrole", rawParams(t, pubkey, "moderator")); err != nil {
		t.Fatal(err)
	}
	if !store.HasRole(pubkey, "moderator") {
		t.Fatal("role was not assigned")
	}
	if _, err := h.Dispatch(ctx, "unassignrole", rawParams(t, pubkey, "moderator")); err != nil {
		t.Fatal(err)
	}
	if store.HasRole(pubkey, "moderator") {
		t.Fatal("role was not unassigned")
	}
	if _, err := h.Dispatch(ctx, "deleterole", rawParams(t, "moderator")); err != nil {
		t.Fatal(err)
	}

	store.FlagEventForModeration(eventID, "review")
	result, err := h.Dispatch(ctx, "listeventsneedingmoderation", nil)
	if err != nil {
		t.Fatal(err)
	}
	moderation := result.([]Entry)
	if len(moderation) != 1 || moderation[0].ID != eventID || moderation[0].Reason != "review" {
		t.Fatalf("moderation = %+v", moderation)
	}
	if _, err := h.Dispatch(ctx, "allowevent", rawParams(t, eventID, "approved")); err != nil {
		t.Fatal(err)
	}
	result, _ = h.Dispatch(ctx, "listeventsneedingmoderation", nil)
	if len(result.([]Entry)) != 0 {
		t.Fatal("allowevent did not clear moderation queue")
	}

	if _, err := h.Dispatch(ctx, "banevent", rawParams(t, eventID, "malformed")); err != nil {
		t.Fatal(err)
	}
	if result, err := h.Dispatch(ctx, "listbannedevents", nil); err != nil || len(result.([]Entry)) != 1 {
		t.Fatalf("listbannedevents result=%v err=%v", result, err)
	}
	for method, value := range map[string]string{
		"changerelayname": "Relay Name", "changerelaydescription": "Relay Description", "changerelayicon": "https://relay.example/icon.png",
	} {
		if _, err := h.Dispatch(ctx, method, rawParams(t, value)); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	if _, err := h.Dispatch(ctx, "allowkind", rawParams(t, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Dispatch(ctx, "disallowkind", rawParams(t, 1)); err != nil {
		t.Fatal(err)
	}
	if result, err := h.Dispatch(ctx, "listallowedkinds", nil); err != nil || len(result.([]int)) != 0 {
		t.Fatalf("listallowedkinds result=%v err=%v", result, err)
	}
	if _, err := h.Dispatch(ctx, "blockip", rawParams(t, "192.0.2.1", "abuse")); err != nil {
		t.Fatal(err)
	}
	if result, err := h.Dispatch(ctx, "listblockedips", nil); err != nil || len(result.([]Entry)) != 1 {
		t.Fatalf("listblockedips result=%v err=%v", result, err)
	}
	if _, err := h.Dispatch(ctx, "unblockip", rawParams(t, "192.0.2.1")); err != nil {
		t.Fatal(err)
	}

	result, err = h.Dispatch(ctx, "supportedmethods", nil)
	if err != nil || len(result.([]string)) != len(SupportedMethods) {
		t.Fatalf("supportedmethods result=%v err=%v", result, err)
	}
	for _, method := range SupportedMethods {
		if method == "supportedmethods" || method == "listdisallowedkinds" {
			t.Fatalf("non-current method %q is advertised", method)
		}
	}
	if _, err := h.Dispatch(ctx, "listdisallowedkinds", nil); err == nil {
		t.Fatal("stale listdisallowedkinds should be unsupported")
	}
}

func TestHandlerAuthorizationUsesVerifiedSignerNotRequestParams(t *testing.T) {
	admin := keyer(t)
	adminPubKey, _ := admin.GetPublicKey(context.Background())
	outsider := keyerFromHex(t, "2222222222222222222222222222222222222222222222222222222222222222")
	body, err := json.Marshal(map[string]any{"method": "banpubkey", "params": []any{adminPubKey.Hex(), "spoof"}})
	if err != nil {
		t.Fatal(err)
	}
	_, header, err := nip98.Build(context.Background(), outsider, nip98.BuildOptions{Method: http.MethodPost, URL: "https://relay.example/manage", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore(adminPubKey.Hex())
	req := httptest.NewRequest(http.MethodPost, "https://relay.example/manage", bytes.NewReader(body))
	req.Header.Set("Content-Type", ContentType)
	req.Header.Set("Authorization", header)
	rr := httptest.NewRecorder()
	NewHandler(store, "https://relay.example/manage").ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if banned, _ := store.ListBannedPubKeys(context.Background()); len(banned) != 0 {
		t.Fatalf("unauthorized signer changed store: %+v", banned)
	}
}

func TestHandlerRejectsMissingAuth(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://relay.example/manage", bytes.NewBufferString(`{"method":"supportedmethods","params":[]}`))
	req.Header.Set("Content-Type", ContentType)
	rr := httptest.NewRecorder()
	NewHandler(NewMemoryStore(), "https://relay.example/manage").ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rr.Code)
	}
}

func rawParams(t *testing.T, values ...any) []json.RawMessage {
	t.Helper()
	out := make([]json.RawMessage, len(values))
	for i, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = raw
	}
	return out
}

func TestHandlerAuthorized(t *testing.T) {
	body := []byte(`{"method":"supportedmethods","params":[]}`)
	adminKeyer := keyer(t)
	adminPubKey, err := adminKeyer.GetPublicKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, header, err := nip98.Build(context.Background(), adminKeyer, nip98.BuildOptions{Method: http.MethodPost, URL: "https://relay.example/manage", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://relay.example/manage", bytes.NewReader(body))
	req.Header.Set("Content-Type", ContentType)
	req.Header.Set("Authorization", header)
	rr := httptest.NewRecorder()
	NewHandler(NewMemoryStore(adminPubKey.Hex()), "https://relay.example/manage").ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("banpubkey")) || bytes.Contains(rr.Body.Bytes(), []byte("listdisallowedkinds")) {
		t.Fatalf("unexpected body %s", rr.Body.String())
	}
}
