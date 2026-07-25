package board

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func putHTMLWidget(t *testing.T, s *Store, sessionKey, name string, tools []string) Snapshot {
	t.Helper()
	params := PutParams{
		SessionKey: sessionKey,
		Name:       name,
		Content:    PutContent{Kind: ContentKindHTML, HTML: "<p>" + name + "</p>"},
	}
	if len(tools) > 0 {
		params.Declared = &Declared{Tools: tools}
	}
	snap, err := s.PutWidget(params)
	if err != nil {
		t.Fatalf("put widget %s: %v", name, err)
	}
	return snap
}

func ticketFor(t *testing.T, s *Store, sessionKey, name string) string {
	t.Helper()
	snap := s.GetSnapshotWithTickets(sessionKey)
	for _, w := range snap.Widgets {
		if w.Name == name {
			if w.ViewTicket == "" {
				t.Fatalf("no view ticket minted for %s: %+v", name, w)
			}
			return w.ViewTicket
		}
	}
	t.Fatalf("widget %s not found", name)
	return ""
}

func TestViewTicketRoundTrip(t *testing.T) {
	s := NewStore()
	putHTMLWidget(t, s, "sess", "chart", nil)

	snap := s.GetSnapshotWithTickets("sess")
	w := snap.Widgets[0]
	if w.ViewTicket == "" || w.ViewGeneration == "" || w.ViewTicketTTLMs != int(ViewTicketTTL/time.Millisecond) {
		t.Fatalf("ticket fields not minted: %+v", w)
	}
	if !strings.HasPrefix(w.FrameURL, HTTPPathPrefix+"sess/chart/index.html?bt=") {
		t.Fatalf("unexpected frame url: %s", w.FrameURL)
	}
	if !strings.HasPrefix(w.ViewTicket, "v1.") {
		t.Fatalf("unexpected ticket format: %s", w.ViewTicket)
	}

	view, err := s.ResolveViewTicket(w.ViewTicket)
	if err != nil {
		t.Fatalf("resolve ticket: %v", err)
	}
	if view.SessionKey != "sess" || view.Name != "chart" || view.HTML != "<p>chart</p>" {
		t.Fatalf("unexpected authorized view: %+v", view)
	}
	if view.GrantState != GrantNone || view.HasGrantedTool("prompt") {
		t.Fatalf("ungated widget must have no granted tools: %+v", view)
	}

	// Plain GetSnapshot never carries tickets, and stored state stays clean.
	if plain := s.GetSnapshot("sess"); plain.Widgets[0].ViewTicket != "" || plain.Widgets[0].FrameURL != "" {
		t.Fatalf("plain snapshot leaked ticket fields: %+v", plain.Widgets[0])
	}
}

func TestViewTicketGrantLifecycle(t *testing.T) {
	s := NewStore()
	snap := putHTMLWidget(t, s, "sess", "chart", []string{"prompt", "health"})
	w := snap.Widgets[0]
	if w.GrantState != GrantPending {
		t.Fatalf("expected pending grant: %+v", w)
	}

	// Pending widgets never receive tickets.
	if got := s.GetSnapshotWithTickets("sess").Widgets[0]; got.ViewTicket != "" {
		t.Fatalf("pending widget received ticket: %+v", got)
	}

	if _, err := s.Grant("sess", "chart", GrantGranted, w.Revision, w.InstanceID); err != nil {
		t.Fatalf("grant: %v", err)
	}
	ticket := ticketFor(t, s, "sess", "chart")
	view, err := s.ResolveViewTicket(ticket)
	if err != nil {
		t.Fatalf("resolve granted ticket: %v", err)
	}
	if !view.HasGrantedTool("prompt") || !view.HasGrantedTool("health") || view.HasGrantedTool("cron.list") {
		t.Fatalf("unexpected granted tools: %+v", view.Declared)
	}

	// A re-put with different bytes revokes the grant and stales the ticket.
	if _, err := s.PutWidget(PutParams{
		SessionKey: "sess",
		Name:       "chart",
		Content:    PutContent{Kind: ContentKindHTML, HTML: "<p>other</p>"},
		Declared:   &Declared{Tools: []string{"prompt"}},
	}); err != nil {
		t.Fatalf("re-put: %v", err)
	}
	if _, err := s.ResolveViewTicket(ticket); err == nil {
		t.Fatal("expected stale ticket after re-put")
	}
	if got := s.GetSnapshotWithTickets("sess").Widgets[0]; got.ViewTicket != "" {
		t.Fatalf("re-put pending widget received ticket: %+v", got)
	}
}

func TestViewTicketRejectedGrantNotServed(t *testing.T) {
	s := NewStore()
	snap := putHTMLWidget(t, s, "sess", "chart", []string{"prompt"})
	w := snap.Widgets[0]
	if _, err := s.Grant("sess", "chart", GrantRejected, w.Revision, w.InstanceID); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if got := s.GetSnapshotWithTickets("sess").Widgets[0]; got.ViewTicket != "" {
		t.Fatalf("rejected widget received ticket: %+v", got)
	}
}

func TestViewTicketExpiryAndTamper(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.now = func() time.Time { return now }
	putHTMLWidget(t, s, "sess", "chart", nil)
	ticket := ticketFor(t, s, "sess", "chart")

	// Within TTL the ticket resolves.
	now = now.Add(ViewTicketTTL - time.Second)
	if _, err := s.ResolveViewTicket(ticket); err != nil {
		t.Fatalf("resolve within ttl: %v", err)
	}
	// At/after TTL it expires.
	now = now.Add(2 * time.Second)
	if _, err := s.ResolveViewTicket(ticket); err == nil {
		t.Fatal("expected expired ticket")
	}

	now = time.Now()
	ticket = ticketFor(t, s, "sess", "chart")
	// Tampered signature fails.
	tampered := ticket[:len(ticket)-2] + "zz"
	if tampered == ticket {
		tampered = ticket[:len(ticket)-2] + "aa"
	}
	if _, err := s.ResolveViewTicket(tampered); err == nil {
		t.Fatal("expected tampered ticket rejection")
	}
	// Tickets from another store (different secret) fail.
	other := NewStore()
	putHTMLWidget(t, other, "sess", "chart", nil)
	if _, err := s.ResolveViewTicket(ticketFor(t, other, "sess", "chart")); err == nil {
		t.Fatal("expected foreign ticket rejection")
	}
	// Garbage inputs fail.
	for _, bogus := range []string{"", "v1.", "v1..", "v2.a.b", strings.Repeat("x", MaxTicketLength+1)} {
		if _, err := s.ResolveViewTicket(bogus); err == nil {
			t.Fatalf("expected rejection for %q", bogus)
		}
	}
}

func TestViewTicketBoardDeletionStalesTicket(t *testing.T) {
	s := NewStore()
	putHTMLWidget(t, s, "sess", "chart", nil)
	ticket := ticketFor(t, s, "sess", "chart")
	if _, err := s.ApplyOps("sess", []Op{{Kind: "widget_remove", Name: "chart"}}); err != nil {
		t.Fatalf("remove widget: %v", err)
	}
	if _, err := s.ResolveViewTicket(ticket); err == nil {
		t.Fatal("expected stale ticket after widget removal")
	}
}

func TestViewTicketConcurrentLifecycle(t *testing.T) {
	s := NewStore()
	putHTMLWidget(t, s, "sess", "chart", nil)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := s.GetSnapshotWithTickets("sess")
				for _, w := range snap.Widgets {
					if w.ViewTicket != "" {
						// Resolution may fail when a re-put races it, but must
						// never panic or corrupt state.
						_, _ = s.ResolveViewTicket(w.ViewTicket)
					}
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		putHTMLWidget(t, s, "sess", "chart", nil)
	}
	close(stop)
	wg.Wait()

	if _, err := s.ResolveViewTicket(ticketFor(t, s, "sess", "chart")); err != nil {
		t.Fatalf("final resolve: %v", err)
	}
}
