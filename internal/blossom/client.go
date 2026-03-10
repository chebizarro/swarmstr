// Package blossom implements the Blossom blob storage protocol.
//
// BUD-01: GET/HEAD /{sha256}         – download a blob
// BUD-02: PUT /upload                – upload with NIP-98 auth
// BUD-03: GET /list/{pubkey}         – list blobs for a pubkey
// BUD-04: DELETE /{sha256}           – delete with NIP-98 auth
// BUD-05: PUT /mirror                – mirror a blob from another server
//
// All authenticated endpoints use NIP-98 HTTP auth (kind 27235).
package blossom

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"
)

// BlobDescriptor is returned by the server for uploaded blobs.
type BlobDescriptor struct {
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Type     string `json:"type,omitempty"`
	Uploaded int64  `json:"uploaded,omitempty"`
}

// Client is a Blossom protocol HTTP client.
type Client struct {
	http   *http.Client
	keyer  nostr.Keyer
	pubkey string
}

// NewClient creates a new Blossom client using the given signing keyer.
func NewClient(ctx context.Context, keyer nostr.Keyer) (*Client, error) {
	pk, err := keyer.GetPublicKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("blossom: get public key: %w", err)
	}
	return &Client{
		http:   &http.Client{Timeout: 60 * time.Second},
		keyer:  keyer,
		pubkey: pk.Hex(),
	}, nil
}

// Upload sends a blob to a Blossom server (BUD-02).
// Returns the BlobDescriptor from the server response.
func (c *Client) Upload(ctx context.Context, serverURL string, data []byte, mimeType string) (*BlobDescriptor, error) {
	hash := sha256.Sum256(data)
	hashHex := hex.EncodeToString(hash[:])

	uploadURL := strings.TrimRight(serverURL, "/") + "/upload"
	authToken, err := c.makeAuthToken(ctx, "PUT", uploadURL, hashHex, mimeType, int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("blossom upload: make auth: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", uploadURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("blossom upload: create request: %w", err)
	}
	req.Header.Set("Authorization", "Nostr "+authToken)
	req.Header.Set("Content-Type", mimeType)
	req.Header.Set("X-SHA-256", hashHex)
	req.ContentLength = int64(len(data))

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("blossom upload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("blossom upload: server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var desc BlobDescriptor
	if err := json.NewDecoder(resp.Body).Decode(&desc); err != nil {
		return nil, fmt.Errorf("blossom upload: decode response: %w", err)
	}
	if desc.SHA256 == "" {
		desc.SHA256 = hashHex
	}
	return &desc, nil
}

// Download fetches a blob from a Blossom server by SHA256 hash (BUD-01).
// Returns the raw bytes and content type.
func (c *Client) Download(ctx context.Context, serverURL, sha256Hex string) ([]byte, string, error) {
	downloadURL := strings.TrimRight(serverURL, "/") + "/" + sha256Hex
	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("blossom download: create request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("blossom download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, "", fmt.Errorf("blossom download: blob %s not found", sha256Hex)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("blossom download: server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("blossom download: read body: %w", err)
	}

	// Verify hash integrity.
	actual := sha256.Sum256(data)
	actualHex := hex.EncodeToString(actual[:])
	if !strings.EqualFold(actualHex, sha256Hex) {
		return nil, "", fmt.Errorf("blossom download: hash mismatch (got %s, expected %s)", actualHex, sha256Hex)
	}

	contentType := resp.Header.Get("Content-Type")
	return data, contentType, nil
}

// List fetches the blob list for a pubkey from a Blossom server (BUD-03).
func (c *Client) List(ctx context.Context, serverURL, pubkeyHex string) ([]BlobDescriptor, error) {
	listURL := strings.TrimRight(serverURL, "/") + "/list/" + pubkeyHex

	req, err := http.NewRequestWithContext(ctx, "GET", listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("blossom list: create request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("blossom list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("blossom list: server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var blobs []BlobDescriptor
	if err := json.NewDecoder(resp.Body).Decode(&blobs); err != nil {
		return nil, fmt.Errorf("blossom list: decode response: %w", err)
	}
	return blobs, nil
}

// Delete removes a blob from a Blossom server (BUD-04).
func (c *Client) Delete(ctx context.Context, serverURL, sha256Hex string) error {
	deleteURL := strings.TrimRight(serverURL, "/") + "/" + sha256Hex
	authToken, err := c.makeAuthToken(ctx, "DELETE", deleteURL, sha256Hex, "", 0)
	if err != nil {
		return fmt.Errorf("blossom delete: make auth: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "DELETE", deleteURL, nil)
	if err != nil {
		return fmt.Errorf("blossom delete: create request: %w", err)
	}
	req.Header.Set("Authorization", "Nostr "+authToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("blossom delete: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("blossom delete: server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// Mirror requests that the server mirror a blob from another server (BUD-05).
func (c *Client) Mirror(ctx context.Context, serverURL, sha256Hex, sourceURL string) (*BlobDescriptor, error) {
	mirrorURL := strings.TrimRight(serverURL, "/") + "/mirror"
	body, _ := json.Marshal(map[string]string{"url": sourceURL})

	authToken, err := c.makeAuthToken(ctx, "PUT", mirrorURL, sha256Hex, "application/json", int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("blossom mirror: make auth: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", mirrorURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("blossom mirror: create request: %w", err)
	}
	req.Header.Set("Authorization", "Nostr "+authToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("blossom mirror: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("blossom mirror: server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var desc BlobDescriptor
	if err := json.NewDecoder(resp.Body).Decode(&desc); err != nil {
		return nil, fmt.Errorf("blossom mirror: decode response: %w", err)
	}
	return &desc, nil
}

// makeAuthToken creates a NIP-98 HTTP auth token (kind 27235) for authenticated requests.
func (c *Client) makeAuthToken(ctx context.Context, method, rawURL, sha256Hex, mimeType string, size int64) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse URL: %w", err)
	}
	// Use canonical URL (scheme + host + path, no query).
	canonical := parsed.Scheme + "://" + parsed.Host + parsed.Path

	tags := nostr.Tags{
		{"u", canonical},
		{"method", strings.ToUpper(method)},
		{"expiration", fmt.Sprintf("%d", time.Now().Add(5*time.Minute).Unix())},
	}
	if sha256Hex != "" {
		tags = append(tags, nostr.Tag{"x", sha256Hex})
	}
	if mimeType != "" {
		tags = append(tags, nostr.Tag{"m", mimeType})
	}
	if size > 0 {
		tags = append(tags, nostr.Tag{"size", fmt.Sprintf("%d", size)})
	}

	evt := nostr.Event{
		Kind:      27235, // NIP-98 HTTP auth
		CreatedAt: nostr.Now(),
		Tags:      tags,
		Content:   "",
	}

	if err := c.keyer.SignEvent(ctx, &evt); err != nil {
		return "", fmt.Errorf("sign auth event: %w", err)
	}

	evtJSON, err := json.Marshal(evt)
	if err != nil {
		return "", fmt.Errorf("marshal auth event: %w", err)
	}

	// Encode as base64url (no padding) per NIP-98.
	return base64.RawURLEncoding.EncodeToString(evtJSON), nil
}
