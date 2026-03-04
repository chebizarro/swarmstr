package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateUTF8ByBytes_DoesNotSplitRunes(t *testing.T) {
	input := strings.Repeat("🙂", 5000)
	out := truncateUTF8ByBytes(input, 16*1024)
	if len(out) > 16*1024 {
		t.Fatalf("len(out) = %d, want <= %d", len(out), 16*1024)
	}
	if !utf8.ValidString(out) {
		t.Fatal("output is not valid UTF-8")
	}
}

func TestTruncateUTF8ByBytes_PreservesASCIIPrefix(t *testing.T) {
	input := "hello world"
	out := truncateUTF8ByBytes(input, 5)
	if out != "hello" {
		t.Fatalf("out = %q, want %q", out, "hello")
	}
}
