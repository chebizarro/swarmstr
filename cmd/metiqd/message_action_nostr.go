package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"

	okpublish "metiq/internal/nostr/publish"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

type messageNostrRef struct {
	EventID    string
	PubKey     string
	Transport  string
	Kind       nostr.Kind
	Relays     []string
	Recipients []string
}

type messageActionNostrPropagator interface {
	Delete(context.Context, messageNostrRef) error
	React(context.Context, messageNostrRef, string) error
}

type daemonMessageActionNostrPropagator struct {
	hub   *nostruntime.NostrHub
	keyer nostr.Keyer
	nip17 *nostruntime.NIP17Bus
}

func (h controlRPCHandler) messageActionNostrPropagator() messageActionNostrPropagator {
	if h.deps.messageNostr != nil {
		return h.deps.messageNostr
	}
	var nip17 *nostruntime.NIP17Bus
	if h.deps.services != nil {
		nip17 = h.deps.services.relay.nip17Bus
	}
	if h.deps.nostrHub == nil && h.deps.keyer == nil && nip17 == nil {
		return nil
	}
	return daemonMessageActionNostrPropagator{hub: h.deps.nostrHub, keyer: h.deps.keyer, nip17: nip17}
}

func (h controlRPCHandler) propagateMessageDelete(ctx context.Context, entry state.TranscriptEntryDoc) (bool, error) {
	ref, published, err := messageNostrRefFromEntry(entry)
	if err != nil || !published {
		return false, err
	}
	propagator := h.messageActionNostrPropagator()
	if propagator == nil {
		return false, fmt.Errorf("nostr message action propagation is not configured")
	}
	if err := propagator.Delete(ctx, ref); err != nil {
		return false, fmt.Errorf("propagate NIP-09 deletion: %w", err)
	}
	return true, nil
}

func (h controlRPCHandler) propagateMessageReaction(ctx context.Context, entry state.TranscriptEntryDoc, reaction string) (bool, error) {
	ref, published, err := messageNostrRefFromEntry(entry)
	if err != nil || !published {
		return false, err
	}
	propagator := h.messageActionNostrPropagator()
	if propagator == nil {
		return false, fmt.Errorf("nostr message action propagation is not configured")
	}
	if err := propagator.React(ctx, ref, reaction); err != nil {
		return false, fmt.Errorf("propagate NIP-25 reaction: %w", err)
	}
	return true, nil
}

func messageNostrRefFromEntry(entry state.TranscriptEntryDoc) (messageNostrRef, bool, error) {
	if entry.Meta == nil {
		return messageNostrRef{}, false, nil
	}
	eventID := strings.TrimSpace(metaString(entry.Meta["nostr_event_id"]))
	if eventID == "" {
		return messageNostrRef{}, false, nil
	}
	if _, err := nostr.IDFromHex(eventID); err != nil {
		return messageNostrRef{}, false, fmt.Errorf("message %q has invalid nostr_event_id: %w", entry.EntryID, err)
	}
	pubkey := strings.TrimSpace(metaString(entry.Meta["nostr_pubkey"]))
	if pubkey != "" {
		if _, err := nostr.PubKeyFromHex(pubkey); err != nil {
			return messageNostrRef{}, false, fmt.Errorf("message %q has invalid nostr_pubkey: %w", entry.EntryID, err)
		}
	}
	kind, err := metaInt(entry.Meta["nostr_kind"])
	if err != nil {
		return messageNostrRef{}, false, fmt.Errorf("message %q has invalid nostr_kind: %w", entry.EntryID, err)
	}
	return messageNostrRef{
		EventID: eventID, PubKey: pubkey, Kind: nostr.Kind(kind),
		Transport:  strings.ToLower(strings.TrimSpace(metaString(entry.Meta["nostr_transport"]))),
		Relays:     compactStrings(metaStrings(entry.Meta["nostr_relays"])),
		Recipients: compactStrings(metaStrings(entry.Meta["nostr_recipients"])),
	}, true, nil
}

func (p daemonMessageActionNostrPropagator) Delete(ctx context.Context, ref messageNostrRef) error {
	if p.keyer == nil {
		return fmt.Errorf("nostr signing keyer not configured")
	}
	own, err := p.keyer.GetPublicKey(ctx)
	if err != nil {
		return fmt.Errorf("resolve nostr signing pubkey: %w", err)
	}
	if ref.PubKey == "" || own.Hex() != ref.PubKey {
		return fmt.Errorf("cannot publish deletion for a nostr event not authored by the local key")
	}
	if ref.Transport == "nip17" {
		if p.nip17 == nil {
			return fmt.Errorf("NIP-17 transport not configured")
		}
		room, err := nip17RoomFromRef(ref, own.Hex())
		if err != nil {
			return err
		}
		return p.nip17.DeleteMessages(ctx, room, "deleted via message.action", nostruntime.NIP17DeletionTarget{EventID: ref.EventID, Kind: ref.Kind})
	}
	return p.publishPublicTo(ctx, ref.Relays, nostr.Event{
		CreatedAt: nostr.Timestamp(time.Now().Unix()), Kind: nostr.KindDeletion,
		Tags:    nostr.Tags{{"e", ref.EventID}, {"k", fmt.Sprint(ref.Kind)}},
		Content: "deleted via message.action",
	})
}

func (p daemonMessageActionNostrPropagator) React(ctx context.Context, ref messageNostrRef, reaction string) error {
	if ref.Transport == "nip17" {
		if p.nip17 == nil {
			return fmt.Errorf("NIP-17 transport not configured")
		}
		if p.keyer == nil {
			return fmt.Errorf("nostr signing keyer not configured")
		}
		own, err := p.keyer.GetPublicKey(ctx)
		if err != nil {
			return fmt.Errorf("resolve nostr signing pubkey: %w", err)
		}
		room, err := nip17RoomFromRef(ref, own.Hex())
		if err != nil {
			return err
		}
		relayHint := ""
		if len(ref.Relays) > 0 {
			relayHint = ref.Relays[0]
		}
		return p.nip17.SendReactionTo(ctx, room, ref.EventID, ref.PubKey, relayHint, reaction)
	}
	if ref.PubKey == "" {
		return fmt.Errorf("nostr reaction target pubkey is missing")
	}
	return p.publishPublicTo(ctx, ref.Relays, nostr.Event{
		CreatedAt: nostr.Timestamp(time.Now().Unix()), Kind: nostr.KindReaction,
		Tags:    nostr.Tags{{"e", ref.EventID}, {"p", ref.PubKey}, {"k", fmt.Sprint(ref.Kind)}},
		Content: reaction,
	})
}

func (p daemonMessageActionNostrPropagator) publishPublicTo(ctx context.Context, relays []string, event nostr.Event) error {
	if p.hub == nil || p.hub.Pool() == nil {
		return fmt.Errorf("nostr relay hub not configured")
	}
	if p.keyer == nil {
		return fmt.Errorf("nostr signing keyer not configured")
	}
	relays = compactStrings(relays)
	if len(relays) == 0 {
		return fmt.Errorf("nostr message action relays not supplied")
	}
	if err := p.keyer.SignEvent(ctx, &event); err != nil {
		return fmt.Errorf("sign nostr message action: %w", err)
	}
	if _, err := okpublish.PublishToAny(ctx, p.hub.Pool(), relays, event); err != nil {
		return fmt.Errorf("publish nostr message action: %w", err)
	}
	return nil
}

func nip17RoomFromRef(ref messageNostrRef, ownPubKey string) (nostruntime.NIP17Room, error) {
	participants := make([]nostruntime.NIP17Participant, 0, len(ref.Recipients)+1)
	seen := map[string]struct{}{}
	for _, raw := range append(append([]string(nil), ref.Recipients...), ref.PubKey) {
		raw = strings.TrimSpace(raw)
		if raw == "" || raw == ownPubKey {
			continue
		}
		if _, ok := seen[raw]; ok {
			continue
		}
		pk, err := nostr.PubKeyFromHex(raw)
		if err != nil {
			return nostruntime.NIP17Room{}, fmt.Errorf("invalid NIP-17 participant: %w", err)
		}
		seen[raw] = struct{}{}
		relayHint := ""
		if len(ref.Relays) > 0 {
			relayHint = ref.Relays[0]
		}
		participants = append(participants, nostruntime.NIP17Participant{PubKey: pk.Hex(), RelayURL: relayHint})
	}
	if len(participants) == 0 {
		return nostruntime.NIP17Room{}, fmt.Errorf("NIP-17 message action has no participants")
	}
	return nostruntime.NIP17Room{Participants: participants}, nil
}

func metaString(value any) string {
	str, _ := value.(string)
	return str
}

func metaStrings(value any) []string {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if str, ok := value.(string); ok {
				out = append(out, str)
			}
		}
		return out
	default:
		return nil
	}
}

func metaInt(value any) (int, error) {
	switch number := value.(type) {
	case int:
		return number, nil
	case int64:
		return int(number), nil
	case float64:
		return int(number), nil
	case json.Number:
		parsed, err := number.Int64()
		return int(parsed), err
	case nil:
		return 0, fmt.Errorf("value is missing")
	default:
		return 0, fmt.Errorf("unsupported type %T", value)
	}
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
