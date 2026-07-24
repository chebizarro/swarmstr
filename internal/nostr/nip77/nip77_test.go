package nip77

import (
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"
)

func TestFrameWireFormatRoundTrip(t *testing.T) {
	filter := nostr.Filter{Kinds: []nostr.Kind{1}, Tags: nostr.TagMap{"p": {"abc"}}}
	maximum := uint64(5000)
	cases := []struct {
		name string
		make func() (Frame, error)
		want string
	}{
		{"open", func() (Frame, error) { return OpenFrame("sync-1", filter, []byte{0x61, 0}) }, `["NEG-OPEN","sync-1",{"kinds":[1],"#p":["abc"]},"6100"]`},
		{"message", func() (Frame, error) { return MessageFrame("sync-1", []byte{0x61}) }, `["NEG-MSG","sync-1","61"]`},
		{"error", func() (Frame, error) { return ErrorFrame("sync-1", "blocked: too many records", &maximum) }, `["NEG-ERR","sync-1","blocked: too many records",5000]`},
		{"close", func() (Frame, error) { return CloseFrame("sync-1") }, `["NEG-CLOSE","sync-1"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame, err := tc.make()
			if err != nil {
				t.Fatal(err)
			}
			raw, err := EncodeFrame(frame)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != tc.want {
				t.Fatalf("wire = %s, want %s", raw, tc.want)
			}
			if strings.Contains(string(raw), "NEG-ERROR") {
				t.Fatalf("obsolete command emitted: %s", raw)
			}
			decoded, err := DecodeFrame(raw)
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Kind != frame.Kind || decoded.ID != frame.ID || !reflect.DeepEqual(decoded.Message, frame.Message) || decoded.Reason != frame.Reason {
				t.Fatalf("decoded %#v, want %#v", decoded, frame)
			}
		})
	}
}

func TestDecodeFrameRejectsMalformedWire(t *testing.T) {
	invalid := []string{
		`null`, `[]`, `["NEG-ERROR","x","blocked: no"]`,
		`["NEG-CLOSE",""]`, `["NEG-CLOSE","x",1]`,
		`["NEG-MSG","x",""]`, `["NEG-MSG","x","6"]`, `["NEG-MSG","x","6A"]`, `["NEG-MSG","x","zz"]`, `["NEG-MSG","x","62"]`,
		`["NEG-OPEN","x",null,"61"]`, `["NEG-OPEN","x",[],"61"]`,
		`["NEG-ERR","x",""]`, `["NEG-ERR","x","blocked: no",-1]`, `["NEG-ERR","x","blocked: no",1.5]`, `["NEG-ERR","x","blocked: no","1"]`,
		`["NEG-CLOSE","x"] true`,
	}
	for _, raw := range invalid {
		if _, err := DecodeFrame([]byte(raw)); err == nil {
			t.Errorf("DecodeFrame accepted %s", raw)
		}
	}
	_, err := DecodeFrame([]byte(`["NEG-MSG","x","62"]`))
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("wrong-version error = %v", err)
	}
}

func TestNegentropySessionReconcilesDeterministically(t *testing.T) {
	id := func(value byte) nostr.ID {
		var out nostr.ID
		out[31] = value
		return out
	}
	client, err := NewSession([]Record{{10, id(1)}, {20, id(2)}, {20, id(2)}}, SessionOptions{Initiator: true, TrackLocalOnly: true, TrackRemoteOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewSession([]Record{{10, id(1)}, {30, id(3)}}, SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	message, err := client.Start()
	if err != nil {
		t.Fatal(err)
	}
	var localOnly, remoteOnly []nostr.ID
	for rounds := 0; ; rounds++ {
		if rounds > 20 {
			t.Fatal("reconciliation did not finish")
		}
		serverStep, err := server.Reconcile(message)
		if err != nil {
			t.Fatal(err)
		}
		if len(serverStep.Next) == 0 {
			t.Fatal("server returned no response")
		}
		clientStep, err := client.Reconcile(serverStep.Next)
		if err != nil {
			t.Fatal(err)
		}
		localOnly = appendUnique(localOnly, clientStep.LocalOnly...)
		remoteOnly = appendUnique(remoteOnly, clientStep.RemoteOnly...)
		if clientStep.Done {
			break
		}
		message = clientStep.Next
	}
	if !reflect.DeepEqual(localOnly, []nostr.ID{id(2)}) {
		t.Fatalf("local only = %x", localOnly)
	}
	if !reflect.DeepEqual(remoteOnly, []nostr.ID{id(3)}) {
		t.Fatalf("remote only = %x", remoteOnly)
	}
}

func TestSessionValidatesStateAndVersion(t *testing.T) {
	s, _ := NewSession(nil, SessionOptions{Initiator: true})
	if _, err := s.Reconcile([]byte{ProtocolVersion}); err == nil {
		t.Fatal("reconcile before start accepted")
	}
	if _, err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(); err == nil {
		t.Fatal("second start accepted")
	}
	if _, err := s.Reconcile([]byte{0x62}); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("wrong version error = %v", err)
	}
	if _, err := NewSession([]Record{{Timestamp: -1}}, SessionOptions{}); err == nil {
		t.Fatal("negative timestamp accepted")
	}
}

func TestProtocolVersionGoldenHex(t *testing.T) {
	client, _ := NewSession(nil, SessionOptions{Initiator: true})
	message, _ := client.Start()
	if !strings.HasPrefix(hex.EncodeToString(message), "61") {
		t.Fatalf("message = %x", message)
	}
}
