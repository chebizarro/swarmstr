package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		candidate, base string
		want            bool
	}{
		{"1.2.3", "1.2.2", true},
		{"1.2.2", "1.2.3", false},
		{"2.0.0", "1.9.9", true},
		{"1.0.0", "1.0.0", false},
		{"v2.0.0", "v1.0.0", true},
		{"1.0.0", "0.9.9-dev", true},   // dev suffix makes base older
		{"1.0.1", "1.0.0", true},
		{"1.0.0", "1.0.1", false},
		{"0.0.0-dev", "0.0.0-dev", false},
		{"1.0.0", "1.0.0", false},
	}
	for _, tc := range cases {
		got := isNewer(tc.candidate, tc.base)
		if got != tc.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", tc.candidate, tc.base, got, tc.want)
		}
	}
}

func TestChecker_updateAvailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"tag_name": "v2.0.0"})
	}))
	defer srv.Close()

	c := NewChecker("1.0.0", srv.URL)
	c.httpClient = srv.Client()

	res := c.Check(context.Background(), false)
	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if !res.Available {
		t.Errorf("expected update available: current=%s latest=%s", res.Current, res.Latest)
	}
	if res.Latest != "v2.0.0" {
		t.Errorf("unexpected latest version: %s", res.Latest)
	}
}

func TestChecker_noUpdateAvailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.0.0"})
	}))
	defer srv.Close()

	c := NewChecker("1.0.0", srv.URL)
	c.httpClient = srv.Client()

	res := c.Check(context.Background(), false)
	if res.Available {
		t.Errorf("did not expect update available when versions match")
	}
}

func TestChecker_cachesResult(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		json.NewEncoder(w).Encode(map[string]string{"tag_name": "v2.0.0"})
	}))
	defer srv.Close()

	c := NewChecker("1.0.0", srv.URL)
	c.httpClient = srv.Client()

	c.Check(context.Background(), false)
	c.Check(context.Background(), false)

	if callCount != 1 {
		t.Errorf("expected 1 HTTP call (cached), got %d", callCount)
	}
}

func TestChecker_forceBypassesCache(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		json.NewEncoder(w).Encode(map[string]string{"tag_name": "v2.0.0"})
	}))
	defer srv.Close()

	c := NewChecker("1.0.0", srv.URL)
	c.httpClient = srv.Client()

	c.Check(context.Background(), false)
	c.Check(context.Background(), true) // force=true

	if callCount != 2 {
		t.Errorf("expected 2 HTTP calls with force=true, got %d", callCount)
	}
}

func TestChecker_serverError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewChecker("1.0.0", srv.URL)
	c.httpClient = srv.Client()

	res := c.Check(context.Background(), false)
	if res.Error == "" {
		t.Error("expected error for 500 response")
	}
	if res.Available {
		t.Error("should not report update available on error")
	}
}

func TestChecker_defaultURL(t *testing.T) {
	c := NewChecker("1.0.0", "")
	if c.checkURL != DefaultCheckURL {
		t.Errorf("expected default URL %s, got %s", DefaultCheckURL, c.checkURL)
	}
}
