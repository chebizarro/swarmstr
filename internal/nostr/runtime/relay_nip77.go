package runtime

import (
	"context"
	"errors"
	"net/http"
	"time"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/nostr/nip77"
)

const NIP77 = 77

var ErrNIP77Unsupported = errors.New("relay does not advertise NIP-77")

// RelaySupportsNIP77 checks the relay's cached NIP-11 information document.
func RelaySupportsNIP77(ctx context.Context, relayURL string) (bool, error) {
	info, err := RelayInfo(ctx, relayURL)
	if err != nil {
		return false, err
	}
	return info.Supports(NIP77), nil
}

// RelaySupportsNIP77WithClient is the testable/custom-policy form of RelaySupportsNIP77.
func RelaySupportsNIP77WithClient(ctx context.Context, client *http.Client, relayURL string, ttl time.Duration) (bool, error) {
	info, err := RelayInfoWithClient(ctx, client, relayURL, ttl)
	if err != nil {
		return false, err
	}
	return info.Supports(NIP77), nil
}

// SyncRelayState gates NIP-77 negotiation on the relay's NIP-11 declaration.
func SyncRelayState(ctx context.Context, relayURL string, filter nostr.Filter, source nostr.Querier, target nostr.Publisher, options nip77.SyncOptions) (nip77.SyncResult, error) {
	supported, err := RelaySupportsNIP77(ctx, relayURL)
	if err != nil {
		return nip77.SyncResult{}, err
	}
	if !supported {
		return nip77.SyncResult{}, ErrNIP77Unsupported
	}
	return nip77.Sync(ctx, relayURL, filter, source, target, options)
}
