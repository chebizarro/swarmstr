package main

import (
	"testing"
)

// ── csvListFlag ───────────────────────────────────────────────────────────────

func TestCsvListFlag_Set(t *testing.T) {
	var f csvListFlag
	if err := f.Set("a, b ,c"); err != nil {
		t.Fatal(err)
	}
	if len(f) != 3 || f[0] != "a" || f[1] != "b" || f[2] != "c" {
		t.Fatalf("got %v", f)
	}
}

func TestCsvListFlag_Set_SkipsEmpty(t *testing.T) {
	var f csvListFlag
	if err := f.Set(",a,,b,"); err != nil {
		t.Fatal(err)
	}
	if len(f) != 2 {
		t.Fatalf("expected 2, got %v", f)
	}
}

func TestCsvListFlag_String(t *testing.T) {
	f := csvListFlag{"a", "b"}
	if f.String() != "a,b" {
		t.Fatalf("got %q", f.String())
	}
	var nilF *csvListFlag
	if nilF.String() != "" {
		t.Fatal("nil should return empty string")
	}
}

// ── stringListFlag ────────────────────────────────────────────────────────────

func TestStringListFlag_Set(t *testing.T) {
	var f stringListFlag
	f.Set("a")
	f.Set("b")
	if len(f) != 2 || f[0] != "a" || f[1] != "b" {
		t.Fatalf("got %v", f)
	}
}

func TestStringListFlag_String(t *testing.T) {
	f := stringListFlag{"x", "y"}
	if f.String() != "x,y" {
		t.Fatalf("got %q", f.String())
	}
}

// ── keyValueFlag ──────────────────────────────────────────────────────────────

func TestKeyValueFlag_Set(t *testing.T) {
	var f keyValueFlag
	if err := f.Set("key=value"); err != nil {
		t.Fatal(err)
	}
	if f["key"] != "value" {
		t.Fatalf("got %v", f)
	}
}

func TestKeyValueFlag_Set_MissingEquals(t *testing.T) {
	var f keyValueFlag
	if err := f.Set("noequals"); err == nil {
		t.Fatal("expected error")
	}
}

func TestKeyValueFlag_Set_EmptyKey(t *testing.T) {
	var f keyValueFlag
	if err := f.Set("=value"); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestKeyValueFlag_String(t *testing.T) {
	f := keyValueFlag{"b": "2", "a": "1"}
	if f.String() != "a=1,b=2" {
		t.Fatalf("got %q", f.String())
	}
	var nilF *keyValueFlag
	if nilF.String() != "" {
		t.Fatal("nil should return empty string")
	}
}

// ── parseObserveWait ──────────────────────────────────────────────────────────

func TestParseObserveWait_Empty(t *testing.T) {
	ms, err := parseObserveWait("")
	if err != nil || ms != 0 {
		t.Fatalf("got %d, %v", ms, err)
	}
}

func TestParseObserveWait_Digits(t *testing.T) {
	ms, err := parseObserveWait("500")
	if err != nil || ms != 500 {
		t.Fatalf("got %d, %v", ms, err)
	}
}

func TestParseObserveWait_Duration(t *testing.T) {
	ms, err := parseObserveWait("2s")
	if err != nil || ms != 2000 {
		t.Fatalf("got %d, %v", ms, err)
	}
}

func TestParseObserveWait_Negative(t *testing.T) {
	_, err := parseObserveWait("-100ms")
	if err == nil {
		t.Fatal("expected error for negative duration")
	}
}

func TestParseObserveWait_Invalid(t *testing.T) {
	_, err := parseObserveWait("abc")
	if err == nil {
		t.Fatal("expected error for invalid input")
	}
}

// ── digitsOnly ────────────────────────────────────────────────────────────────

func TestDigitsOnly(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"123", true},
		{"0", true},
		{"", false},
		{"12a", false},
		{"-1", false},
	}
	for _, tc := range tests {
		if got := digitsOnly(tc.in); got != tc.want {
			t.Errorf("digitsOnly(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// ── field accessor helpers ────────────────────────────────────────────────────

func TestStringFieldAny(t *testing.T) {
	m := map[string]any{"key": "val", "num": 42}
	if stringFieldAny(m, "key") != "val" {
		t.Error("expected val")
	}
	if stringFieldAny(m, "num") != "" {
		t.Error("expected empty for non-string")
	}
	if stringFieldAny(m, "missing") != "" {
		t.Error("expected empty for missing key")
	}
}

func TestBoolFieldAny(t *testing.T) {
	m := map[string]any{"flag": true, "str": "true"}
	if !boolFieldAny(m, "flag") {
		t.Error("expected true")
	}
	if boolFieldAny(m, "str") {
		t.Error("expected false for non-bool")
	}
}

func TestFloatFieldAny(t *testing.T) {
	m := map[string]any{"val": 3.14, "str": "3.14"}
	if floatFieldAny(m, "val") != 3.14 {
		t.Error("expected 3.14")
	}
	if floatFieldAny(m, "str") != 0 {
		t.Error("expected 0 for non-float")
	}
}

func TestIntFieldAny(t *testing.T) {
	m := map[string]any{
		"int":   42,
		"int64": int64(99),
		"float": float64(7),
		"str":   "5",
	}
	if intFieldAny(m, "int") != 42 {
		t.Error("expected 42")
	}
	if intFieldAny(m, "int64") != 99 {
		t.Error("expected 99")
	}
	if intFieldAny(m, "float") != 7 {
		t.Error("expected 7")
	}
	if intFieldAny(m, "str") != 0 {
		t.Error("expected 0 for string")
	}
}

func TestAnySlice(t *testing.T) {
	s := anySlice([]any{1, 2, 3})
	if len(s) != 3 {
		t.Fatalf("expected 3, got %d", len(s))
	}
	if anySlice("not a slice") != nil {
		t.Error("expected nil for non-slice")
	}
}

func TestStringSliceAny(t *testing.T) {
	// From []string
	got := stringSliceAny([]string{"a", "b"})
	if len(got) != 2 || got[0] != "a" {
		t.Fatalf("got %v", got)
	}
	// From []any
	got = stringSliceAny([]any{"x", "y", 42})
	if len(got) != 2 || got[0] != "x" {
		t.Fatalf("got %v", got)
	}
	// From nil
	if stringSliceAny(nil) != nil {
		t.Error("expected nil")
	}
}

func TestChannelStatusLabel(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]any
		want string
	}{
		{"explicit status", map[string]any{"status": "error"}, "error"},
		{"logged_out", map[string]any{"logged_out": true}, "logged_out"},
		{"connected", map[string]any{"connected": true}, "connected"},
		{"disconnected", map[string]any{}, "disconnected"},
	}
	for _, tc := range tests {
		if got := channelStatusLabel(tc.m); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
