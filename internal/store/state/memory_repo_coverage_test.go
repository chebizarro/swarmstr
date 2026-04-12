package state

import (
	"testing"
)

func TestNormalizeToken(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"  Hello World  ", "hello world"},
		{"UPPER", "upper"},
		{"\t\ttabbed\n\nnewline", "tabbed newline"},
		{"  multiple   spaces  ", "multiple spaces"},
		{"", ""},
		{"   ", ""},
	}
	for _, tc := range tests {
		got := normalizeToken(tc.in)
		if got != tc.want {
			t.Errorf("normalizeToken(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
