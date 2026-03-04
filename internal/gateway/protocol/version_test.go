package protocol

import "testing"

func TestNegotiateProtocol(t *testing.T) {
	version, err := NegotiateProtocol(1, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if version != 3 {
		t.Fatalf("version = %d, want 3", version)
	}

	version, err = NegotiateProtocol(2, 99)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if version != CurrentProtocolVersion {
		t.Fatalf("version = %d, want %d", version, CurrentProtocolVersion)
	}

	if _, err := NegotiateProtocol(4, 5); err == nil {
		t.Fatal("expected unsupported range error")
	}
	if _, err := NegotiateProtocol(3, 2); err == nil {
		t.Fatal("expected invalid range error")
	}
}
