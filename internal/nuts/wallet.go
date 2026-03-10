// Package nuts implements the Cashu NUT (Notation, Units, and Token) protocol
// for privacy-preserving ecash payments.
//
// This package implements the core NUT-00 through NUT-06 specifications:
//   NUT-00: Basic token format (V3 base64url encoded proofs)
//   NUT-01: Mint public keys
//   NUT-02: Keysets
//   NUT-03: Swap (split) tokens
//   NUT-04: Mint tokens (Lightning → ecash)
//   NUT-05: Melt tokens (ecash → Lightning payment)
//   NUT-06: Mint info
//
// Note: This is a client-only implementation (no mint). It interfaces with
// compatible Cashu mints via their REST APIs.
package nuts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Proof is a single Cashu proof (unblinded token).
type Proof struct {
	Amount  int    `json:"amount"`
	ID      string `json:"id"`      // keyset ID
	Secret  string `json:"secret"`
	C       string `json:"C"`       // unblinded signature
}

// Token is a V3 Cashu token (a collection of proofs with mint URL and unit).
type Token struct {
	Token []TokenEntry `json:"token"`
	Memo  string       `json:"memo,omitempty"`
	Unit  string       `json:"unit,omitempty"`
}

// TokenEntry groups proofs by mint.
type TokenEntry struct {
	Mint   string  `json:"mint"`
	Proofs []Proof `json:"proofs"`
}

// MintInfo holds the NUT-06 mint information.
type MintInfo struct {
	Name        string         `json:"name"`
	PubKey      string         `json:"pubkey"`
	Version     string         `json:"version"`
	Description string         `json:"description"`
	Motd        string         `json:"motd,omitempty"`
	Nuts        map[string]any `json:"nuts,omitempty"`
}

// Keyset is a NUT-02 mint keyset.
type Keyset struct {
	ID       string            `json:"id"`
	Unit     string            `json:"unit"`
	Active   bool              `json:"active"`
	Keys     map[string]string `json:"keys"` // amount -> pubkey
}

// MintQuote is a NUT-04 mint quote (Lightning invoice).
type MintQuote struct {
	Quote   string `json:"quote"`
	Request string `json:"request"` // BOLT11 invoice
	State   string `json:"state"`   // "UNPAID", "PENDING", or "PAID"
	Expiry  int64  `json:"expiry"`
}

// MeltQuote is a NUT-05 melt quote.
type MeltQuote struct {
	Quote      string `json:"quote"`
	Amount     int    `json:"amount"`
	Unit       string `json:"unit"`
	FeeReserve int    `json:"fee_reserve"`
	State      string `json:"state"`    // "UNPAID", "PENDING", or "PAID"
	Expiry     int64  `json:"expiry"`
	Preimage   string `json:"payment_preimage,omitempty"`
}

// Client is a Cashu wallet client.
type Client struct {
	http    *http.Client
	mintURL string
}

// NewClient creates a Cashu wallet client for the given mint.
func NewClient(mintURL string) *Client {
	return &Client{
		http:    &http.Client{Timeout: 30 * time.Second},
		mintURL: strings.TrimRight(mintURL, "/"),
	}
}

// Info fetches NUT-06 mint info.
func (c *Client) Info(ctx context.Context) (*MintInfo, error) {
	var info MintInfo
	if err := c.get(ctx, "/v1/info", &info); err != nil {
		return nil, fmt.Errorf("nuts info: %w", err)
	}
	return &info, nil
}

// Keysets fetches all keysets (NUT-02).
func (c *Client) Keysets(ctx context.Context) ([]Keyset, error) {
	var resp struct {
		Keysets []Keyset `json:"keysets"`
	}
	if err := c.get(ctx, "/v1/keysets", &resp); err != nil {
		return nil, fmt.Errorf("nuts keysets: %w", err)
	}
	return resp.Keysets, nil
}

// MintQuoteRequest creates a Lightning invoice for minting tokens (NUT-04).
func (c *Client) MintQuoteRequest(ctx context.Context, amount int, unit string) (*MintQuote, error) {
	if unit == "" {
		unit = "sat"
	}
	body, _ := json.Marshal(map[string]any{"amount": amount, "unit": unit})
	var quote MintQuote
	if err := c.post(ctx, "/v1/mint/quote/bolt11", body, &quote); err != nil {
		return nil, fmt.Errorf("nuts mint quote: %w", err)
	}
	return &quote, nil
}

// CheckMintQuote checks whether a Lightning invoice has been paid (NUT-04).
func (c *Client) CheckMintQuote(ctx context.Context, quoteID string) (*MintQuote, error) {
	var quote MintQuote
	if err := c.get(ctx, "/v1/mint/quote/bolt11/"+quoteID, &quote); err != nil {
		return nil, fmt.Errorf("nuts check mint quote: %w", err)
	}
	return &quote, nil
}

// MeltQuoteRequest creates a quote for paying a Lightning invoice with tokens (NUT-05).
func (c *Client) MeltQuoteRequest(ctx context.Context, invoice, unit string) (*MeltQuote, error) {
	if unit == "" {
		unit = "sat"
	}
	body, _ := json.Marshal(map[string]any{"request": invoice, "unit": unit})
	var quote MeltQuote
	if err := c.post(ctx, "/v1/melt/quote/bolt11", body, &quote); err != nil {
		return nil, fmt.Errorf("nuts melt quote: %w", err)
	}
	return &quote, nil
}

// Melt pays a Lightning invoice using the provided proofs (NUT-05).
// Returns the preimage on success.
func (c *Client) Melt(ctx context.Context, quoteID string, proofs []Proof) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"quote":  quoteID,
		"inputs": proofs,
	})
	var resp MeltQuote
	if err := c.post(ctx, "/v1/melt/bolt11", body, &resp); err != nil {
		return "", fmt.Errorf("nuts melt: %w", err)
	}
	if resp.State != "PAID" {
		return "", fmt.Errorf("nuts melt: payment state is %q (expected PAID)", resp.State)
	}
	return resp.Preimage, nil
}

// Balance returns the total value of proofs in a token.
func Balance(token Token) int {
	total := 0
	for _, entry := range token.Token {
		for _, proof := range entry.Proofs {
			total += proof.Amount
		}
	}
	return total
}

// Encode serializes a token to the cashu: URL format.
func Encode(token Token) (string, error) {
	b, err := json.Marshal(token)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(b)
	// V3 token format uses "cashuA" prefix (base64url encoded JSON).
	return "cashuA" + encoded, nil
}

// Decode deserializes a cashu: URL token.
func Decode(tokenStr string) (Token, error) {
	tokenStr = strings.TrimSpace(tokenStr)
	// Strip cashu prefix (cashuA, cashuB, cashuC suffixes indicate version).
	// Strip version prefix: cashuA = V3 (JSON), cashuB = V4 (CBOR), etc.
	for _, prefix := range []string{"cashuA", "cashuB", "cashuC", "cashu"} {
		if strings.HasPrefix(tokenStr, prefix) {
			tokenStr = tokenStr[len(prefix):]
			break
		}
	}
	b, err := base64.RawURLEncoding.DecodeString(tokenStr)
	if err != nil {
		// Try standard base64.
		b, err = base64.StdEncoding.DecodeString(tokenStr)
		if err != nil {
			return Token{}, fmt.Errorf("nuts: decode token: %w", err)
		}
	}
	var token Token
	if err := json.Unmarshal(b, &token); err != nil {
		return Token{}, fmt.Errorf("nuts: unmarshal token: %w", err)
	}
	return token, nil
}

// ─── HTTP helpers ──────────────────────────────────────────────────────────────

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.mintURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) post(ctx context.Context, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, "POST", c.mintURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
