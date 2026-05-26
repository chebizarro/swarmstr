package toolloop

import (
	"strings"
	"testing"
)

func TestCompactToolOutputPreservesEdgesAndErrors(t *testing.T) {
	out := "first line\n" + strings.Repeat("noise\n", 400) + "ERROR: relay rejected OK false\n" + strings.Repeat("more\n", 400) + "last line"
	res := CompactToolOutput("bash", out, CompactionConfig{Enabled: true, DefaultMaxBytes: 1200, FirstLines: 2, LastLines: 2, ErrorContextLines: 1})
	if !res.Compacted {
		t.Fatal("expected compacted")
	}
	if !strings.Contains(res.Output, "first line") || !strings.Contains(res.Output, "last line") || !strings.Contains(res.Output, "ERROR: relay rejected") {
		t.Fatalf("missing preserved content: %s", res.Output)
	}
	if res.CompactedBytes >= res.OriginalBytes {
		t.Fatal("did not shrink")
	}
}

func TestCompactToolOutputDisabled(t *testing.T) {
	res := CompactToolOutput("bash", strings.Repeat("x", 2000), CompactionConfig{Enabled: false})
	if res.Compacted {
		t.Fatal("unexpected compaction")
	}
}
