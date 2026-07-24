package channels

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// EditMessageHandle is the local optional shape used for live draft updates.
// Existing sdk.EditHandle implementations satisfy it without importing sdk here.
type EditMessageHandle interface {
	EditMessage(ctx context.Context, eventID, newText string) error
}

// ThreadSendReceiptHandle is implemented by thread-capable channels that can
// return the created platform message ID, allowing subsequent draft edits to
// remain bound to the original thread.
type ThreadSendReceiptHandle interface {
	SendInThreadWithReceipt(ctx context.Context, threadID, text string) (DeliveryReceipt, error)
}

// DeleteMessageHandle is implemented by channels that can retract a platform
// message. It intentionally lives in channels to avoid changing the shared SDK
// package while still allowing duck-typed extension support.
type DeleteMessageHandle interface {
	DeleteMessage(ctx context.Context, eventID string) error
}

// DraftStreamOptions configures live draft preview behavior.
type DraftStreamOptions struct {
	MinEditInterval time.Duration
	InitialText     string
	MaxPreviewRunes int
	// ThreadID binds draft creation to a platform thread/topic. Editing then
	// targets the receipt returned from that threaded send.
	ThreadID string
}

// DraftStreamController creates a draft message and keeps it updated as text
// streams in. It edits when the handle supports EditMessage; otherwise it keeps
// the latest text and sends once on Finalize to avoid noisy fallback spam.
type DraftStreamController struct {
	sender         BasicSender
	receipts       SendReceiptHandle
	threadReceipts ThreadSendReceiptHandle
	editor         EditMessageHandle
	deleter        DeleteMessageHandle
	opts           DraftStreamOptions

	mu       sync.Mutex
	text     string
	draftID  string
	created  bool
	receipt  DeliveryReceipt
	lastEdit time.Time
	inFlight bool
	closed   bool
}

// NewDraftStreamController builds a draft controller for a channel handle.
func NewDraftStreamController(handle BasicSender, opts DraftStreamOptions) *DraftStreamController {
	if opts.MinEditInterval <= 0 {
		opts.MinEditInterval = 750 * time.Millisecond
	}
	if opts.MaxPreviewRunes <= 0 {
		opts.MaxPreviewRunes = 4000
	}
	var receipts SendReceiptHandle
	if h, ok := handle.(SendReceiptHandle); ok {
		receipts = h
	}
	var threadReceipts ThreadSendReceiptHandle
	if h, ok := handle.(ThreadSendReceiptHandle); ok {
		threadReceipts = h
	}
	var editor EditMessageHandle
	if h, ok := handle.(EditMessageHandle); ok {
		editor = h
	}
	var deleter DeleteMessageHandle
	if h, ok := handle.(DeleteMessageHandle); ok {
		deleter = h
	}
	return &DraftStreamController{sender: handle, receipts: receipts, threadReceipts: threadReceipts, editor: editor, deleter: deleter, opts: opts, text: opts.InitialText}
}

// Append adds streamed text and updates the live draft if throttling permits.
func (d *DraftStreamController) Append(ctx context.Context, chunk string) error {
	if d == nil {
		return fmt.Errorf("draft stream: nil controller")
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return fmt.Errorf("draft stream: already finalized")
	}
	d.text += chunk
	d.text = truncateRunes(d.text, d.opts.MaxPreviewRunes)
	d.mu.Unlock()
	return d.flush(ctx, false)
}

// Update replaces the preview text and updates the live draft if throttling permits.
func (d *DraftStreamController) Update(ctx context.Context, text string) error {
	if d == nil {
		return fmt.Errorf("draft stream: nil controller")
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return fmt.Errorf("draft stream: already finalized")
	}
	d.text = truncateRunes(text, d.opts.MaxPreviewRunes)
	d.mu.Unlock()
	return d.flush(ctx, false)
}

// Flush forces a draft update immediately.
func (d *DraftStreamController) Flush(ctx context.Context) error {
	return d.flush(ctx, true)
}

// Finalize flushes finalText (if provided) and marks the stream complete.
func (d *DraftStreamController) Finalize(ctx context.Context, finalText string) (DeliveryReceipt, error) {
	if d == nil {
		return DeliveryReceipt{Status: DeliveryFailed, Error: "nil controller", CreatedAt: time.Now()}, fmt.Errorf("draft stream: nil controller")
	}
	d.mu.Lock()
	if strings.TrimSpace(finalText) != "" {
		d.text = truncateRunes(finalText, d.opts.MaxPreviewRunes)
	}
	d.closed = true
	d.mu.Unlock()
	if err := d.flush(ctx, true); err != nil {
		return d.currentReceipt(), err
	}
	return d.currentReceipt(), nil
}

// DeleteDraft retracts the current draft when the channel supports deletion.
func (d *DraftStreamController) DeleteDraft(ctx context.Context) error {
	if d == nil {
		return fmt.Errorf("draft stream: nil controller")
	}
	d.mu.Lock()
	id := d.draftID
	deleter := d.deleter
	d.mu.Unlock()
	if id == "" {
		return nil
	}
	if deleter == nil {
		return fmt.Errorf("draft stream: delete unsupported")
	}
	return deleter.DeleteMessage(ctx, id)
}

func (d *DraftStreamController) flush(ctx context.Context, force bool) error {
	d.mu.Lock()
	if d.inFlight {
		d.mu.Unlock()
		return nil
	}
	text := strings.TrimSpace(d.text)
	if text == "" {
		d.mu.Unlock()
		return nil
	}
	now := time.Now()
	if !force && !d.lastEdit.IsZero() && now.Sub(d.lastEdit) < d.opts.MinEditInterval {
		d.mu.Unlock()
		return nil
	}
	needsCreate := !d.created
	id := d.draftID
	editor := d.editor
	receipts := d.receipts
	threadReceipts := d.threadReceipts
	threadID := strings.TrimSpace(d.opts.ThreadID)
	sender := d.sender
	d.inFlight = true
	d.mu.Unlock()

	var receipt DeliveryReceipt
	var err error
	if needsCreate && editor == nil && !force {
		d.mu.Lock()
		d.inFlight = false
		d.mu.Unlock()
		return nil
	}
	if needsCreate {
		if threadID != "" && threadReceipts != nil {
			receipt, err = threadReceipts.SendInThreadWithReceipt(ctx, threadID, text)
		} else if threadID != "" {
			err = fmt.Errorf("draft stream: threaded send receipts unsupported")
		} else if receipts != nil {
			receipt, err = receipts.SendWithReceipt(ctx, text)
		} else if sender != nil {
			err = sender.Send(ctx, text)
			receipt = DeliveryReceipt{ChannelID: sender.ID(), Status: DeliveryDelivered, CreatedAt: now, DeliveredAt: time.Now()}
		} else {
			err = fmt.Errorf("draft stream: nil sender")
		}
	} else if editor != nil && id != "" {
		err = editor.EditMessage(ctx, id, text)
		receipt = d.currentReceipt()
	} else {
		// Non-edit channels keep accumulating and emit only the initial/final send.
		receipt = d.currentReceipt()
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	d.inFlight = false
	if err != nil {
		return err
	}
	if needsCreate {
		d.created = true
		d.receipt = receipt
		if receipt.MessageID != "" {
			d.draftID = receipt.MessageID
		}
	} else {
		d.receipt = receipt
	}
	d.lastEdit = time.Now()
	return nil
}

func (d *DraftStreamController) currentReceipt() DeliveryReceipt {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.receipt
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
