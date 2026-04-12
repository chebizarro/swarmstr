package config

import (
	"testing"
)

func TestToInt(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want int
		ok   bool
	}{
		{"int", 42, 42, true},
		{"int8", int8(8), 8, true},
		{"int16", int16(16), 16, true},
		{"int32", int32(32), 32, true},
		{"int64", int64(64), 64, true},
		{"uint", uint(10), 10, true},
		{"uint8", uint8(8), 8, true},
		{"uint16", uint16(16), 16, true},
		{"uint32", uint32(32), 32, true},
		{"uint64", uint64(64), 64, true},
		{"float32", float32(3.14), 3, true},
		{"float64", float64(2.7), 2, true},
		{"string", "not a number", 0, false},
		{"nil", nil, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toInt(tc.in)
			if ok != tc.ok {
				t.Errorf("ok: expected %v, got %v", tc.ok, ok)
			}
			if got != tc.want {
				t.Errorf("value: expected %d, got %d", tc.want, got)
			}
		})
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want float64
		ok   bool
	}{
		{"float64", float64(3.14), 3.14, true},
		{"float32", float32(2.5), 2.5, true},
		{"int", 42, 42.0, true},
		{"int64", int64(100), 100.0, true},
		{"string", "x", 0, false},
		{"nil", nil, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toFloat64(tc.in)
			if ok != tc.ok {
				t.Errorf("ok: expected %v, got %v", tc.ok, ok)
			}
			if tc.ok && got != tc.want {
				t.Errorf("value: expected %f, got %f", tc.want, got)
			}
		})
	}
}

func TestToStringSlice(t *testing.T) {
	// []string passthrough
	got := toStringSlice([]string{"a", "b"})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("expected [a b], got %v", got)
	}

	// []any with strings
	got = toStringSlice([]any{"hello", "  world  ", "", 123})
	if len(got) != 2 || got[0] != "hello" || got[1] != "world" {
		t.Errorf("expected [hello world], got %v", got)
	}

	// nil
	got = toStringSlice(nil)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}

	// unsupported type
	got = toStringSlice(42)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestDefaultBootstrapPath(t *testing.T) {
	p, _ := DefaultBootstrapPath()
	if p == "" {
		t.Fatal("path should not be empty")
	}
}
