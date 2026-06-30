package nip86

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	t.Helper()
	sk, err := nostr.SecretKeyFromHex("1111111111111111111111111111111111111111111111111111111111111111")
	if err != nil {
		t.Fatal(err)
	}
	return testKeyer{sk: [32]byte(sk)}
}

func TestDispatchMethods(t *testing.T) {
	store := NewMemoryStore()
	h := &Handler{Store: store}
	raw := []json.RawMessage{json.RawMessage(`"abcd"`), json.RawMessage(`"bad"`)}
	res, err := h.Dispatch(context.Background(), "banpubkey", raw)
	if err != nil || res != true {
		t.Fatalf("banpubkey res=%v err=%v", res, err)
	}
	res, err = h.Dispatch(context.Background(), "listbannedpubkeys", nil)
	if err != nil {
		t.Fatal(err)
	}
	entries := res.([]Entry)
	if len(entries) != 1 || entries[0].PubKey != "abcd" || entries[0].Reason != "bad" {
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

func TestHandlerRejectsMissingAuth(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://relay.example/manage", bytes.NewBufferString(`{"method":"supportedmethods","params":[]}`))
	req.Header.Set("Content-Type", ContentType)
	rr := httptest.NewRecorder()
	NewHandler(NewMemoryStore(), "https://relay.example/manage").ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestHandlerAuthorized(t *testing.T) {
	body := []byte(`{"method":"supportedmethods","params":[]}`)
	_, header, err := nip98.Build(context.Background(), keyer(t), nip98.BuildOptions{Method: http.MethodPost, URL: "https://relay.example/manage", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://relay.example/manage", bytes.NewReader(body))
	req.Header.Set("Content-Type", ContentType)
	req.Header.Set("Authorization", header)
	rr := httptest.NewRecorder()
	NewHandler(NewMemoryStore(), "https://relay.example/manage").ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("supportedmethods")) {
		t.Fatalf("unexpected body %s", rr.Body.String())
	}
}
