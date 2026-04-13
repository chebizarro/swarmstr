package nuts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestNewClient_TrimsTrailingSlash(t *testing.T) {
	client := NewClient("https://mint.example/")
	if client.mintURL != "https://mint.example" {
		t.Fatalf("unexpected mintURL: %q", client.mintURL)
	}
	if client.http == nil {
		t.Fatal("expected http client")
	}
}

func TestBalance(t *testing.T) {
	token := Token{
		Token: []TokenEntry{{
			Mint:   "https://mint.example",
			Proofs: []Proof{{Amount: 1}, {Amount: 2}, {Amount: 5}},
		}},
	}
	if got := Balance(token); got != 8 {
		t.Fatalf("unexpected balance: %d", got)
	}
}

func TestEncodeDecode_RoundTrip(t *testing.T) {
	original := Token{
		Token: []TokenEntry{{
			Mint:   "https://mint.example",
			Proofs: []Proof{{Amount: 1, ID: "k1", Secret: "s1", C: "c1"}},
		}},
		Memo: "memo",
		Unit: "sat",
	}

	encoded, err := Encode(original)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.HasPrefix(encoded, "cashuA") {
		t.Fatalf("expected cashuA prefix, got %q", encoded)
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(decoded, original) {
		t.Fatalf("round trip mismatch:\n got: %#v\nwant: %#v", decoded, original)
	}
}

func TestDecode_AcceptsPrefixes(t *testing.T) {
	token := Token{Token: []TokenEntry{{Mint: "https://mint.example", Proofs: []Proof{{Amount: 3}}}}, Unit: "sat"}
	payload, err := json.Marshal(token)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(payload)

	for _, prefix := range []string{"cashuA", "cashuB", "cashuC", "cashu"} {
		t.Run(prefix, func(t *testing.T) {
			decoded, err := Decode(prefix + raw)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !reflect.DeepEqual(decoded, token) {
				t.Fatalf("decoded mismatch: got %#v want %#v", decoded, token)
			}
		})
	}
}

func TestDecode_AcceptsStandardBase64AndWhitespace(t *testing.T) {
	token := Token{Token: []TokenEntry{{Mint: "https://mint.example", Proofs: []Proof{{Amount: 7}}}}, Unit: "sat"}
	payload, err := json.Marshal(token)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(payload)

	decoded, err := Decode("  cashuA" + encoded + "  ")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(decoded, token) {
		t.Fatalf("decoded mismatch: got %#v want %#v", decoded, token)
	}
}

func TestDecode_InvalidToken(t *testing.T) {
	_, err := Decode("not-base64")
	if err == nil {
		t.Fatal("expected decode error")
	}
	if !strings.Contains(err.Error(), "nuts: decode token") && !strings.Contains(err.Error(), "nuts: unmarshal token") {
		t.Fatalf("expected wrapped decode or unmarshal error, got %v", err)
	}
}

func TestDecode_InvalidJSONPayload(t *testing.T) {
	encoded := base64.RawURLEncoding.EncodeToString([]byte("not-json"))
	if _, err := Decode("cashuA" + encoded); err == nil || !strings.Contains(err.Error(), "nuts: unmarshal token") {
		t.Fatalf("expected unmarshal error, got %v", err)
	}
}

func TestClient_Info(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/v1/info" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("unexpected Accept header: %q", got)
		}
		_ = json.NewEncoder(w).Encode(MintInfo{Name: "mint", Version: "1.0.0", PubKey: "pub"})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	info, err := client.Info(context.Background())
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.Name != "mint" || info.Version != "1.0.0" || info.PubKey != "pub" {
		t.Fatalf("unexpected info: %#v", info)
	}
}

func TestClient_Keysets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/keysets" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keysets": []Keyset{{ID: "k1", Unit: "sat", Active: true, Keys: map[string]string{"1": "pub1"}}}})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	keysets, err := client.Keysets(context.Background())
	if err != nil {
		t.Fatalf("keysets: %v", err)
	}
	if len(keysets) != 1 || keysets[0].ID != "k1" || keysets[0].Keys["1"] != "pub1" {
		t.Fatalf("unexpected keysets: %#v", keysets)
	}
}

func TestClient_MintQuoteRequest_DefaultsUnitAndPostsJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/v1/mint/quote/bolt11" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("unexpected Content-Type: %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if req["amount"].(float64) != 42 || req["unit"].(string) != "sat" {
			t.Fatalf("unexpected request body: %#v", req)
		}
		_ = json.NewEncoder(w).Encode(MintQuote{Quote: "q1", Request: "lnbc1", State: "UNPAID", Expiry: 123})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	quote, err := client.MintQuoteRequest(context.Background(), 42, "")
	if err != nil {
		t.Fatalf("mint quote: %v", err)
	}
	if quote.Quote != "q1" || quote.Request != "lnbc1" || quote.State != "UNPAID" {
		t.Fatalf("unexpected quote: %#v", quote)
	}
}

func TestClient_CheckMintQuote(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/mint/quote/bolt11/quote-123" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(MintQuote{Quote: "quote-123", State: "PAID"})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	quote, err := client.CheckMintQuote(context.Background(), "quote-123")
	if err != nil {
		t.Fatalf("check mint quote: %v", err)
	}
	if quote.Quote != "quote-123" || quote.State != "PAID" {
		t.Fatalf("unexpected quote: %#v", quote)
	}
}

func TestClient_MeltQuoteRequest_DefaultsUnit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/melt/quote/bolt11" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if req["request"].(string) != "invoice" || req["unit"].(string) != "sat" {
			t.Fatalf("unexpected request body: %#v", req)
		}
		_ = json.NewEncoder(w).Encode(MeltQuote{Quote: "mq1", Amount: 5, Unit: "sat", State: "UNPAID"})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	quote, err := client.MeltQuoteRequest(context.Background(), "invoice", "")
	if err != nil {
		t.Fatalf("melt quote: %v", err)
	}
	if quote.Quote != "mq1" || quote.Amount != 5 || quote.Unit != "sat" {
		t.Fatalf("unexpected quote: %#v", quote)
	}
}

func TestClient_Melt_ReturnsPreimageWhenPaid(t *testing.T) {
	proofs := []Proof{{Amount: 2, ID: "k1", Secret: "s1", C: "c1"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/melt/bolt11" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var req struct {
			Quote  string  `json:"quote"`
			Inputs []Proof `json:"inputs"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if req.Quote != "quote-1" || !reflect.DeepEqual(req.Inputs, proofs) {
			t.Fatalf("unexpected melt request: %#v", req)
		}
		_ = json.NewEncoder(w).Encode(MeltQuote{Quote: "quote-1", State: "PAID", Preimage: "preimage-1"})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	preimage, err := client.Melt(context.Background(), "quote-1", proofs)
	if err != nil {
		t.Fatalf("melt: %v", err)
	}
	if preimage != "preimage-1" {
		t.Fatalf("unexpected preimage: %q", preimage)
	}
}

func TestClient_Melt_RejectsNonPaidState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(MeltQuote{Quote: "quote-1", State: "PENDING"})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.Melt(context.Background(), "quote-1", []Proof{{Amount: 1}})
	if err == nil || !strings.Contains(err.Error(), `payment state is "PENDING"`) {
		t.Fatalf("expected non-paid error, got %v", err)
	}
}

func TestClient_HTTPErrorWrapping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "mint failed", http.StatusBadGateway)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	if _, err := client.Info(context.Background()); err == nil || !strings.Contains(err.Error(), "nuts info: mint returned 502: mint failed") {
		t.Fatalf("expected wrapped info error, got %v", err)
	}
	if _, err := client.MintQuoteRequest(context.Background(), 1, "sat"); err == nil || !strings.Contains(err.Error(), "nuts mint quote: mint returned 502: mint failed") {
		t.Fatalf("expected wrapped mint quote error, got %v", err)
	}
}
