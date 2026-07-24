package protocol

import (
	"errors"
	"testing"
)

func TestNegotiateProtocol(t *testing.T) {
	tests := []struct {
		name     string
		min, max int
		want     int
		wantErr  error
	}{
		{name: "v4 exact", min: 4, max: 4, want: 4},
		{name: "full overlap", min: 1, max: 4, want: 4},
		{name: "legacy overlap", min: 1, max: 3, want: 3},
		{name: "future max", min: 2, max: 99, want: 4},
		{name: "future only", min: 5, max: 99, wantErr: ErrUnsupportedProtocolRange},
		{name: "reversed", min: 3, max: 2, wantErr: ErrInvalidProtocolRange},
		{name: "zero", min: 0, max: 4, wantErr: ErrInvalidProtocolRange},
		{name: "negative", min: -1, max: 4, wantErr: ErrInvalidProtocolRange},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NegotiateProtocol(tc.min, tc.max)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want category %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("version = %d, want %d", got, tc.want)
			}
		})
	}
}
