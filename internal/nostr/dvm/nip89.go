package dvm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	nostr "fiatjaf.com/nostr"
)

const (
	// KindHandlerRecommendation is the optional NIP-89 recommendation event kind.
	KindHandlerRecommendation = 31989
	// KindHandlerInformation is the NIP-89 handler information event kind.
	KindHandlerInformation = 31990
)

// HandlerInformationOptions describes optional metadata and handler references
// for a NIP-89 kind:31990 handler-information event.
type HandlerInformationOptions struct {
	D           string
	Name        string
	About       string
	Picture     string
	WebHandlers []HandlerReference
}

// HandlerReference adds a platform handler tag such as ["web", url, entity].
type HandlerReference struct {
	Platform string
	URL      string
	Entity   string
}

// BuildHandlerInformationEvent constructs an unsigned NIP-89 kind:31990 event
// advertising the provided NIP-90 job request kinds via k tags.
func BuildHandlerInformationEvent(pubkey nostr.PubKey, jobKinds []int, opts HandlerInformationOptions) (nostr.Event, error) {
	kinds := normalizeJobKinds(jobKinds)
	if len(kinds) == 0 {
		return nostr.Event{}, fmt.Errorf("dvm nip89: at least one job kind is required")
	}
	if opts.D == "" {
		opts.D = "dvm"
	}

	tags := nostr.Tags{{"d", opts.D}}
	for _, kind := range kinds {
		tags = append(tags, nostr.Tag{"k", strconv.Itoa(kind)})
	}
	for _, ref := range opts.WebHandlers {
		if ref.Platform == "" || ref.URL == "" {
			continue
		}
		tag := nostr.Tag{ref.Platform, ref.URL}
		if ref.Entity != "" {
			tag = append(tag, ref.Entity)
		}
		tags = append(tags, tag)
	}

	evt := nostr.Event{
		PubKey:    pubkey,
		Kind:      KindHandlerInformation,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags:      tags,
		Content:   handlerInformationContent(opts),
	}
	return evt, nil
}

// PublishHandlerInformation signs and publishes this handler's NIP-89
// kind:31990 event to its current relays.
func (h *Handler) PublishHandlerInformation(ctx context.Context, opts HandlerInformationOptions) (string, error) {
	if h == nil {
		return "", fmt.Errorf("dvm nip89: handler is nil")
	}
	evt, err := BuildHandlerInformationEvent(h.pubkey, h.opts.AcceptedKinds, opts)
	if err != nil {
		return "", err
	}
	if err := h.signEvent(ctx, &evt); err != nil {
		return "", fmt.Errorf("dvm nip89: sign handler information: %w", err)
	}
	if err := h.publish(ctx, evt); err != nil {
		return "", fmt.Errorf("dvm nip89: publish handler information: %w", err)
	}
	return evt.ID.Hex(), nil
}

func normalizeJobKinds(jobKinds []int) []int {
	seen := map[int]struct{}{}
	out := make([]int, 0, len(jobKinds))
	for _, kind := range jobKinds {
		if kind < 5000 || kind > 5999 {
			continue
		}
		if _, ok := seen[kind]; ok {
			continue
		}
		seen[kind] = struct{}{}
		out = append(out, kind)
	}
	sort.Ints(out)
	return out
}

func handlerInformationContent(opts HandlerInformationOptions) string {
	meta := map[string]string{}
	if opts.Name != "" {
		meta["name"] = opts.Name
	}
	if opts.About != "" {
		meta["about"] = opts.About
	}
	if opts.Picture != "" {
		meta["picture"] = opts.Picture
	}
	if len(meta) == 0 {
		return ""
	}
	b, _ := json.Marshal(meta)
	return string(b)
}
