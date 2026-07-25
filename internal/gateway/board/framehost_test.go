package board

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func frameGet(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestFrameHostServesTicketedWidget(t *testing.T) {
	s := NewStore()
	putHTMLWidget(t, s, "sess", "chart", nil)
	h := NewFrameHandler(s)
	w := s.GetSnapshotWithTickets("sess").Widgets[0]

	rec := frameGet(t, h, w.FrameURL)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "<p>chart</p>" {
		t.Fatalf("unexpected body: %q", rec.Body.String())
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox allow-scripts") || !strings.Contains(csp, "connect-src 'none'") {
		t.Fatalf("unexpected CSP: %s", csp)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("unexpected content type: %s", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("unexpected cache control: %s", cc)
	}

	// Non-GET is rejected after CORS headers.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, w.FrameURL, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestFrameHostGrantedOriginsWidenConnectSrc(t *testing.T) {
	s := NewStore()
	snap, err := s.PutWidget(PutParams{
		SessionKey: "sess",
		Name:       "chart",
		Content:    PutContent{Kind: ContentKindHTML, HTML: "<p>net</p>"},
		Declared:   &Declared{NetOrigins: []string{"https://api.example.com"}},
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	w := snap.Widgets[0]
	if _, err := s.Grant("sess", "chart", GrantGranted, w.Revision, w.InstanceID); err != nil {
		t.Fatalf("grant: %v", err)
	}
	minted := s.GetSnapshotWithTickets("sess").Widgets[0]
	rec := frameGet(t, NewFrameHandler(s), minted.FrameURL)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "connect-src https://api.example.com") {
		t.Fatalf("granted origin missing from CSP: %s", csp)
	}
}

func TestFrameHostRejectsUnauthorized(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.now = func() time.Time { return now }
	putHTMLWidget(t, s, "sess", "chart", nil)
	putHTMLWidget(t, s, "sess", "other", nil)
	h := NewFrameHandler(s)
	ticket := ticketFor(t, s, "sess", "chart")

	cases := map[string]string{
		"missing ticket": HTTPPathPrefix + "sess/chart/index.html",
		"forged ticket":  HTTPPathPrefix + "sess/chart/index.html?bt=v1.bogus.bogus",
		"wrong widget":   HTTPPathPrefix + "sess/other/index.html?bt=" + url.QueryEscape(ticket),
		"wrong session":  HTTPPathPrefix + "nope/chart/index.html?bt=" + url.QueryEscape(ticket),
	}
	for label, target := range cases {
		if rec := frameGet(t, h, target); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: expected 401, got %d", label, rec.Code)
		}
	}

	// Malformed paths are 404s.
	for _, target := range []string{
		HTTPPathPrefix + "sess/chart/other.html?bt=x",
		HTTPPathPrefix + "sess/chart?bt=x",
		HTTPPathPrefix + "sess/UPPER/index.html?bt=x",
	} {
		if rec := frameGet(t, h, target); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: expected 404, got %d", target, rec.Code)
		}
	}

	// Expired tickets are 401s.
	now = now.Add(ViewTicketTTL + time.Second)
	if rec := frameGet(t, h, HTTPPathPrefix+"sess/chart/index.html?bt="+url.QueryEscape(ticket)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired ticket: expected 401, got %d", rec.Code)
	}

	// Re-put stales tickets immediately.
	now = time.Now()
	ticket = ticketFor(t, s, "sess", "chart")
	putHTMLWidget(t, s, "sess", "chart", nil)
	if rec := frameGet(t, h, HTTPPathPrefix+"sess/chart/index.html?bt="+url.QueryEscape(ticket)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("stale ticket: expected 401, got %d", rec.Code)
	}
}

func TestFrameHostPendingWidgetNeverServed(t *testing.T) {
	s := NewStore()
	snap := putHTMLWidget(t, s, "sess", "gated", []string{"prompt"})
	w := snap.Widgets[0]
	// Forge a plausible URL by minting a ticket internally against the
	// pending document: ResolveViewTicket must still refuse it.
	ticket, err := mintViewTicket(s.ticketSecret, ticketClaims{
		SessionKey:     "sess",
		Name:           "gated",
		Revision:       w.Revision,
		ViewGeneration: w.InstanceID,
		ExpiresAtMs:    time.Now().Add(time.Minute).UnixMilli(),
		Nonce:          newTicketNonce(),
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	rec := frameGet(t, NewFrameHandler(s), HTTPPathPrefix+"sess/gated/index.html?bt="+url.QueryEscape(ticket))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("pending widget served: %d", rec.Code)
	}
}
