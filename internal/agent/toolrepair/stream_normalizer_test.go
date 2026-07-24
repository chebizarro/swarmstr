package toolrepair

import (
	"strings"
	"testing"
)

func collectNormalized(t *testing.T, input string, cuts []int) StreamOutput {
	t.Helper()
	n := NewStreamNormalizer([]ToolDefinition{{Name: "lookup"}})
	var out StreamOutput
	last := 0
	for _, cut := range cuts {
		part := n.Feed(input[last:cut])
		out.Text += part.Text
		out.Calls = append(out.Calls, part.Calls...)
		out.Scrubbed = out.Scrubbed || part.Scrubbed
		last = cut
	}
	part := n.Feed(input[last:])
	out.Text += part.Text
	out.Calls = append(out.Calls, part.Calls...)
	out.Scrubbed = out.Scrubbed || part.Scrubbed
	part = n.Flush()
	out.Text += part.Text
	out.Calls = append(out.Calls, part.Calls...)
	out.Scrubbed = out.Scrubbed || part.Scrubbed
	return out
}

func TestStreamNormalizerPromotesAcrossEveryChunkBoundary(t *testing.T) {
	inputs := []string{
		`{"name":"lookup","arguments":{"q":"x"}}`,
		"```json\n{\"name\":\"lookup\",\"args\":{\"q\":\"x\"}}\n```",
		"<tool_call>{\"name\":\"lookup\",\"arguments\":{\"q\":\"x\"}}</tool_call>",
	}
	for _, input := range inputs {
		for cut := 0; cut <= len(input); cut++ {
			out := collectNormalized(t, input, []int{cut})
			if out.Text != "" || len(out.Calls) != 1 || out.Calls[0].Name != "lookup" || !out.Scrubbed {
				t.Fatalf("input=%q cut=%d output=%#v", input, cut, out)
			}
		}
	}
}

func TestStreamNormalizerSuppressesPartialCandidate(t *testing.T) {
	n := NewStreamNormalizer([]ToolDefinition{{Name: "lookup"}})
	if out := n.Feed(`{"name":"look`); out.Text != "" || len(out.Calls) != 0 {
		t.Fatalf("partial leaked: %#v", out)
	}
	out := n.Feed(`up","arguments":{"q":"x"}}`)
	if out.Text != "" || len(out.Calls) != 1 || out.Calls[0].Args["q"] != "x" {
		t.Fatalf("completion=%#v", out)
	}
}

func TestStreamNormalizerReplaysFalsePositives(t *testing.T) {
	input := `{"hello":"world"}`
	out := collectNormalized(t, input, []int{3, 8})
	if out.Text != input || len(out.Calls) != 0 || out.Scrubbed {
		t.Fatalf("output=%#v", out)
	}
}

func TestStreamNormalizerScrubsIncompleteKnownToolOnFlush(t *testing.T) {
	n := NewStreamNormalizer([]ToolDefinition{{Name: "lookup"}})
	if out := n.Feed("<tool_call>{\"name\":\"lookup\",\"arguments\":{"); out.Text != "" {
		t.Fatalf("partial leaked: %#v", out)
	}
	out := n.Flush()
	if out.Text != "" || !out.Scrubbed || len(out.Calls) != 0 {
		t.Fatalf("flush=%#v", out)
	}
}

func TestStreamNormalizerBoundsUnresolvedCandidates(t *testing.T) {
	huge := strings.Repeat("x", maxSuppressedStreamBytes+1)
	known := NewStreamNormalizer([]ToolDefinition{{Name: "lookup"}})
	out := known.Feed(`{"name":"lookup","arguments":{"data":"` + huge)
	if out.Text != "" || !out.Scrubbed {
		t.Fatalf("known overflow=%#v", out)
	}
	if known.buffer != "" {
		t.Fatalf("known candidate retained %d bytes", len(known.buffer))
	}

	unknownInput := `{"name":"unknown","arguments":{"data":"` + huge
	unknown := NewStreamNormalizer([]ToolDefinition{{Name: "lookup"}})
	out = unknown.Feed(unknownInput)
	if out.Text != unknownInput || out.Scrubbed || unknown.buffer != "" {
		t.Fatalf("unknown overflow text=%d scrubbed=%v retained=%d", len(out.Text), out.Scrubbed, len(unknown.buffer))
	}
}

func TestStreamNormalizerPreservesVisiblePrefixAndSuffix(t *testing.T) {
	input := "Before\n{\"name\":\"lookup\",\"arguments\":{}}\nAfter"
	out := collectNormalized(t, input, []int{4, 11, len(input) - 3})
	if strings.Contains(out.Text, "lookup") || !strings.Contains(out.Text, "Before") || !strings.Contains(out.Text, "After") || len(out.Calls) != 1 {
		t.Fatalf("output=%#v", out)
	}
}

func FuzzStreamNormalizerNeverLeaksCompleteKnownCall(f *testing.F) {
	input := `{"name":"lookup","arguments":{"q":"x"}}`
	for i := 0; i <= len(input); i++ {
		f.Add(i)
	}
	f.Fuzz(func(t *testing.T, cut int) {
		if cut < 0 {
			cut = -cut
		}
		cut %= len(input) + 1
		out := collectNormalized(t, input, []int{cut})
		if strings.Contains(out.Text, `"name":"lookup"`) || len(out.Calls) != 1 {
			t.Fatalf("cut=%d output=%#v", cut, out)
		}
	})
}
