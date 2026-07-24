package toolrepair

import "testing"

func defs() []ToolDefinition { return []ToolDefinition{{Name: "read_file"}, {Name: "grep"}} }

func TestPromoteFencedJSON(t *testing.T) {
	text := "I'll inspect that.\n```json\n{\"name\":\"read_file\",\"arguments\":{\"path\":\"main.go\"}}\n```"
	cleaned, calls, repaired := Promote(text, defs())
	if !repaired || len(calls) != 1 {
		t.Fatalf("expected one repaired call, repaired=%v calls=%d", repaired, len(calls))
	}
	if calls[0].Name != "read_file" || calls[0].Args["path"] != "main.go" {
		t.Fatalf("unexpected call: %+v", calls[0])
	}
	if cleaned != "I'll inspect that." {
		t.Fatalf("unexpected cleaned text: %q", cleaned)
	}
}

func TestPromoteNoFalsePositiveOrdinaryProseAndCode(t *testing.T) {
	text := "Here is normal JSON code:\n```json\n{\"name\":\"not_a_tool\",\"arguments\":{}}\n```\nUse it as an example."
	cleaned, calls, repaired := Promote(text, defs())
	if repaired || len(calls) != 0 || cleaned != text {
		t.Fatalf("expected no repair, repaired=%v calls=%d cleaned=%q", repaired, len(calls), cleaned)
	}
}

func TestPromoteMultipleCalls(t *testing.T) {
	text := "```json\n[{\"name\":\"read_file\",\"arguments\":{\"path\":\"a.go\"}},{\"name\":\"grep\",\"arguments\":{\"pattern\":\"TODO\"}}]\n```"
	cleaned, calls, repaired := Promote(text, defs())
	if !repaired || len(calls) != 2 {
		t.Fatalf("expected two repaired calls, repaired=%v calls=%d", repaired, len(calls))
	}
	if cleaned != "" {
		t.Fatalf("expected scrubbed text, got %q", cleaned)
	}
	if calls[0].Name != "read_file" || calls[1].Name != "grep" {
		t.Fatalf("unexpected calls: %+v", calls)
	}
}

func TestPromoteOpenClawRepairSyntaxes(t *testing.T) {
	tests := []struct {
		name string
		text string
		want map[string]any
	}{
		{name: "tool bracket", text: `[tool:read_file] {"path":"main.go"}<|call|>`, want: map[string]any{"path": "main.go"}},
		{name: "named bracket", text: "[read_file]\n{\"path\":\"main.go\"}\n[/read_file]", want: map[string]any{"path": "main.go"}},
		{name: "harmony", text: `<|channel|>analysis to=read_file code <|message|>{"path":"main.go"}<|call|>`, want: map[string]any{"path": "main.go"}},
		{name: "xml function", text: `<function=read_file><parameter=path>main.go</parameter></function>`, want: map[string]any{"path": "main.go"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cleaned, calls, repaired := Promote("Before\n"+tc.text+"\nAfter", defs())
			if !repaired || len(calls) != 1 || calls[0].Name != "read_file" || calls[0].Args["path"] != tc.want["path"] {
				t.Fatalf("repair=%v calls=%#v", repaired, calls)
			}
			if cleaned != "Before\n\nAfter" {
				t.Fatalf("cleaned=%q", cleaned)
			}
		})
	}
}

func TestPromoteRepairsStringEncodedArguments(t *testing.T) {
	text := "<tool_call>{\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"main.go\\\"}\"}}</tool_call>"
	cleaned, calls, repaired := Promote(text, defs())
	if !repaired || cleaned != "" || len(calls) != 1 || calls[0].Args["path"] != "main.go" {
		t.Fatalf("cleaned=%q repaired=%v calls=%#v", cleaned, repaired, calls)
	}
}

func TestPromoteRepairSyntaxFalsePositivesReplay(t *testing.T) {
	for _, text := range []string{
		"Use [read_file] inline as documentation.",
		"analysis is ordinary prose",
		"[unknown]\n{\"path\":\"main.go\"}\n[/unknown]",
		"[read_file]\n{not-json}\n[/read_file]",
		"<function=read_file><parameter=path>unterminated",
	} {
		cleaned, calls, repaired := Promote(text, defs())
		if repaired || len(calls) != 0 || cleaned != text {
			t.Fatalf("text=%q cleaned=%q repaired=%v calls=%#v", text, cleaned, repaired, calls)
		}
	}
}

func TestPromoteUnknownToolRejected(t *testing.T) {
	text := "<tool_call>{\"name\":\"shell\",\"arguments\":{\"cmd\":\"rm -rf /\"}}</tool_call>"
	cleaned, calls, repaired := Promote(text, defs())
	if repaired || len(calls) != 0 || cleaned != text {
		t.Fatalf("expected unknown tool rejection, repaired=%v calls=%d cleaned=%q", repaired, len(calls), cleaned)
	}
}

func TestPromoteInlineJSON(t *testing.T) {
	text := "{\"name\":\"read_file\",\"arguments\":{\"path\":\"inline.go\"}}"
	cleaned, calls, repaired := Promote(text, defs())
	if !repaired || len(calls) != 1 || cleaned != "" {
		t.Fatalf("expected inline promotion, repaired=%v calls=%d cleaned=%q", repaired, len(calls), cleaned)
	}
}
