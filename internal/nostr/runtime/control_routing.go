package runtime

import (
	"context"

	nostr "fiatjaf.com/nostr"
)

// ControlRequestRelayCandidates returns the deterministic relay set for a
// control request authored by callerPubKey and tagged to targetPubKey.
//
// Routing rule:
//   - publish to the caller's write relays
//   - also publish to the target's read relays
//   - fall back to the configured query relays when NIP-65 data is unavailable
func ControlRequestRelayCandidates(ctx context.Context, selector *RelaySelector, pool *nostr.Pool, queryRelays []string, callerPubKey, targetPubKey string) []string {
	return mergeControlRelayGroups(
		controlAuthorPublishRelays(ctx, selector, pool, queryRelays, callerPubKey),
		controlTargetReadRelays(ctx, selector, pool, queryRelays, targetPubKey),
	)
}

// ControlResponseRelayCandidates returns the deterministic relay set for a
// control response authored by responderPubKey and tagged to requesterPubKey.
//
// Routing rule:
//   - prefer the request relay first when present
//   - then publish to the responder's write relays
//   - then to the requester's read relays
//   - fall back to the configured query relays when NIP-65 data is unavailable
func ControlResponseRelayCandidates(ctx context.Context, selector *RelaySelector, pool *nostr.Pool, queryRelays []string, responderPubKey, requesterPubKey, preferredRelay string) []string {
	base := mergeControlRelayGroups(
		controlAuthorPublishRelays(ctx, selector, pool, queryRelays, responderPubKey),
		controlTargetReadRelays(ctx, selector, pool, queryRelays, requesterPubKey),
	)
	return mergeControlRelayGroups([]string{preferredRelay}, base)
}

// ControlResponseListenRelayCandidates returns the relays a requester should
// listen on for a control response from responderPubKey after publishing the
// request on requestRelays.
func ControlResponseListenRelayCandidates(ctx context.Context, selector *RelaySelector, pool *nostr.Pool, queryRelays []string, responderPubKey, requesterPubKey string, requestRelays []string) []string {
	return mergeControlRelayGroups(
		requestRelays,
		controlAuthorPublishRelays(ctx, selector, pool, queryRelays, responderPubKey),
		controlTargetReadRelays(ctx, selector, pool, queryRelays, requesterPubKey),
	)
}

func controlAuthorPublishRelays(ctx context.Context, selector *RelaySelector, pool *nostr.Pool, queryRelays []string, authorPubKey string) []string {
	if selector == nil {
		return sanitizeRelayList(queryRelays)
	}
	if pool == nil {
		if list := selector.Get(authorPubKey); list != nil {
			if relays := sanitizeRelayList(list.WriteRelays()); len(relays) > 0 {
				return relays
			}
		}
		return sanitizeRelayList(queryRelays)
	}
	return sanitizeRelayList(selector.RelaysForPublishingAsAuthor(ctx, pool, queryRelays, authorPubKey))
}

func controlTargetReadRelays(ctx context.Context, selector *RelaySelector, pool *nostr.Pool, queryRelays []string, targetPubKey string) []string {
	if selector == nil {
		return sanitizeRelayList(queryRelays)
	}
	if pool == nil {
		if list := selector.Get(targetPubKey); list != nil {
			if relays := sanitizeRelayList(list.ReadRelays()); len(relays) > 0 {
				return relays
			}
		}
		return sanitizeRelayList(queryRelays)
	}
	return sanitizeRelayList(selector.RelaysForDownloadingAbout(ctx, pool, queryRelays, targetPubKey))
}

func mergeControlRelayGroups(groups ...[]string) []string {
	out := make([]string, 0)
	seen := map[string]struct{}{}
	for _, group := range groups {
		for _, relay := range group {
			for _, cleaned := range sanitizeRelayList([]string{relay}) {
				if _, ok := seen[cleaned]; ok {
					continue
				}
				seen[cleaned] = struct{}{}
				out = append(out, cleaned)
			}
		}
	}
	return out
}
