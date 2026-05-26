package channels

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"
)

// DeliveryStatus records the terminal or in-flight state of an outbound send.
type DeliveryStatus string

const (
	DeliveryPending    DeliveryStatus = "pending"
	DeliveryDelivered  DeliveryStatus = "delivered"
	DeliveryFailed     DeliveryStatus = "failed"
	DeliveryDeadLetter DeliveryStatus = "dead_letter"
)

// DeliveryReceipt is the durable send pipeline's platform-neutral receipt.
// MessageID is platform-native when the adapter can return it; otherwise it may
// be empty while Status still records whether the send was accepted.
type DeliveryReceipt struct {
	ChannelID   string         `json:"channel_id"`
	Provider    string         `json:"provider,omitempty"`
	MessageID   string         `json:"message_id,omitempty"`
	Status      DeliveryStatus `json:"status"`
	Attempts    int            `json:"attempts"`
	TextPreview string         `json:"text_preview,omitempty"`
	Error       string         `json:"error,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	DeliveredAt time.Time      `json:"delivered_at,omitempty"`
}

// SendReceiptHandle is optionally implemented by channel handles that can
// return platform message IDs for outbound sends.
type SendReceiptHandle interface {
	ID() string
	SendWithReceipt(ctx context.Context, text string) (DeliveryReceipt, error)
}

// BasicSender is the minimum shape required by DurableSender.
type BasicSender interface {
	ID() string
	Send(ctx context.Context, text string) error
}

// RetryPolicy controls durable delivery retry behavior.
type RetryPolicy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	BackoffFactor  float64
	Retryable      func(error) bool
	Sleep          func(context.Context, time.Duration) bool
}

// DefaultRetryPolicy retries transient sends three times with exponential backoff.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:    3,
		InitialBackoff: 250 * time.Millisecond,
		MaxBackoff:     5 * time.Second,
		BackoffFactor:  2,
		Retryable:      defaultRetryableDeliveryError,
		Sleep:          sleepDeliveryBackoff,
	}
}

// DurableSender wraps an existing channel sender with receipt tracking, retry,
// and dead-letter logging. It is concurrency-safe.
type DurableSender struct {
	id       string
	sender   BasicSender
	receipts SendReceiptHandle
	policy   RetryPolicy

	mu      sync.Mutex
	history []DeliveryReceipt
}

// NewDurableSender creates a durable delivery wrapper around sender.
func NewDurableSender(sender BasicSender, policy RetryPolicy) *DurableSender {
	if policy.MaxAttempts == 0 && policy.InitialBackoff == 0 && policy.MaxBackoff == 0 && policy.BackoffFactor == 0 && policy.Retryable == nil && policy.Sleep == nil {
		policy = DefaultRetryPolicy()
	} else {
		policy = normalizeRetryPolicy(policy)
	}
	var receiptSender SendReceiptHandle
	if h, ok := sender.(SendReceiptHandle); ok {
		receiptSender = h
	}
	id := ""
	if sender != nil {
		id = sender.ID()
	}
	return &DurableSender{id: id, sender: sender, receipts: receiptSender, policy: policy}
}

// Send delivers text and returns a receipt. On final failure a dead-letter
// receipt is recorded and returned with the error.
func (d *DurableSender) Send(ctx context.Context, text string) (DeliveryReceipt, error) {
	if d == nil || d.sender == nil {
		return DeliveryReceipt{Status: DeliveryFailed, Error: "nil sender", CreatedAt: time.Now()}, fmt.Errorf("durable send: nil sender")
	}
	policy := normalizeRetryPolicy(d.policy)
	receipt := DeliveryReceipt{
		ChannelID:   d.id,
		Status:      DeliveryPending,
		CreatedAt:   time.Now(),
		TextPreview: previewText(text, 160),
	}
	var lastErr error
	backoff := policy.InitialBackoff
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		receipt.Attempts = attempt
		var got DeliveryReceipt
		var err error
		if d.receipts != nil {
			got, err = d.receipts.SendWithReceipt(ctx, text)
		} else {
			err = d.sender.Send(ctx, text)
		}
		if err == nil {
			if got.ChannelID != "" {
				receipt.ChannelID = got.ChannelID
			}
			if got.Provider != "" {
				receipt.Provider = got.Provider
			}
			if got.MessageID != "" {
				receipt.MessageID = got.MessageID
			}
			receipt.Status = DeliveryDelivered
			receipt.Error = ""
			receipt.DeliveredAt = time.Now()
			d.record(receipt)
			return receipt, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			lastErr = ctx.Err()
			break
		}
		if attempt >= policy.MaxAttempts || !policy.Retryable(err) {
			break
		}
		if !policy.Sleep(ctx, backoff) {
			lastErr = ctx.Err()
			if lastErr == nil {
				lastErr = err
			}
			break
		}
		backoff = nextDeliveryBackoff(backoff, policy)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("delivery failed")
	}
	receipt.Status = DeliveryDeadLetter
	receipt.Error = lastErr.Error()
	d.record(receipt)
	log.Printf("durable delivery dead-letter channel=%s attempts=%d err=%v", receipt.ChannelID, receipt.Attempts, lastErr)
	return receipt, lastErr
}

// Receipts returns a snapshot of recorded receipts.
func (d *DurableSender) Receipts() []DeliveryReceipt {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]DeliveryReceipt, len(d.history))
	copy(out, d.history)
	return out
}

func (d *DurableSender) record(r DeliveryReceipt) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.history = append(d.history, r)
}

func normalizeRetryPolicy(p RetryPolicy) RetryPolicy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 3
	}
	if p.InitialBackoff <= 0 {
		p.InitialBackoff = 250 * time.Millisecond
	}
	if p.MaxBackoff <= 0 {
		p.MaxBackoff = 5 * time.Second
	}
	if p.BackoffFactor <= 1 {
		p.BackoffFactor = 2
	}
	if p.Retryable == nil {
		p.Retryable = defaultRetryableDeliveryError
	}
	if p.Sleep == nil {
		p.Sleep = sleepDeliveryBackoff
	}
	return p
}

func nextDeliveryBackoff(current time.Duration, policy RetryPolicy) time.Duration {
	if current <= 0 {
		return policy.InitialBackoff
	}
	next := time.Duration(math.Round(float64(current) * policy.BackoffFactor))
	if next > policy.MaxBackoff {
		return policy.MaxBackoff
	}
	return next
}

func sleepDeliveryBackoff(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func defaultRetryableDeliveryError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, token := range []string{"timeout", "temporar", "429", "rate limit", "too many requests", "connection reset", "connection refused", "eof", "503", "502", "504"} {
		if strings.Contains(s, token) {
			return true
		}
	}
	return false
}

func previewText(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}
