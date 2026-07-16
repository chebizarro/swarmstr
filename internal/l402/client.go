package l402

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"metiq/internal/browser"
	"metiq/internal/lightning"
	"metiq/internal/paymentstate"
)

var (
	ErrOriginNotAllowed      = errors.New("L402 origin is not allowed")
	ErrAuthorizationRejected = errors.New("L402 authorization was rejected")
	ErrPaymentNotSettled     = errors.New("L402 payment did not settle")
	ErrCredentialOrigin      = errors.New("L402 challenge is already bound to another origin")
)

type ClientOptions struct {
	Browser        *browser.Client
	Cache          TokenCache
	Coordinator    lightning.PaymentCoordinator
	PayerID        string
	AllowedOrigins []string
	Warn           func(error)
}

type paymentFlight struct {
	done   chan struct{}
	record paymentstate.L402TokenRecord
	err    error
}

// Client owns the bounded L402 fetch/payment/retry state machine.
type Client struct {
	browser     *browser.Client
	cache       TokenCache
	coordinator lightning.PaymentCoordinator
	payerID     string
	origins     map[string]struct{}
	warn        func(error)

	challengeMu      sync.Mutex
	challengeOrigins map[string]string
	flightsMu        sync.Mutex
	flights          map[string]*paymentFlight
}

func NewClient(opts ClientOptions) (*Client, error) {
	if opts.Cache == nil {
		return nil, fmt.Errorf("L402 cache is required")
	}
	if opts.Coordinator == nil {
		return nil, fmt.Errorf("Lightning payment coordinator is required")
	}
	if strings.TrimSpace(opts.PayerID) == "" {
		return nil, fmt.Errorf("L402 payer id is required")
	}
	origins := make(map[string]struct{}, len(opts.AllowedOrigins))
	for _, raw := range opts.AllowedOrigins {
		origin, err := canonicalAllowedOrigin(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid allowed L402 origin")
		}
		origins[origin] = struct{}{}
	}
	if len(origins) == 0 {
		return nil, fmt.Errorf("at least one allowed L402 origin is required")
	}
	base := opts.Browser
	if base == nil {
		base = &browser.Client{}
	}
	transport := &browser.Client{
		HTTPClient: base.HTTPClient,
		ValidateIP: base.ValidateIP,
		LookupIP:   base.LookupIP,
	}
	client := &Client{
		browser: transport, cache: opts.Cache, coordinator: opts.Coordinator,
		payerID: strings.TrimSpace(opts.PayerID), origins: origins, warn: opts.Warn,
		challengeOrigins: map[string]string{}, flights: map[string]*paymentFlight{},
	}
	transport.ValidateURL = func(rawURL string) error {
		if base.ValidateURL != nil {
			if err := base.ValidateURL(rawURL); err != nil {
				return err
			}
		}
		return client.validateOrigin(rawURL)
	}
	return client, nil
}

// Fetch makes one unauthenticated-or-cached request, at most one payment, and
// exactly one authenticated retry after a new payment.
func (c *Client) Fetch(ctx context.Context, req browser.Request) (browser.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	if err := c.validateOrigin(req.URL); err != nil {
		return browser.Response{}, err
	}

	initial := req
	initial.Method = method
	initial.Headers = cloneStringMap(req.Headers)
	deleteHeader(initial.Headers, "Authorization")

	cached, cachedOK, cacheErr := c.cache.Get(ctx, method, req.URL)
	if cacheErr != nil {
		c.warning(fmt.Errorf("update L402 cache usage: %w", cacheErr))
	}
	var cachedAuth string
	if cachedOK {
		requestOrigin, err := CanonicalOrigin(req.URL)
		if err != nil || cached.Origin != requestOrigin {
			c.warning(c.cache.Delete(ctx, method, req.URL))
			cachedOK = false
		} else {
			cachedAuth, err = (Challenge{Scheme: cached.Scheme, Macaroon: cached.Macaroon}).Authorization(cached.PreimageHex)
			if err != nil {
				c.warning(c.cache.Delete(ctx, method, req.URL))
				cachedOK = false
			} else {
				initial.Headers["Authorization"] = cachedAuth
			}
		}
	}

	response, err := c.browser.Fetch(ctx, initial)
	if err != nil {
		return browser.Response{}, err
	}
	if response.StatusCode != http.StatusPaymentRequired {
		return response, nil
	}

	challenge, err := ParseChallenge(response.Headers.Values("WWW-Authenticate"))
	if err != nil {
		return browser.Response{}, fmt.Errorf("L402 server returned an unusable authentication challenge: %w", err)
	}
	finalURL := response.URL
	finalOrigin, err := CanonicalOrigin(finalURL)
	if err != nil {
		return browser.Response{}, err
	}
	if _, ok := c.origins[finalOrigin]; !ok {
		return browser.Response{}, ErrOriginNotAllowed
	}
	if err := c.claimChallengeOrigin(challenge, finalOrigin); err != nil {
		return browser.Response{}, err
	}

	if cachedOK && cached.Origin == finalOrigin {
		c.warning(c.cache.Delete(ctx, method, req.URL))
		if cached.Scheme == challenge.Scheme && cached.MacaroonSHA256 == challenge.MacaroonFingerprint() {
			return browser.Response{}, ErrAuthorizationRejected
		}
	}

	record, err := c.payOnce(ctx, method, finalURL, challenge)
	if err != nil {
		return browser.Response{}, err
	}
	authorization, err := (Challenge{Scheme: record.Scheme, Macaroon: record.Macaroon}).Authorization(record.PreimageHex)
	if err != nil {
		c.warning(c.cache.Delete(ctx, method, finalURL))
		return browser.Response{}, err
	}

	retry := req
	retry.Method = method
	retry.URL = finalURL
	retry.Query = nil
	retry.Headers = cloneStringMap(req.Headers)
	deleteHeader(retry.Headers, "Authorization")
	retry.Headers["Authorization"] = authorization
	response, err = c.browser.Fetch(ctx, retry)
	if err != nil {
		return browser.Response{}, err
	}
	if response.StatusCode == http.StatusPaymentRequired {
		c.warning(c.cache.Delete(ctx, method, finalURL))
		return browser.Response{}, ErrAuthorizationRejected
	}
	return response, nil
}

func (c *Client) payOnce(ctx context.Context, method, rawURL string, challenge Challenge) (paymentstate.L402TokenRecord, error) {
	origin, err := CanonicalOrigin(rawURL)
	if err != nil {
		return paymentstate.L402TokenRecord{}, err
	}
	key := origin + "\x00" + challenge.Fingerprint()
	c.flightsMu.Lock()
	if existing := c.flights[key]; existing != nil {
		c.flightsMu.Unlock()
		select {
		case <-existing.done:
			if existing.err != nil {
				return existing.record, existing.err
			}
			record, persistErr := c.cache.Put(ctx, method, rawURL, Token{
				Challenge: challenge, PreimageHex: existing.record.PreimageHex,
				PaymentHashHex: existing.record.PaymentHashHex, PayerID: existing.record.PayerID,
				CreatedAt: existing.record.CreatedAt, ExpiresAt: existing.record.ExpiresAt,
			})
			if persistErr != nil {
				c.warning(fmt.Errorf("persist shared L402 token: %w", persistErr))
			}
			return record, nil
		case <-ctx.Done():
			return paymentstate.L402TokenRecord{}, ctx.Err()
		}
	}
	flight := &paymentFlight{done: make(chan struct{})}
	c.flights[key] = flight
	c.flightsMu.Unlock()

	result, err := c.coordinator.PayInvoice(ctx, challenge.Invoice)
	if err == nil && result.Status != lightning.PaymentStatusSucceeded {
		err = fmt.Errorf("%w: status %s", ErrPaymentNotSettled, result.Status)
	}
	if err == nil && len(result.Preimage) != 32 {
		err = fmt.Errorf("%w: invalid payment proof", ErrPaymentNotSettled)
	}
	if err == nil {
		flight.record, err = c.cache.Put(ctx, method, rawURL, Token{
			Challenge: challenge, PreimageHex: hex.EncodeToString(result.Preimage),
			PaymentHashHex: hex.EncodeToString(result.PaymentHash[:]), PayerID: c.payerID,
			CreatedAt: time.Now(),
		})
		if err != nil {
			c.warning(fmt.Errorf("persist paid L402 token: %w", err))
			err = nil
		}
	}
	flight.err = err
	close(flight.done)
	c.flightsMu.Lock()
	delete(c.flights, key)
	c.flightsMu.Unlock()
	return flight.record, flight.err
}

type challengeOriginLookup interface {
	ChallengeOrigin(Challenge) (string, bool)
}

func (c *Client) claimChallengeOrigin(challenge Challenge, origin string) error {
	if lookup, ok := c.cache.(challengeOriginLookup); ok {
		if cachedOrigin, found := lookup.ChallengeOrigin(challenge); found && cachedOrigin != origin {
			return ErrCredentialOrigin
		}
	}
	key := challenge.Fingerprint()
	c.challengeMu.Lock()
	defer c.challengeMu.Unlock()
	if claimed := c.challengeOrigins[key]; claimed != "" && claimed != origin {
		return ErrCredentialOrigin
	}
	c.challengeOrigins[key] = origin
	return nil
}

func (c *Client) validateOrigin(rawURL string) error {
	origin, err := CanonicalOrigin(rawURL)
	if err != nil {
		return err
	}
	if _, ok := c.origins[origin]; !ok {
		return ErrOriginNotAllowed
	}
	return nil
}

func (c *Client) warning(err error) {
	if err != nil && c.warn != nil {
		c.warn(err)
	}
}

func cloneStringMap(input map[string]string) map[string]string {
	output := make(map[string]string, len(input)+1)
	for key, value := range input {
		output[key] = value
	}
	return output
}

func deleteHeader(headers map[string]string, name string) {
	for key := range headers {
		if strings.EqualFold(key, name) {
			delete(headers, key)
		}
	}
}
