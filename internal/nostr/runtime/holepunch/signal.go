//go:build experimental_fips

package holepunch

import (
	"encoding/json"
	"fmt"
	"time"
)

// SignalType discriminates offer vs answer signaling messages.
type SignalType string

const (
	SignalOffer  SignalType = "offer"
	SignalAnswer SignalType = "answer"
)

// Signal is the JSON payload exchanged during the offer/answer phase.
// It is NIP-44 encrypted and NIP-59 gift-wrapped before relay delivery.
type Signal struct {
	Type          SignalType      `json:"type"`
	SessionID     string          `json:"session_id"`
	ReflexiveAddr string          `json:"reflexive_addr"`
	LocalAddr     string          `json:"local_addr,omitempty"`
	STUNServer    string          `json:"stun_server"`
	Timestamp     int64           `json:"timestamp"`
	AppParams     json.RawMessage `json:"app_params,omitempty"`
}

// Validate checks required fields.
func (s Signal) Validate() error {
	if s.Type != SignalOffer && s.Type != SignalAnswer {
		return fmt.Errorf("signal: invalid type %q", s.Type)
	}
	if len(s.SessionID) == 0 {
		return fmt.Errorf("signal: session_id is required")
	}
	if len(s.ReflexiveAddr) == 0 {
		return fmt.Errorf("signal: reflexive_addr is required")
	}
	if s.Timestamp == 0 {
		return fmt.Errorf("signal: timestamp is required")
	}
	return nil
}

// IsExpired returns true if the signal is older than maxAge.
func (s Signal) IsExpired(maxAge time.Duration) bool {
	return time.Since(time.Unix(s.Timestamp, 0)) > maxAge
}

// MarshalJSON encodes the signal for encryption.
func (s Signal) Marshal() ([]byte, error) {
	return json.Marshal(s)
}

// UnmarshalSignal decodes a signal from decrypted JSON.
func UnmarshalSignal(data []byte) (Signal, error) {
	var s Signal
	if err := json.Unmarshal(data, &s); err != nil {
		return Signal{}, fmt.Errorf("signal: unmarshal: %w", err)
	}
	if err := s.Validate(); err != nil {
		return Signal{}, err
	}
	return s, nil
}

// ServiceAdvertisement represents a kind:30078 UDP service advertisement
// for a peer that supports hole punching.
type ServiceAdvertisement struct {
	// PubKey is the advertiser's Nostr pubkey (hex).
	PubKey string `json:"pubkey"`
	// AppID is the application identifier (d-tag suffix after "udp-service-v1/").
	AppID string `json:"app_id"`
	// Protocol is the application protocol name.
	Protocol string `json:"protocol"`
	// Version is the protocol version string.
	Version string `json:"version"`
	// Relays where the advertiser listens for signaling messages.
	Relays []string `json:"relays"`
	// STUNServers preferred by the advertiser.
	STUNServers []string `json:"stun_servers"`
	// ExpiresAt is the unix timestamp when this advertisement expires.
	ExpiresAt int64 `json:"expires_at,omitempty"`
}

// DTag returns the NIP-78 d-tag for this advertisement.
func (sa ServiceAdvertisement) DTag() string {
	return "udp-service-v1/" + sa.AppID
}

// IsExpiredAt returns true if the advertisement has expired at the given time.
func (sa ServiceAdvertisement) IsExpiredAt(t time.Time) bool {
	if sa.ExpiresAt <= 0 {
		return false
	}
	return t.Unix() > sa.ExpiresAt
}
