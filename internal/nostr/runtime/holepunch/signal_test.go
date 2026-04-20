//go:build experimental_fips

package holepunch

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSignal_MarshalRoundtrip(t *testing.T) {
	orig := Signal{
		Type:          SignalOffer,
		SessionID:     "abc123def456",
		ReflexiveAddr: "198.51.100.1:12345",
		LocalAddr:     "192.168.1.10:54321",
		STUNServer:    "stun.l.google.com:19302",
		Timestamp:     time.Now().Unix(),
		AppParams:     json.RawMessage(`{"from":"aabbccdd"}`),
	}

	data, err := orig.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	decoded, err := UnmarshalSignal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Type != orig.Type {
		t.Errorf("type = %q, want %q", decoded.Type, orig.Type)
	}
	if decoded.SessionID != orig.SessionID {
		t.Errorf("session_id = %q, want %q", decoded.SessionID, orig.SessionID)
	}
	if decoded.ReflexiveAddr != orig.ReflexiveAddr {
		t.Errorf("reflexive_addr = %q, want %q", decoded.ReflexiveAddr, orig.ReflexiveAddr)
	}
	if decoded.LocalAddr != orig.LocalAddr {
		t.Errorf("local_addr = %q, want %q", decoded.LocalAddr, orig.LocalAddr)
	}
	if decoded.STUNServer != orig.STUNServer {
		t.Errorf("stun_server = %q, want %q", decoded.STUNServer, orig.STUNServer)
	}
}

func TestSignal_Validate_OK(t *testing.T) {
	s := Signal{
		Type:          SignalAnswer,
		SessionID:     "session-1",
		ReflexiveAddr: "1.2.3.4:5678",
		Timestamp:     time.Now().Unix(),
	}
	if err := s.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSignal_Validate_MissingType(t *testing.T) {
	s := Signal{SessionID: "s", ReflexiveAddr: "1.2.3.4:5", Timestamp: 1}
	if err := s.Validate(); err == nil {
		t.Error("expected error for invalid type")
	}
}

func TestSignal_Validate_MissingSessionID(t *testing.T) {
	s := Signal{Type: SignalOffer, ReflexiveAddr: "1.2.3.4:5", Timestamp: 1}
	if err := s.Validate(); err == nil {
		t.Error("expected error for missing session_id")
	}
}

func TestSignal_Validate_MissingAddr(t *testing.T) {
	s := Signal{Type: SignalOffer, SessionID: "s", Timestamp: 1}
	if err := s.Validate(); err == nil {
		t.Error("expected error for missing reflexive_addr")
	}
}

func TestSignal_Validate_MissingTimestamp(t *testing.T) {
	s := Signal{Type: SignalOffer, SessionID: "s", ReflexiveAddr: "1.2.3.4:5"}
	if err := s.Validate(); err == nil {
		t.Error("expected error for missing timestamp")
	}
}

func TestSignal_IsExpired(t *testing.T) {
	fresh := Signal{Timestamp: time.Now().Unix()}
	if fresh.IsExpired(60 * time.Second) {
		t.Error("fresh signal should not be expired")
	}

	stale := Signal{Timestamp: time.Now().Add(-2 * time.Minute).Unix()}
	if !stale.IsExpired(60 * time.Second) {
		t.Error("2-minute-old signal should be expired with 60s max age")
	}
}

func TestServiceAdvertisement_DTag(t *testing.T) {
	sa := ServiceAdvertisement{AppID: "fips-agent"}
	if sa.DTag() != "udp-service-v1/fips-agent" {
		t.Errorf("dtag = %q", sa.DTag())
	}
}

func TestServiceAdvertisement_IsExpiredAt(t *testing.T) {
	now := time.Now()
	sa := ServiceAdvertisement{ExpiresAt: now.Add(-time.Minute).Unix()}
	if !sa.IsExpiredAt(now) {
		t.Error("expected expired")
	}

	sa2 := ServiceAdvertisement{ExpiresAt: now.Add(time.Minute).Unix()}
	if sa2.IsExpiredAt(now) {
		t.Error("expected not expired")
	}

	sa3 := ServiceAdvertisement{ExpiresAt: 0}
	if sa3.IsExpiredAt(now) {
		t.Error("no expiry should not be expired")
	}
}
