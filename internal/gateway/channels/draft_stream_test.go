package channels

import (
	"context"
	"testing"
	"time"
)

type draftTestHandle struct {
	id       string
	sent     []string
	threads  []string
	edits    []string
	deletes  []string
	nextID   string
	lastText string
}

func (h *draftTestHandle) ID() string { return h.id }
func (h *draftTestHandle) Send(ctx context.Context, text string) error {
	h.sent = append(h.sent, text)
	h.lastText = text
	return nil
}
func (h *draftTestHandle) SendWithReceipt(ctx context.Context, text string) (DeliveryReceipt, error) {
	h.sent = append(h.sent, text)
	h.lastText = text
	id := h.nextID
	if id == "" {
		id = "draft-1"
	}
	return DeliveryReceipt{ChannelID: h.id, MessageID: id, Status: DeliveryDelivered, CreatedAt: time.Now(), DeliveredAt: time.Now()}, nil
}
func (h *draftTestHandle) SendInThreadWithReceipt(ctx context.Context, threadID, text string) (DeliveryReceipt, error) {
	h.threads = append(h.threads, threadID+":"+text)
	h.lastText = text
	id := h.nextID
	if id == "" {
		id = "draft-thread-1"
	}
	return DeliveryReceipt{ChannelID: h.id, MessageID: id, Status: DeliveryDelivered, CreatedAt: time.Now(), DeliveredAt: time.Now()}, nil
}
func (h *draftTestHandle) EditMessage(ctx context.Context, eventID, newText string) error {
	h.edits = append(h.edits, eventID+":"+newText)
	h.lastText = newText
	return nil
}
func (h *draftTestHandle) DeleteMessage(ctx context.Context, eventID string) error {
	h.deletes = append(h.deletes, eventID)
	return nil
}

type draftSendOnlyHandle struct {
	id   string
	sent []string
}

func (h *draftSendOnlyHandle) ID() string { return h.id }
func (h *draftSendOnlyHandle) Send(ctx context.Context, text string) error {
	h.sent = append(h.sent, text)
	return nil
}

func TestDraftStreamController_EditFinalizeDelete(t *testing.T) {
	h := &draftTestHandle{id: "ch", nextID: "msg-1"}
	c := NewDraftStreamController(h, DraftStreamOptions{MinEditInterval: time.Hour})
	if err := c.Append(context.Background(), "hello"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if len(h.sent) != 1 || h.sent[0] != "hello" {
		t.Fatalf("expected draft send, got %+v", h.sent)
	}
	if err := c.Update(context.Background(), "throttled edit"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(h.edits) != 0 {
		t.Fatalf("expected throttle to suppress edit, got %+v", h.edits)
	}
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(h.edits) != 1 || h.edits[0] != "msg-1:throttled edit" {
		t.Fatalf("expected forced edit, got %+v", h.edits)
	}
	receipt, err := c.Finalize(context.Background(), "final")
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if receipt.MessageID != "msg-1" {
		t.Fatalf("expected draft receipt, got %+v", receipt)
	}
	if h.edits[len(h.edits)-1] != "msg-1:final" {
		t.Fatalf("expected final edit, got %+v", h.edits)
	}
	if err := c.DeleteDraft(context.Background()); err != nil {
		t.Fatalf("DeleteDraft: %v", err)
	}
	if len(h.deletes) != 1 || h.deletes[0] != "msg-1" {
		t.Fatalf("expected delete msg-1, got %+v", h.deletes)
	}
}

func TestDraftStreamController_ThreadedDraftEditsInPlace(t *testing.T) {
	h := &draftTestHandle{id: "ch", nextID: "thread-msg-1"}
	c := NewDraftStreamController(h, DraftStreamOptions{ThreadID: "topic-42", MinEditInterval: time.Hour})
	if err := c.Append(context.Background(), "partial"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if len(h.sent) != 0 || len(h.threads) != 1 || h.threads[0] != "topic-42:partial" {
		t.Fatalf("expected threaded draft creation, sent=%v threads=%v", h.sent, h.threads)
	}
	if _, err := c.Finalize(context.Background(), "final"); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if len(h.edits) != 1 || h.edits[0] != "thread-msg-1:final" {
		t.Fatalf("expected final in-place edit, got %v", h.edits)
	}
}

func TestDraftStreamController_ThreadRequiresReceiptSupport(t *testing.T) {
	h := &draftSendOnlyHandle{id: "ch"}
	c := NewDraftStreamController(h, DraftStreamOptions{ThreadID: "topic-42"})
	if _, err := c.Finalize(context.Background(), "final"); err == nil {
		t.Fatal("expected explicit error when threaded receipt sends are unsupported")
	}
	if len(h.sent) != 0 {
		t.Fatalf("threaded draft must not fall back to root send: %v", h.sent)
	}
}

func TestDraftStreamController_SendOnlyDefersUntilFinalize(t *testing.T) {
	h := &draftSendOnlyHandle{id: "ch"}
	c := NewDraftStreamController(h, DraftStreamOptions{})
	if err := c.Append(context.Background(), "partial"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if len(h.sent) != 0 {
		t.Fatalf("send-only handle should not spam partials, got %+v", h.sent)
	}
	if _, err := c.Finalize(context.Background(), "final"); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if len(h.sent) != 1 || h.sent[0] != "final" {
		t.Fatalf("expected one final send, got %+v", h.sent)
	}
	if err := c.DeleteDraft(context.Background()); err != nil {
		t.Fatalf("delete without draft id should be nil, got %v", err)
	}
}

func TestDraftStreamController_RejectsUpdateAfterFinalize(t *testing.T) {
	h := &draftTestHandle{id: "ch"}
	c := NewDraftStreamController(h, DraftStreamOptions{})
	if _, err := c.Finalize(context.Background(), "done"); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if err := c.Append(context.Background(), "late"); err == nil {
		t.Fatal("expected error after finalize")
	}
}
