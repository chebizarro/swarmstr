package nip77

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"
)

const defaultTransferBatch = 50

type SyncOptions struct {
	SubscriptionID string
	FrameSizeLimit int
	TransferBatch  int
}

type SyncResult struct {
	LocalOnly  []nostr.ID
	RemoteOnly []nostr.ID
	Uploaded   int
	Downloaded int
}

// Sync reconciles the filtered local event set with a NIP-77 relay and then
// transfers complete events with ordinary EVENT/OK and REQ/EOSE flows.
func Sync(ctx context.Context, relayURL string, filter nostr.Filter, source nostr.Querier, target nostr.Publisher, options SyncOptions) (result SyncResult, err error) {
	if source == nil && target == nil {
		return result, fmt.Errorf("NIP-77 sync requires an upload source or download target")
	}
	if !filterIsScoped(filter) {
		return result, fmt.Errorf("NIP-77 sync filter must be scoped")
	}
	batch := options.TransferBatch
	if batch == 0 {
		batch = defaultTransferBatch
	}
	if batch < 1 || batch > 500 {
		return result, fmt.Errorf("NIP-77 transfer batch must be between 1 and 500")
	}
	subscriptionID := options.SubscriptionID
	if subscriptionID == "" {
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return result, fmt.Errorf("create NIP-77 subscription id: %w", err)
		}
		subscriptionID = hex.EncodeToString(random[:])
	}

	records := make([]Record, 0)
	uploadEvents := make(map[nostr.ID]nostr.Event)
	seenRecords := make(map[nostr.ID]struct{})
	collect := func(query nostr.Querier, uploads bool) error {
		if query == nil {
			return nil
		}
		for event := range query.QueryEvents(filter) {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := validateEvent(event, filter); err != nil {
				return fmt.Errorf("invalid local event %s: %w", event.ID.Hex(), err)
			}
			if uploads {
				uploadEvents[event.ID] = event
			}
			if _, exists := seenRecords[event.ID]; exists {
				continue
			}
			seenRecords[event.ID] = struct{}{}
			records = append(records, Record{Timestamp: event.CreatedAt, ID: event.ID})
		}
		return nil
	}
	if err := collect(source, true); err != nil {
		return result, err
	}
	if queryTarget, ok := target.(nostr.Querier); ok {
		if err := collect(queryTarget, false); err != nil {
			return result, err
		}
	}

	session, err := NewSession(records, SessionOptions{
		Initiator:       true,
		TrackLocalOnly:  source != nil,
		TrackRemoteOnly: target != nil,
		FrameSizeLimit:  options.FrameSizeLimit,
	})
	if err != nil {
		return result, err
	}
	initial, err := session.Start()
	if err != nil {
		return result, err
	}

	inbound := make(chan string, 32)
	overflow := make(chan error, 1)
	relay, err := nostr.RelayConnect(ctx, relayURL, nostr.RelayOptions{CustomHandler: func(data string) {
		if !strings.HasPrefix(data, "[\"NEG-") {
			return
		}
		select {
		case inbound <- data:
		default:
			select {
			case overflow <- fmt.Errorf("NIP-77 inbound frame queue overflow"):
			default:
			}
		}
	}})
	if err != nil {
		return result, fmt.Errorf("connect NIP-77 relay: %w", err)
	}
	defer relay.Close()
	opened := false
	closed := false
	defer func() {
		if opened && !closed {
			if closeFrame, closeErr := CloseFrame(subscriptionID); closeErr == nil {
				if raw, encodeErr := EncodeFrame(closeFrame); encodeErr == nil {
					relay.Write(raw)
				}
			}
		}
	}()

	open, _ := OpenFrame(subscriptionID, filter, initial)
	if err := writeFrame(relay, open); err != nil {
		return result, err
	}
	opened = true
	for {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-relay.Context().Done():
			return result, fmt.Errorf("NIP-77 relay disconnected: %w", context.Cause(relay.Context()))
		case queueErr := <-overflow:
			return result, queueErr
		case raw := <-inbound:
			frame, err := DecodeFrame([]byte(raw))
			if err != nil {
				return result, err
			}
			if frame.ID != subscriptionID {
				return result, protocolErrorf("unexpected subscription id %q", frame.ID)
			}
			switch frame.Kind {
			case FrameError:
				closed = true
				return result, &RemoteError{ID: frame.ID, Reason: frame.Reason, MaxRecords: frame.MaxRecords}
			case FrameClose:
				closed = true
				return result, protocolErrorf("relay closed reconciliation before completion")
			case FrameOpen:
				return result, protocolErrorf("relay sent unexpected NEG-OPEN")
			case FrameMsg:
				step, err := session.Reconcile(frame.Message)
				if err != nil {
					return result, err
				}
				result.LocalOnly = appendUnique(result.LocalOnly, step.LocalOnly...)
				result.RemoteOnly = appendUnique(result.RemoteOnly, step.RemoteOnly...)
				if step.Done {
					closeFrame, _ := CloseFrame(subscriptionID)
					if err := writeFrame(relay, closeFrame); err != nil {
						return result, err
					}
					closed = true
					goto transfer
				}
				message, _ := MessageFrame(subscriptionID, step.Next)
				if err := writeFrame(relay, message); err != nil {
					return result, err
				}
			}
		}
	}

transfer:
	sortIDs(result.LocalOnly)
	sortIDs(result.RemoteOnly)
	if target != nil {
		count, err := transferIDs(ctx, relay, target, filter, result.RemoteOnly, batch)
		result.Downloaded = count
		if err != nil {
			return result, fmt.Errorf("download reconciled events: %w", err)
		}
	}
	if source != nil {
		count, err := publishEvents(ctx, relay, uploadEvents, result.LocalOnly)
		result.Uploaded = count
		if err != nil {
			return result, fmt.Errorf("upload reconciled events: %w", err)
		}
	}
	return result, nil
}

func writeFrame(relay *nostr.Relay, frame Frame) error {
	raw, err := EncodeFrame(frame)
	if err != nil {
		return err
	}
	if err := relay.WriteWithError(raw); err != nil {
		return fmt.Errorf("write %s: %w", frame.Kind, err)
	}
	return nil
}

func transferIDs(ctx context.Context, from nostr.Querier, to nostr.Publisher, original nostr.Filter, ids []nostr.ID, batch int) (int, error) {
	transferred := 0
	for start := 0; start < len(ids); start += batch {
		end := start + batch
		if end > len(ids) {
			end = len(ids)
		}
		requested := make(map[nostr.ID]struct{}, end-start)
		for _, id := range ids[start:end] {
			requested[id] = struct{}{}
		}
		received := make(map[nostr.ID]struct{}, len(requested))
		for event := range from.QueryEvents(nostr.Filter{IDs: ids[start:end]}) {
			if err := ctx.Err(); err != nil {
				return transferred, err
			}
			if _, ok := requested[event.ID]; !ok {
				return transferred, fmt.Errorf("received unsolicited event %s", event.ID.Hex())
			}
			if _, duplicate := received[event.ID]; duplicate {
				continue
			}
			if err := validateEvent(event, original); err != nil {
				return transferred, fmt.Errorf("invalid event %s: %w", event.ID.Hex(), err)
			}
			if err := to.Publish(ctx, event); err != nil {
				return transferred, err
			}
			received[event.ID] = struct{}{}
			transferred++
		}
		if len(received) != len(requested) {
			return transferred, fmt.Errorf("relay omitted %d requested events", len(requested)-len(received))
		}
	}
	return transferred, nil
}

func publishEvents(ctx context.Context, relay nostr.Publisher, events map[nostr.ID]nostr.Event, ids []nostr.ID) (int, error) {
	published := 0
	for _, id := range ids {
		event, ok := events[id]
		if !ok {
			return published, fmt.Errorf("local source omitted event %s", id.Hex())
		}
		if err := relay.Publish(ctx, event); err != nil {
			return published, err
		}
		published++
	}
	return published, nil
}

func validateEvent(event nostr.Event, filter nostr.Filter) error {
	if event.CreatedAt < 0 || event.CreatedAt > nostr.Timestamp(time.Now().Add(10*time.Minute).Unix()) {
		return fmt.Errorf("event timestamp outside accepted bounds")
	}
	if !event.CheckID() {
		return fmt.Errorf("event id mismatch")
	}
	if !event.VerifySignature() {
		return fmt.Errorf("invalid event signature")
	}
	if !filter.Matches(event) {
		return fmt.Errorf("event does not match sync filter")
	}
	return nil
}

func filterIsScoped(filter nostr.Filter) bool {
	return filter.IDs != nil || filter.Kinds != nil || filter.Authors != nil || len(filter.Tags) > 0 || filter.Since != 0 || filter.Until != 0 || filter.Search != ""
}

func appendUnique(existing []nostr.ID, values ...nostr.ID) []nostr.ID {
	seen := make(map[nostr.ID]struct{}, len(existing)+len(values))
	for _, id := range existing {
		seen[id] = struct{}{}
	}
	for _, id := range values {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		existing = append(existing, id)
	}
	return existing
}

func sortIDs(ids []nostr.ID) {
	sort.Slice(ids, func(i, j int) bool { return strings.Compare(ids[i].Hex(), ids[j].Hex()) < 0 })
}
