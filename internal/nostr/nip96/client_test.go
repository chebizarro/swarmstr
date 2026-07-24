package nip96

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"metiq/internal/nostr/nip98"
)

func TestDiscoverUploadDownloadDeleteAndPreference(t *testing.T) {
	data := []byte("file bytes")
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	var server *httptest.Server
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/.well-known/nostr/nip96.json":
			fmt.Fprintf(w, `{"api_url":%q,"download_url":%q,"content_types":["image/*"],"plans":{"free":{"max_byte_size":1024}}}`, server.URL+"/api", server.URL+"/cdn")
		case r.Method == http.MethodPost && r.URL.Path == "/api":
			e, err := nip98.DecodeAuthorization(r.Header.Get("Authorization"))
			if err != nil || tag(e.Tags, "payload") != hash {
				t.Errorf("bad upload auth: %v %#v", err, e.Tags)
			}
			if err := r.ParseMultipartForm(2048); err != nil {
				t.Error(err)
			}
			f, _, _ := r.FormFile("file")
			got, _ := io.ReadAll(f)
			if string(got) != string(data) || r.FormValue("no_transform") != "true" {
				t.Errorf("bad multipart")
			}
			w.WriteHeader(201)
			fmt.Fprintf(w, `{"status":"success","message":"ok","nip94_event":{"tags":[["url",%q],["ox",%q],["x",%q]],"content":""}}`, server.URL+"/cdn/"+hash, hash, hash)
		case r.Method == http.MethodGet && r.URL.Path == "/cdn/"+hash+".png":
			w.Write(data)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/"+hash:
			if _, err := nip98.Verify(r.Header.Get("Authorization"), http.MethodDelete, server.URL+"/api/"+hash, nil); err != nil {
				t.Errorf("bad delete auth: %v", err)
			}
			fmt.Fprint(w, `{"status":"success","message":"deleted"}`)
		default:
			http.NotFound(w, r)
		}
	})
	server = httptest.NewTLSServer(h)
	defer server.Close()
	cfg, err := Discover(context.Background(), server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	signer := keyer.NewPlainKeySigner(nostr.Generate())
	client := Client{Config: cfg, HTTP: server.Client(), Keyer: signer}
	response, err := client.Upload(context.Background(), UploadOptions{Filename: "x.png", Data: data, ContentType: "image/png", NoTransform: true})
	if err != nil || response.Status != "success" {
		t.Fatal(response, err)
	}
	download, err := client.Download(context.Background(), hash, "png")
	if err != nil {
		t.Fatal(err)
	}
	downloaded, _ := io.ReadAll(download.Body)
	download.Body.Close()
	if string(downloaded) != string(data) {
		t.Fatal("download mismatch")
	}
	if _, err := client.Delete(context.Background(), hash, ""); err != nil {
		t.Fatal(err)
	}
	pref, err := BuildServerPreference(context.Background(), signer, []string{server.URL})
	if err != nil {
		t.Fatal(err)
	}
	servers, err := ParseServerPreference(pref)
	if err != nil || len(servers) != 1 {
		t.Fatal(servers, err)
	}
}
func TestDiscoveryRejectsRedirectAndUploadHashMismatch(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"api_url":"https://files.example/api"}`) }))
	defer target.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	if _, err := Discover(context.Background(), redirect.URL, redirect.Client()); err == nil {
		t.Fatal("followed redirect")
	}
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			fmt.Fprint(w, `{"status":"success","nip94_event":{"tags":[["url","https://x"],["ox","bad"],["x","bad"]]}}`)
			return
		}
		fmt.Fprintf(w, `{"api_url":%q}`, server.URL)
	}))
	defer server.Close()
	c := Client{Config: ServerConfig{APIURL: server.URL}, HTTP: server.Client(), Keyer: keyer.NewPlainKeySigner(nostr.Generate())}
	if _, err := c.Upload(context.Background(), UploadOptions{Data: []byte("x"), NoTransform: true}); err == nil || !strings.Contains(err.Error(), "hash") {
		t.Fatalf("expected hash error: %v", err)
	}
}
