package methods

import (
	"strings"
	"testing"
)

func TestChannelsSendRequestNormalizeTargets(t *testing.T) {
	eventID := strings.Repeat("a", 64)
	rootID := strings.Repeat("b", 64)
	pubkey := strings.Repeat("c", 64)
	tests := []struct {
		name    string
		request ChannelsSendRequest
		wantErr bool
	}{
		{
			name:    "legacy targetless",
			request: ChannelsSendRequest{ChannelID: "room", Text: "hello"},
		},
		{
			name: "valid reply target normalizes hex",
			request: ChannelsSendRequest{
				ChannelID: "room", Text: "ok", ReplyToEventID: strings.ToUpper(eventID), TargetPubkey: strings.ToUpper(pubkey), TargetKind: 9,
			},
		},
		{
			name: "valid thread target",
			request: ChannelsSendRequest{
				ChannelID: "room", Text: "status", ThreadRootEventID: rootID, TargetPubkey: pubkey,
			},
		},
		{
			name:    "missing target author",
			request: ChannelsSendRequest{ChannelID: "room", Text: "ok", ReplyToEventID: eventID},
			wantErr: true,
		},
		{
			name:    "malformed event id",
			request: ChannelsSendRequest{ChannelID: "room", Text: "ok", ReplyToEventID: "bad", TargetPubkey: pubkey},
			wantErr: true,
		},
		{
			name:    "orphan target metadata",
			request: ChannelsSendRequest{ChannelID: "room", Text: "ok", TargetPubkey: pubkey},
			wantErr: true,
		},
		{
			name:    "negative target kind",
			request: ChannelsSendRequest{ChannelID: "room", Text: "ok", ReplyToEventID: eventID, TargetPubkey: pubkey, TargetKind: -1},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.request.Normalize()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Normalize() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && got.ReplyToEventID != strings.ToLower(strings.TrimSpace(tt.request.ReplyToEventID)) {
				t.Fatalf("reply target = %q", got.ReplyToEventID)
			}
		})
	}
}
