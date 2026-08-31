package agent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type CredentialFailureClass string

const (
	CredentialFailureNone      CredentialFailureClass = "none"
	CredentialFailureRateLimit CredentialFailureClass = "rate_limit"
	CredentialFailureQuota     CredentialFailureClass = "quota"
)

type statusCodeError interface{ StatusCode() int }
type httpStatusCodeError interface{ HTTPStatusCode() int }
type retryAfterError interface{ RetryAfter() time.Duration }

type CredentialsCoolingError struct {
	RetryAt time.Time
	Cause   error
}

func (e *CredentialsCoolingError) Error() string {
	if e == nil {
		return "provider credentials unavailable"
	}
	if e.RetryAt.IsZero() {
		return "all provider credentials are cooling down"
	}
	return fmt.Sprintf("all provider credentials are cooling down until %s", e.RetryAt.UTC().Format(time.RFC3339))
}
func (e *CredentialsCoolingError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

var statusCodePattern = regexp.MustCompile(`(?i)(?:http|status(?:_code)?)[^0-9]{0,6}(401|403|429)\b`)

func ClassifyCredentialFailure(err error) CredentialFailureClass {
	if err == nil {
		return CredentialFailureNone
	}
	status := 0
	var sc statusCodeError
	if errors.As(err, &sc) {
		status = sc.StatusCode()
	}
	var hc httpStatusCodeError
	if status == 0 && errors.As(err, &hc) {
		status = hc.HTTPStatusCode()
	}
	if status == 401 || status == 403 {
		return CredentialFailureNone
	}
	if status == 429 {
		return CredentialFailureRateLimit
	}
	msg := strings.ToLower(err.Error())
	if match := statusCodePattern.FindStringSubmatch(msg); len(match) == 2 {
		parsed, _ := strconv.Atoi(match[1])
		if parsed == 401 || parsed == 403 {
			return CredentialFailureNone
		}
		if parsed == 429 {
			return CredentialFailureRateLimit
		}
	}
	for _, code := range []string{"insufficient_quota", "quota_exceeded", "billing_hard_limit_reached", "resource_exhausted"} {
		if strings.Contains(msg, code) {
			return CredentialFailureQuota
		}
	}
	for _, code := range []string{"rate_limit_exceeded", "rate limit exceeded", "too many requests"} {
		if strings.Contains(msg, code) {
			return CredentialFailureRateLimit
		}
	}
	return CredentialFailureNone
}

func credentialRetryAt(err error, now time.Time) time.Time {
	var retry retryAfterError
	if errors.As(err, &retry) {
		if delay := retry.RetryAfter(); delay > 0 && delay <= 24*time.Hour {
			return now.Add(delay)
		}
	}
	return now.Add(keyCooldown)
}

type CredentialProviderFactory func(apiKey string) (Provider, error)

// RotatingProvider acquires a credential for each provider request and retries
// the same model with another key only for classified rate/quota failures.
type RotatingProvider struct {
	Ring    *KeyRing
	Factory CredentialProviderFactory
}

func NewRotatingProvider(ring *KeyRing, factory CredentialProviderFactory) (*RotatingProvider, error) {
	if ring == nil || ring.Len() == 0 {
		return nil, fmt.Errorf("credential key ring is empty")
	}
	if factory == nil {
		return nil, fmt.Errorf("provider factory is required")
	}
	return &RotatingProvider{Ring: ring, Factory: factory}, nil
}

func (p *RotatingProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	attempted := map[string]struct{}{}
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return ProviderResult{}, err
		}
		lease, ok := p.Ring.Acquire(attempted)
		if !ok {
			retryAt, _ := p.Ring.EarliestRetry()
			return ProviderResult{}, &CredentialsCoolingError{RetryAt: retryAt, Cause: lastErr}
		}
		attempted[lease.Fingerprint] = struct{}{}
		provider, err := p.Factory(lease.Key)
		if err != nil {
			return ProviderResult{}, err
		}
		result, err := provider.Generate(ctx, turn)
		if err == nil {
			return result, nil
		}
		class := ClassifyCredentialFailure(err)
		if class == CredentialFailureNone {
			return ProviderResult{}, err
		}
		lastErr = err
		p.Ring.MarkRateLimited(lease, credentialRetryAt(err, time.Now()))
	}
}

func (p *RotatingProvider) Stream(ctx context.Context, turn Turn, onChunk func(string)) (ProviderResult, error) {
	attempted := map[string]struct{}{}
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return ProviderResult{}, err
		}
		lease, ok := p.Ring.Acquire(attempted)
		if !ok {
			retryAt, _ := p.Ring.EarliestRetry()
			return ProviderResult{}, &CredentialsCoolingError{RetryAt: retryAt, Cause: lastErr}
		}
		attempted[lease.Fingerprint] = struct{}{}
		provider, err := p.Factory(lease.Key)
		if err != nil {
			return ProviderResult{}, err
		}
		streaming, ok := provider.(StreamingProvider)
		if !ok {
			return provider.Generate(ctx, turn)
		}
		chunks := 0
		result, err := streaming.Stream(ctx, turn, func(text string) {
			chunks++
			if onChunk != nil {
				onChunk(text)
			}
		})
		if err == nil {
			return result, nil
		}
		if chunks > 0 || ClassifyCredentialFailure(err) == CredentialFailureNone {
			return ProviderResult{}, err
		}
		lastErr = err
		p.Ring.MarkRateLimited(lease, credentialRetryAt(err, time.Now()))
	}
}

var _ Provider = (*RotatingProvider)(nil)
var _ StreamingProvider = (*RotatingProvider)(nil)
