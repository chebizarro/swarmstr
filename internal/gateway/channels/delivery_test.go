package channels

import (
	"context"
	"errors"
	"testing"
	"time"
)

type deliveryTestSender struct {
	id       string
	errs     []error
	calls    int
	messages []string
}

func (s *deliveryTestSender) ID() string { return s.id }

func (s *deliveryTestSender) Send(ctx context.Context, text string) error {
	s.calls++
	s.messages = append(s.messages, text)
	if s.calls <= len(s.errs) {
		return s.errs[s.calls-1]
	}
	return nil
}

type deliveryReceiptSender struct {
	deliveryTestSender
	messageID string
}

func (s *deliveryReceiptSender) SendWithReceipt(ctx context.Context, text string) (DeliveryReceipt, error) {
	if err := s.Send(ctx, text); err != nil {
		return DeliveryReceipt{}, err
	}
	return DeliveryReceipt{ChannelID: s.id, Provider: "test", MessageID: s.messageID}, nil
}

func testRetryPolicy(max int) RetryPolicy {
	return RetryPolicy{
		MaxAttempts:    max,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
		BackoffFactor:  2,
		Retryable:      func(error) bool { return true },
		Sleep:          func(context.Context, time.Duration) bool { return true },
	}
}

func TestDurableSender_RetriesTransientAndRecordsReceipt(t *testing.T) {
	sender := &deliveryTestSender{id: "ch", errs: []error{errors.New("timeout")}}
	d := NewDurableSender(sender, testRetryPolicy(3))
	receipt, err := d.Send(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if sender.calls != 2 {
		t.Fatalf("expected retry, got %d calls", sender.calls)
	}
	if receipt.Status != DeliveryDelivered || receipt.Attempts != 2 || receipt.ChannelID != "ch" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	history := d.Receipts()
	if len(history) != 1 || history[0].Status != DeliveryDelivered {
		t.Fatalf("expected delivered history, got %+v", history)
	}
}

func TestDurableSender_DeadLettersAfterMaxAttempts(t *testing.T) {
	sender := &deliveryTestSender{id: "ch", errs: []error{errors.New("503"), errors.New("503")}}
	d := NewDurableSender(sender, testRetryPolicy(2))
	receipt, err := d.Send(context.Background(), "lost")
	if err == nil {
		t.Fatal("expected final error")
	}
	if receipt.Status != DeliveryDeadLetter || receipt.Attempts != 2 || receipt.Error == "" {
		t.Fatalf("expected dead-letter receipt, got %+v", receipt)
	}
	if got := d.Receipts(); len(got) != 1 || got[0].Status != DeliveryDeadLetter {
		t.Fatalf("expected dead-letter history, got %+v", got)
	}
}

func TestDurableSender_UsesPlatformReceipt(t *testing.T) {
	sender := &deliveryReceiptSender{deliveryTestSender: deliveryTestSender{id: "ch"}, messageID: "msg-123"}
	d := NewDurableSender(sender, testRetryPolicy(1))
	receipt, err := d.Send(context.Background(), "with receipt")
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if receipt.Provider != "test" || receipt.MessageID != "msg-123" {
		t.Fatalf("platform receipt not captured: %+v", receipt)
	}
}

func TestDurableSender_NonRetryableStopsImmediately(t *testing.T) {
	sender := &deliveryTestSender{id: "ch", errs: []error{errors.New("bad request")}}
	p := testRetryPolicy(3)
	p.Retryable = func(error) bool { return false }
	d := NewDurableSender(sender, p)
	_, err := d.Send(context.Background(), "bad")
	if err == nil {
		t.Fatal("expected error")
	}
	if sender.calls != 1 {
		t.Fatalf("expected no retry, got %d calls", sender.calls)
	}
}
