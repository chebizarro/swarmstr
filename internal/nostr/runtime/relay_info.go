package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const defaultRelayInfoTTL = 10 * time.Minute

// RelayInformation is the NIP-11 relay information document.
type RelayInformation struct {
	Name           string          `json:"name,omitempty"`
	Description    string          `json:"description,omitempty"`
	Banner         string          `json:"banner,omitempty"`
	Icon           string          `json:"icon,omitempty"`
	PubKey         string          `json:"pubkey,omitempty"`
	Self           string          `json:"self,omitempty"`
	Contact        string          `json:"contact,omitempty"`
	SupportedNIPs  []int           `json:"supported_nips,omitempty"`
	Software       string          `json:"software,omitempty"`
	Version        string          `json:"version,omitempty"`
	TermsOfService string          `json:"terms_of_service,omitempty"`
	PaymentsURL    string          `json:"payments_url,omitempty"`
	Limitation     RelayLimitation `json:"limitation,omitempty"`
}

// RelayLimitation contains relay policy limits advertised in NIP-11.
type RelayLimitation struct {
	MaxMessageLength    int  `json:"max_message_length,omitempty"`
	MaxSubscriptions    int  `json:"max_subscriptions,omitempty"`
	MaxLimit            int  `json:"max_limit,omitempty"`
	MaxSubIDLength      int  `json:"max_subid_length,omitempty"`
	MaxEventTags        int  `json:"max_event_tags,omitempty"`
	MaxContentLength    int  `json:"max_content_length,omitempty"`
	MinPOWDifficulty    int  `json:"min_pow_difficulty,omitempty"`
	AuthRequired        bool `json:"auth_required,omitempty"`
	PaymentRequired     bool `json:"payment_required,omitempty"`
	RestrictedWrites    bool `json:"restricted_writes,omitempty"`
	CreatedAtLowerLimit int  `json:"created_at_lower_limit,omitempty"`
	CreatedAtUpperLimit int  `json:"created_at_upper_limit,omitempty"`
	DefaultLimit        int  `json:"default_limit,omitempty"`
}

// Supports reports whether the relay advertises support for a NIP number.
func (ri *RelayInformation) Supports(nip int) bool {
	if ri == nil {
		return false
	}
	for _, supported := range ri.SupportedNIPs {
		if supported == nip {
			return true
		}
	}
	return false
}

type relayInfoCacheEntry struct {
	info      *RelayInformation
	expiresAt time.Time
}

var relayInfoCache = struct {
	sync.Mutex
	entries map[string]relayInfoCacheEntry
}{entries: map[string]relayInfoCacheEntry{}}

// RelayInfo fetches and caches a relay's NIP-11 information document.
func RelayInfo(ctx context.Context, relayURL string) (*RelayInformation, error) {
	return RelayInfoWithClient(ctx, http.DefaultClient, relayURL, defaultRelayInfoTTL)
}

// RelayInfoWithClient fetches a relay's NIP-11 document using client and ttl.
// It is exported primarily for tests and callers that need custom HTTP policy.
func RelayInfoWithClient(ctx context.Context, client *http.Client, relayURL string, ttl time.Duration) (*RelayInformation, error) {
	if client == nil {
		client = http.DefaultClient
	}
	url, err := relayInfoHTTPURL(relayURL)
	if err != nil {
		return nil, err
	}
	if ttl > 0 {
		if info := cachedRelayInfo(url, time.Now()); info != nil {
			return info, nil
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("relay info: build request: %w", err)
	}
	req.Header.Set("Accept", "application/nostr+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("relay info: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("relay info: fetch %s: status %s", url, resp.Status)
	}
	var info RelayInformation
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("relay info: decode %s: %w", url, err)
	}
	if ttl > 0 {
		storeRelayInfo(url, &info, time.Now().Add(ttl))
	}
	return &info, nil
}

func relayInfoHTTPURL(relayURL string) (string, error) {
	relayURL = strings.TrimSpace(relayURL)
	if relayURL == "" {
		return "", fmt.Errorf("relay info: relay URL is empty")
	}
	switch {
	case strings.HasPrefix(relayURL, "wss://"):
		return "https://" + strings.TrimPrefix(relayURL, "wss://"), nil
	case strings.HasPrefix(relayURL, "ws://"):
		return "http://" + strings.TrimPrefix(relayURL, "ws://"), nil
	case strings.HasPrefix(relayURL, "https://") || strings.HasPrefix(relayURL, "http://"):
		return relayURL, nil
	default:
		return "", fmt.Errorf("relay info: unsupported relay URL %q", relayURL)
	}
}

func cachedRelayInfo(url string, now time.Time) *RelayInformation {
	relayInfoCache.Lock()
	defer relayInfoCache.Unlock()
	entry, ok := relayInfoCache.entries[url]
	if !ok || now.After(entry.expiresAt) {
		return nil
	}
	copy := *entry.info
	copy.SupportedNIPs = append([]int(nil), entry.info.SupportedNIPs...)
	return &copy
}

func storeRelayInfo(url string, info *RelayInformation, expiresAt time.Time) {
	copy := *info
	copy.SupportedNIPs = append([]int(nil), info.SupportedNIPs...)
	relayInfoCache.Lock()
	relayInfoCache.entries[url] = relayInfoCacheEntry{info: &copy, expiresAt: expiresAt}
	relayInfoCache.Unlock()
}
