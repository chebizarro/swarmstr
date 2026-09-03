package blossom

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

func decodeAuthEvent(t *testing.T, header string) nostr.Event {
	t.Helper()
	const prefix = "Nostr "
	if len(header) <= len(prefix) || header[:len(prefix)] != prefix {
		t.Fatalf("unexpected Authorization header %q", header)
	}
	data, err := base64.RawURLEncoding.DecodeString(header[len(prefix):])
	if err != nil {
		t.Fatalf("decode auth token: %v", err)
	}
	var evt nostr.Event
	if err := json.Unmarshal(data, &evt); err != nil {
		t.Fatalf("decode auth event: %v", err)
	}
	if !evt.CheckID() || !evt.VerifySignature() {
		t.Fatal("auth event has invalid id or signature")
	}
	return evt
}

func requireTag(t *testing.T, evt nostr.Event, name, want string) {
	t.Helper()
	for _, tag := range evt.Tags {
		if len(tag) >= 2 && tag[0] == name {
			if tag[1] != want {
				t.Fatalf("tag %q = %q, want %q", name, tag[1], want)
			}
			return
		}
	}
	t.Fatalf("missing tag %q in %#v", name, evt.Tags)
}

func testClient(t *testing.T) *Client {
	t.Helper()
	sk := nostr.Generate()
	ctx := context.Background()
	k := keyer.NewPlainKeySigner([32]byte(sk))
	c, err := NewClient(ctx, k)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewClient(t *testing.T) {
	c := testClient(t)
	if c.pubkey == "" {
		t.Fatal("pubkey should not be empty")
	}
	if c.http == nil {
		t.Fatal("http client should not be nil")
	}
}

func TestUpload_Success(t *testing.T) {
	data := []byte("hello blossom")
	hash := sha256.Sum256(data)
	hashHex := hex.EncodeToString(hash[:])

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/upload" {
			t.Errorf("expected /upload, got %s", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		if auth == "" {
			t.Error("missing Authorization header")
		}
		evt := decodeAuthEvent(t, auth)
		if evt.Kind != nostr.Kind(24242) {
			t.Fatalf("auth kind = %d, want 24242", evt.Kind)
		}
		requireTag(t, evt, "u", srv.URL+"/upload")
		requireTag(t, evt, "method", "PUT")
		requireTag(t, evt, "t", "upload")
		requireTag(t, evt, "x", hashHex)
		requireTag(t, evt, "m", "text/plain")
		requireTag(t, evt, "size", "13")

		body, _ := io.ReadAll(r.Body)
		if string(body) != string(data) {
			t.Errorf("body mismatch")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(BlobDescriptor{
			SHA256: hashHex,
			Size:   int64(len(data)),
			Type:   "text/plain",
		})
	}))
	defer srv.Close()

	c := testClient(t)
	c.http = srv.Client()

	desc, err := c.Upload(context.Background(), srv.URL, data, "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if desc.SHA256 != hashHex {
		t.Errorf("expected hash %s, got %s", hashHex, desc.SHA256)
	}
}

func TestUpload_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	c := testClient(t)
	c.http = srv.Client()

	_, err := c.Upload(context.Background(), srv.URL, []byte("x"), "text/plain")
	if err == nil {
		t.Fatal("expected error for 500")
	}
}

func TestDownload_Success(t *testing.T) {
	data := []byte("blob content")
	hash := sha256.Sum256(data)
	hashHex := hex.EncodeToString(hash[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(data)
	}))
	defer srv.Close()

	c := testClient(t)
	c.http = srv.Client()

	got, ct, err := c.Download(context.Background(), srv.URL, hashHex)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Errorf("data mismatch")
	}
	if ct != "application/octet-stream" {
		t.Errorf("unexpected content-type %s", ct)
	}
}

func TestDownload_HashMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("wrong data"))
	}))
	defer srv.Close()

	c := testClient(t)
	c.http = srv.Client()

	_, _, err := c.Download(context.Background(), srv.URL, "0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("expected hash mismatch error")
	}
}

func TestDownload_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	c := testClient(t)
	c.http = srv.Client()

	_, _, err := c.Download(context.Background(), srv.URL, "abc123")
	if err == nil {
		t.Fatal("expected not found error")
	}
}

func TestList_Success(t *testing.T) {
	blobs := []BlobDescriptor{
		{SHA256: "aaa", Size: 100},
		{SHA256: "bbb", Size: 200},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(blobs)
	}))
	defer srv.Close()

	c := testClient(t)
	c.http = srv.Client()

	got, err := c.List(context.Background(), srv.URL, "pubkey123")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 blobs, got %d", len(got))
	}
}

func TestDelete_Success(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		auth := r.Header.Get("Authorization")
		if auth == "" {
			t.Error("missing Authorization header")
		}
		evt := decodeAuthEvent(t, auth)
		if evt.Kind != nostr.Kind(24242) {
			t.Fatalf("auth kind = %d, want 24242", evt.Kind)
		}
		requireTag(t, evt, "u", srv.URL+"/abc123hash")
		requireTag(t, evt, "method", "DELETE")
		requireTag(t, evt, "t", "delete")
		requireTag(t, evt, "x", "abc123hash")

		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := testClient(t)
	c.http = srv.Client()

	err := c.Delete(context.Background(), srv.URL, "abc123hash")
	if err != nil {
		t.Fatal(err)
	}
}

func TestDelete_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte("forbidden"))
	}))
	defer srv.Close()

	c := testClient(t)
	c.http = srv.Client()

	err := c.Delete(context.Background(), srv.URL, "hash")
	if err == nil {
		t.Fatal("expected error for 403")
	}
}

func TestMirror_Success(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/mirror" {
			t.Errorf("expected /mirror, got %s", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		if auth == "" {
			t.Error("missing Authorization header")
		}
		evt := decodeAuthEvent(t, auth)
		if evt.Kind != nostr.Kind(24242) {
			t.Fatalf("auth kind = %d, want 24242", evt.Kind)
		}
		requireTag(t, evt, "u", srv.URL+"/mirror")
		requireTag(t, evt, "method", "PUT")
		requireTag(t, evt, "t", "mirror")
		requireTag(t, evt, "x", "sourcehash")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(BlobDescriptor{SHA256: "mirrored"})
	}))
	defer srv.Close()

	c := testClient(t)
	c.http = srv.Client()

	desc, err := c.Mirror(context.Background(), srv.URL, "sourcehash", "https://example.com/blob")
	if err != nil {
		t.Fatal(err)
	}
	if desc.SHA256 != "mirrored" {
		t.Errorf("expected mirrored, got %s", desc.SHA256)
	}
}

func TestBlobDescriptor_JSON(t *testing.T) {
	d := BlobDescriptor{
		URL:    "https://example.com/abc",
		SHA256: "abc",
		Size:   1024,
		Type:   "image/png",
	}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var d2 BlobDescriptor
	if err := json.Unmarshal(data, &d2); err != nil {
		t.Fatal(err)
	}
	if d2.SHA256 != d.SHA256 || d2.Size != d.Size {
		t.Error("round-trip mismatch")
	}
}
