package toolrepair

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
)

// ToolDefinition is the minimal provider tool shape needed for promotion.
type ToolDefinition struct {
	Name string
}

// ToolCall is the promoted, provider-agnostic tool call shape.
type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

type candidate struct {
	start int
	end   int
	text  string
}

type payload struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	Args      map[string]any `json:"args"`
}

var (
	fencedBlockRE = regexp.MustCompile("(?is)```(?:json)?\\s*(.*?)\\s*```")
	xmlToolRE     = regexp.MustCompile("(?is)<tool_call>\\s*(.*?)\\s*</tool_call>")
)

// Promote detects model-leaked plain-text tool calls, promotes them to ToolCall
// values when every payload names a known tool, and removes the leaked payloads
// from the visible assistant text.
func Promote(text string, defs []ToolDefinition) (cleanedText string, calls []ToolCall, repaired bool) {
	allowed := allowedTools(defs)
	if len(allowed) == 0 || strings.TrimSpace(text) == "" {
		return text, nil, false
	}

	candidates := collectCandidates(text)
	if len(candidates) == 0 {
		return text, nil, false
	}

	var promoted []ToolCall
	var accepted []candidate
	for _, c := range candidates {
		parsed, ok := parsePayloads(c.text)
		if !ok || len(parsed) == 0 {
			continue
		}
		var next []ToolCall
		valid := true
		for _, p := range parsed {
			if !allowed[p.Name] {
				valid = false
				break
			}
			args := p.Arguments
			if args == nil {
				args = p.Args
			}
			if args == nil {
				args = map[string]any{}
			}
			next = append(next, ToolCall{ID: newID(), Name: p.Name, Args: args})
		}
		if !valid {
			continue
		}
		accepted = append(accepted, c)
		promoted = append(promoted, next...)
	}
	if len(promoted) == 0 {
		return text, nil, false
	}
	return scrub(text, accepted), promoted, true
}

func allowedTools(defs []ToolDefinition) map[string]bool {
	out := make(map[string]bool, len(defs))
	for _, d := range defs {
		name := strings.TrimSpace(d.Name)
		if name != "" {
			out[name] = true
		}
	}
	return out
}

func collectCandidates(text string) []candidate {
	var out []candidate
	for _, loc := range fencedBlockRE.FindAllStringSubmatchIndex(text, -1) {
		if len(loc) >= 4 {
			out = append(out, candidate{start: loc[0], end: loc[1], text: text[loc[2]:loc[3]]})
		}
	}
	for _, loc := range xmlToolRE.FindAllStringSubmatchIndex(text, -1) {
		if len(loc) >= 4 {
			out = append(out, candidate{start: loc[0], end: loc[1], text: text[loc[2]:loc[3]]})
		}
	}
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		start := strings.Index(text, trimmed)
		out = append(out, candidate{start: start, end: start + len(trimmed), text: trimmed})
	}
	return dedupeCandidates(out)
}

func dedupeCandidates(in []candidate) []candidate {
	seen := map[[2]int]bool{}
	out := make([]candidate, 0, len(in))
	for _, c := range in {
		key := [2]int{c.start, c.end}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}

func parsePayloads(raw string) ([]payload, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, false
	}
	var one payload
	if err := json.Unmarshal([]byte(trimmed), &one); err == nil && one.Name != "" {
		return []payload{one}, true
	}
	var many []payload
	if err := json.Unmarshal([]byte(trimmed), &many); err == nil {
		for _, p := range many {
			if p.Name == "" {
				return nil, false
			}
		}
		return many, len(many) > 0
	}
	return nil, false
}

func scrub(text string, blocks []candidate) string {
	if len(blocks) == 0 {
		return text
	}
	var b strings.Builder
	cursor := 0
	for _, c := range blocks {
		if c.start < cursor {
			continue
		}
		b.WriteString(text[cursor:c.start])
		cursor = c.end
	}
	b.WriteString(text[cursor:])
	return strings.TrimSpace(collapseBlankLines(b.String()))
}

func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		isBlank := strings.TrimSpace(line) == ""
		if isBlank && blank {
			continue
		}
		out = append(out, line)
		blank = isBlank
	}
	return strings.Join(out, "\n")
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "toolrepair"
	}
	return "toolrepair_" + hex.EncodeToString(b[:])
}
