package l402

import (
	"context"
	"crypto/sha256"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"metiq/internal/browser"
	"metiq/internal/lightning"
	"metiq/internal/paymentstate"
)

type fakeInvoicePayer struct {
	calls   atomic.Int32
	started chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (p *fakeInvoicePayer) ID() string { return "fake" }
func (p *fakeInvoicePayer) PayInvoice(ctx context.Context, req lightning.PaymentRequest) (lightning.PaymentResult, error) {
	p.calls.Add(1)
	if p.started != nil {
		p.once.Do(func() { close(p.started) })
	}
	if p.release != nil {
		select {
		case <-p.release:
		case <-ctx.Done():
			return lightning.PaymentResult{}, ctx.Err()
		}
	}
	preimage := make([]byte, 32)
	for i := range preimage {
		preimage[i] = 0x42
	}
	return lightning.PaymentResult{
		Status: lightning.PaymentStatusSucceeded, PaymentHash: req.PaymentHash,
		Preimage: preimage, AmountMSat: req.AmountMSat, FeeMSat: 1,
	}, nil
}
func (p *fakeInvoicePayer) LookupPayment(context.Context, lightning.PaymentLookup) (lightning.PaymentResult, error) {
	return lightning.PaymentResult{Status: lightning.PaymentStatusNotFound}, nil
}
func (p *fakeInvoicePayer) Close() error { return nil }

func testCoordinator(t *testing.T, payer *fakeInvoicePayer) *lightning.Coordinator {
	t.Helper()
	preimage := make([]byte, 32)
	for i := range preimage {
		preimage[i] = 0x42
	}
	hash := sha256.Sum256(preimage)
	coordinator, err := lightning.NewCoordinator(context.Background(), lightning.CoordinatorConfig{
		Policy: lightning.CoordinatorPolicy{
			Network: "regtest", PayerID: payer.ID(), MaxInvoiceMSat: 1000,
			MaxFeeMSat: 10, MaxSpendMSatPerHour: 10000, PaymentTimeout: time.Second,
		},
		Payers: map[string]lightning.InvoicePayer{payer.ID(): payer},
		Decoder: lightning.InvoiceDecoderFunc(func(invoice, network string) (lightning.DecodedInvoice, error) {
			if invoice != "lnbcrt1test" || network != "regtest" {
				return lightning.DecodedInvoice{}, lightning.ErrInvoiceInvalid
			}
			return lightning.DecodedInvoice{
				PaymentHash: hash, AmountMSat: 100, CreatedAt: time.Now(),
				ExpiresAt: time.Now().Add(time.Hour), Network: network,
			}, nil
		}),
		Attempts: paymentstate.NewMemoryPaymentAttemptRepository(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

func newTestClient(t *testing.T, server *httptest.Server, payer *fakeInvoicePayer, origins ...string) *Client {
	t.Helper()
	cache, err := NewCache(context.Background(), paymentstate.NewMemoryL402TokenRepository(), CacheOptions{})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := testCoordinator(t, payer)
	client, err := NewClient(ClientOptions{
		Browser: &browser.Client{HTTPClient: server.Client()}, Cache: cache,
		Coordinator: coordinator, PayerID: payer.ID(), AllowedOrigins: origins,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestNewClientPreservesDialTimeSSRFGuards(t *testing.T) {
	blocked := errors.New("blocked dial address")
	lookupCalled := false
	base := &browser.Client{
		ValidateIP: func(ip net.IP) error {
			if ip.IsLoopback() {
				return blocked
			}
			return nil
		},
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			lookupCalled = true
			return []net.IP{net.ParseIP("203.0.113.10")}, nil
		},
	}
	cache, err := NewCache(context.Background(), paymentstate.NewMemoryL402TokenRepository(), CacheOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(ClientOptions{
		Browser: base, Cache: cache, Coordinator: testCoordinator(t, &fakeInvoicePayer{}),
		PayerID: "fake", AllowedOrigins: []string{"https://allowed.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.browser.ValidateIP == nil || !errors.Is(client.browser.ValidateIP(net.ParseIP("127.0.0.1")), blocked) {
		t.Fatal("dial-time IP validator was not preserved")
	}
	if client.browser.LookupIP == nil {
		t.Fatal("pinned DNS resolver was not preserved")
	}
	if _, err := client.browser.LookupIP(context.Background(), "allowed.example"); err != nil || !lookupCalled {
		t.Fatalf("lookup seam was not preserved: %v", err)
	}
}

func writeChallenge(w http.ResponseWriter) {
	w.Header().Add("WWW-Authenticate", "Bearer realm=\"public\"")
	w.Header().Add("WWW-Authenticate", "L402 macaroon=\"opaque,macaroon\", invoice=\"lnbcrt1test\"")
	w.WriteHeader(http.StatusPaymentRequired)
	_, _ = w.Write([]byte("payment required"))
}

func expectedAuthorization() string {
	return "L402 opaque,macaroon:" + strings.Repeat("42", 32)
}

func TestClientPaysRetriesAndUsesCachedAuthorization(t *testing.T) {
	var mu sync.Mutex
	var authorizations []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		authorizations = append(authorizations, auth)
		mu.Unlock()
		if auth != expectedAuthorization() {
			writeChallenge(w)
			return
		}
		_, _ = w.Write([]byte("paid content"))
	}))
	defer srv.Close()
	payer := &fakeInvoicePayer{}
	client := newTestClient(t, srv, payer, srv.URL)

	for i := 0; i < 2; i++ {
		response, err := client.Fetch(context.Background(), browser.Request{URL: srv.URL + "/resource"})
		if err != nil {
			t.Fatal(err)
		}
		if response.Body != "paid content" {
			t.Fatalf("response body = %q", response.Body)
		}
	}
	if got := payer.calls.Load(); got != 1 {
		t.Fatalf("payer calls = %d, want 1", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(authorizations) != 3 || authorizations[0] != "" || authorizations[1] != expectedAuthorization() || authorizations[2] != expectedAuthorization() {
		t.Fatalf("actual Authorization headers = %#v", authorizations)
	}
}

func TestClientRejectedCachedTokenEvictedWithoutRepayment(t *testing.T) {
	var reject atomic.Bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reject.Load() || r.Header.Get("Authorization") != expectedAuthorization() {
			writeChallenge(w)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	payer := &fakeInvoicePayer{}
	client := newTestClient(t, srv, payer, srv.URL)
	if _, err := client.Fetch(context.Background(), browser.Request{URL: srv.URL + "/resource"}); err != nil {
		t.Fatal(err)
	}
	reject.Store(true)
	if _, err := client.Fetch(context.Background(), browser.Request{URL: srv.URL + "/resource"}); !errors.Is(err, ErrAuthorizationRejected) {
		t.Fatalf("cached rejection error = %v", err)
	}
	if got := payer.calls.Load(); got != 1 {
		t.Fatalf("rejected cached token repaid invoice: calls=%d", got)
	}
}

func TestClientAtMostOnePaymentAndAuthenticatedRetry(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		writeChallenge(w)
	}))
	defer srv.Close()
	payer := &fakeInvoicePayer{}
	client := newTestClient(t, srv, payer, srv.URL)
	if _, err := client.Fetch(context.Background(), browser.Request{URL: srv.URL + "/resource"}); !errors.Is(err, ErrAuthorizationRejected) {
		t.Fatalf("error = %v", err)
	}
	if got := payer.calls.Load(); got != 1 {
		t.Fatalf("payer calls = %d", got)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("HTTP requests = %d, want initial + one retry", got)
	}
}

func TestClientConcurrentIdenticalChallengeSingleFlight(t *testing.T) {
	const callers = 8
	var unauthenticated atomic.Int32
	allChallenged := make(chan struct{})
	var allOnce sync.Once
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == expectedAuthorization() {
			_, _ = w.Write([]byte("ok"))
			return
		}
		if unauthenticated.Add(1) == callers {
			allOnce.Do(func() { close(allChallenged) })
		}
		writeChallenge(w)
	}))
	defer srv.Close()
	payer := &fakeInvoicePayer{started: make(chan struct{}), release: allChallenged}
	client := newTestClient(t, srv, payer, srv.URL)

	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() {
			response, err := client.Fetch(context.Background(), browser.Request{URL: srv.URL + "/same"})
			if err == nil && response.Body != "ok" {
				err = errors.New("unexpected response")
			}
			errs <- err
		}()
	}
	for i := 0; i < callers; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got := payer.calls.Load(); got != 1 {
		t.Fatalf("payer calls = %d, want 1", got)
	}
}

func TestClientStripsPaidAuthorizationAcrossOriginRedirect(t *testing.T) {
	var leaked string
	leak := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("done"))
	}))
	defer leak.Close()
	var target *httptest.Server
	target = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			writeChallenge(w)
			return
		}
		http.Redirect(w, r, leak.URL+"/sink", http.StatusFound)
	}))
	defer target.Close()
	start := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/paid", http.StatusFound)
	}))
	defer start.Close()

	payer := &fakeInvoicePayer{}
	client := newTestClient(t, start, payer, start.URL, target.URL, leak.URL)
	response, err := client.Fetch(context.Background(), browser.Request{URL: start.URL + "/start"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Body != "done" {
		t.Fatalf("response = %#v", response)
	}
	if leaked != "" {
		t.Fatalf("paid credential leaked cross-origin: %q", leaked)
	}
	if got := payer.calls.Load(); got != 1 {
		t.Fatalf("payer calls = %d", got)
	}
}

func TestClientNeverReusesChallengeCredentialAcrossOrigins(t *testing.T) {
	var secondAuthorization string
	second := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondAuthorization = r.Header.Get("Authorization")
		writeChallenge(w)
	}))
	defer second.Close()
	first := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != expectedAuthorization() {
			writeChallenge(w)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer first.Close()

	payer := &fakeInvoicePayer{}
	client := newTestClient(t, first, payer, first.URL, second.URL)
	if _, err := client.Fetch(context.Background(), browser.Request{URL: first.URL + "/resource"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Fetch(context.Background(), browser.Request{URL: second.URL + "/copied"}); !errors.Is(err, ErrCredentialOrigin) {
		t.Fatalf("cross-origin copied challenge error = %v", err)
	}
	if secondAuthorization != "" {
		t.Fatalf("credential reused at second origin: %q", secondAuthorization)
	}
	if got := payer.calls.Load(); got != 1 {
		t.Fatalf("copied cross-origin challenge invoked payer: %d", got)
	}
}

func TestClientRejectsUnallowedOriginBeforeRequest(t *testing.T) {
	var requested atomic.Bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requested.Store(true) }))
	defer srv.Close()
	payer := &fakeInvoicePayer{}
	client := newTestClient(t, srv, payer, "https://allowed.example")
	if _, err := client.Fetch(context.Background(), browser.Request{URL: srv.URL}); !errors.Is(err, ErrOriginNotAllowed) {
		t.Fatalf("error = %v", err)
	}
	if requested.Load() || payer.calls.Load() != 0 {
		t.Fatal("unallowed origin caused network or payment activity")
	}
}
