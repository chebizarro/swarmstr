package runtime

import (
	"strings"
	"testing"
)

func TestNormalizeOutboundDMText(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "simple text",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "trims leading whitespace",
			input: "  hello",
			want:  "hello",
		},
		{
			name:  "trims trailing whitespace",
			input: "hello  ",
			want:  "hello",
		},
		{
			name:  "trims both sides",
			input: "  hello world  ",
			want:  "hello world",
		},
		{
			name:  "preserves internal whitespace",
			input: "hello   world",
			want:  "hello   world",
		},
		{
			name:    "rejects empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "rejects whitespace only",
			input:   "   ",
			wantErr: true,
		},
		{
			name:  "accepts long text",
			input: strings.Repeat("x", 5000),
			want:  strings.Repeat("x", 5000),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeOutboundDMText(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("normalizeOutboundDMText() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("normalizeOutboundDMText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateNIP04OutboundDMText(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "simple text",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "trims whitespace",
			input: "  hello  ",
			want:  "hello",
		},
		{
			name:    "rejects empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:  "accepts exactly 2800 runes",
			input: strings.Repeat("x", 2800),
			want:  strings.Repeat("x", 2800),
		},
		{
			name:    "rejects 2801 runes",
			input:   strings.Repeat("x", 2801),
			wantErr: true,
		},
		{
			name:    "rejects long text",
			input:   strings.Repeat("x", 5000),
			wantErr: true,
		},
		{
			name:  "unicode: accepts 2800 runes",
			input: strings.Repeat("🔥", 2800),
			want:  strings.Repeat("🔥", 2800),
		},
		{
			name:    "unicode: rejects 2801 runes",
			input:   strings.Repeat("🔥", 2801),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateNIP04OutboundDMText(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateNIP04OutboundDMText() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				if len(got) < 100 && len(tt.want) < 100 {
					t.Errorf("validateNIP04OutboundDMText() = %q, want %q", got, tt.want)
				} else {
					t.Errorf("validateNIP04OutboundDMText() length = %d, want %d", len(got), len(tt.want))
				}
			}
		})
	}
}
