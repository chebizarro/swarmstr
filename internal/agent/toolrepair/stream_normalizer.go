package toolrepair

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxSuppressedStreamBytes = 256 << 10

// StreamOutput is the visible text and promoted calls made stable by one Feed or Flush.
type StreamOutput struct {
	Text     string
	Calls    []ToolCall
	Scrubbed bool
}

// StreamNormalizer incrementally suppresses leaked plain-text tool calls until
// they can be promoted or proven to be ordinary text. It is synchronous and
// completion is driven only by Feed and Flush.
type StreamNormalizer struct {
	allowed     map[string]bool
	buffer      string
	atLineStart bool
}

func NewStreamNormalizer(defs []ToolDefinition) *StreamNormalizer {
	return &StreamNormalizer{allowed: allowedTools(defs), atLineStart: true}
}

// Feed consumes the next provider text delta. Text in an unresolved candidate
// is withheld; ordinary text and false positives are returned in wire order.
func (n *StreamNormalizer) Feed(chunk string) StreamOutput {
	if n == nil || chunk == "" {
		return StreamOutput{}
	}
	n.buffer += chunk
	return n.process(false)
}

// Flush resolves the final pending candidate. Incomplete candidates naming a
// configured tool are scrubbed; other incomplete prefixes are replayed as text.
func (n *StreamNormalizer) Flush() StreamOutput {
	if n == nil {
		return StreamOutput{}
	}
	return n.process(true)
}

func (n *StreamNormalizer) process(final bool) StreamOutput {
	var out StreamOutput
	for n.buffer != "" {
		start, kind, found := findStreamCandidateStart(n.buffer, n.atLineStart)
		if !found {
			n.emit(&out, n.buffer)
			n.buffer = ""
			break
		}
		if start > 0 {
			n.emit(&out, n.buffer[:start])
			n.buffer = n.buffer[start:]
		}
		end, payloadText, parsed, state := streamCandidate(n.buffer, kind, final)
		if state == repairPrefix {
			if !final && len(n.buffer) <= maxSuppressedStreamBytes {
				break
			}
			if incompleteNamesAllowedTool(n.buffer, n.allowed) {
				out.Scrubbed = true
				n.buffer = ""
				break
			}
			n.emit(&out, n.buffer)
			n.buffer = ""
			break
		}
		if state == repairInvalid || end <= 0 || end > len(n.buffer) {
			n.emit(&out, n.buffer)
			n.buffer = ""
			break
		}
		candidateText := n.buffer[:end]
		ok := len(parsed) > 0
		if !ok {
			parsed, ok = parsePayloads(payloadText)
		}
		calls, valid := n.promoteParsed(parsed, ok)
		if valid {
			out.Calls = append(out.Calls, calls...)
			out.Scrubbed = true
		} else {
			n.emit(&out, candidateText)
		}
		n.buffer = n.buffer[end:]
	}
	return out
}

func (n *StreamNormalizer) promoteParsed(parsed []payload, ok bool) ([]ToolCall, bool) {
	if !ok || len(parsed) == 0 || len(n.allowed) == 0 {
		return nil, false
	}
	calls := make([]ToolCall, 0, len(parsed))
	for _, p := range parsed {
		if !n.allowed[p.Name] {
			return nil, false
		}
		args := p.Arguments
		if args == nil {
			args = p.Args
		}
		if args == nil {
			args = map[string]any{}
		}
		calls = append(calls, ToolCall{ID: newID(), Name: p.Name, Args: args})
	}
	return calls, true
}

func (n *StreamNormalizer) emit(out *StreamOutput, text string) {
	if text == "" {
		return
	}
	out.Text += text
	n.consume(text)
}

func (n *StreamNormalizer) consume(text string) {
	for _, r := range text {
		switch r {
		case '\n', '\r':
			n.atLineStart = true
		default:
			if n.atLineStart && !unicode.IsSpace(r) {
				n.atLineStart = false
			}
		}
	}
}

type streamCandidateKind uint8

const (
	streamCandidatePrefix streamCandidateKind = iota
	streamCandidateJSON
	streamCandidateFence
	streamCandidateXML
	streamCandidateRepair
)

var streamMarkers = []struct {
	text string
	kind streamCandidateKind
}{
	{text: "```json", kind: streamCandidateFence},
	{text: "```", kind: streamCandidateFence},
	{text: "<tool_call>", kind: streamCandidateXML},
	{text: "{", kind: streamCandidateJSON},
	{text: "[", kind: streamCandidateJSON},
}

func findStreamCandidateStart(text string, startsAtLineStart bool) (int, streamCandidateKind, bool) {
	lineStart := startsAtLineStart
	for i := 0; i < len(text); {
		if lineStart {
			j := i
			for j < len(text) {
				r, size := utf8.DecodeRuneInString(text[j:])
				if r == '\n' || r == '\r' || !unicode.IsSpace(r) {
					break
				}
				j += size
			}
			rest := text[j:]
			if possibleRepairSyntax(rest) {
				return i, streamCandidateRepair, true
			}
			for _, marker := range streamMarkers {
				if strings.HasPrefix(rest, marker.text) {
					return i, marker.kind, true
				}
				if rest != "" && strings.HasPrefix(marker.text, rest) {
					return i, streamCandidatePrefix, true
				}
			}
			if rest == "" { // retain indentation across a chunk boundary
				return i, streamCandidatePrefix, true
			}
		}
		if text[i] == '\n' || text[i] == '\r' {
			lineStart = true
		} else if !unicode.IsSpace(rune(text[i])) {
			lineStart = false
		}
		i++
	}
	return 0, 0, false
}

func streamCandidate(text string, kind streamCandidateKind, final bool) (end int, payloadText string, parsed []payload, state repairScanState) {
	trimmedStart := len(text) - len(strings.TrimLeftFunc(text, unicode.IsSpace))
	body := text[trimmedStart:]
	if kind == streamCandidateRepair || possibleRepairSyntax(body) {
		scan := scanRepairSyntax(body, final)
		if scan.state == repairComplete {
			return trimmedStart + scan.end, "", []payload{{Name: scan.name, Arguments: scan.args}}, repairComplete
		}
		return 0, "", nil, scan.state
	}
	switch kind {
	case streamCandidatePrefix:
		for _, marker := range streamMarkers {
			if strings.HasPrefix(body, marker.text) {
				return streamCandidate(text, marker.kind, final)
			}
		}
		return 0, "", nil, repairPrefix
	case streamCandidateFence:
		openerLen := 3
		if strings.HasPrefix(strings.ToLower(body), "```json") {
			openerLen = len("```json")
		}
		if closeAt := strings.Index(body[openerLen:], "```"); closeAt >= 0 {
			payloadStart := openerLen
			payloadEnd := openerLen + closeAt
			return trimmedStart + payloadEnd + 3, body[payloadStart:payloadEnd], nil, repairComplete
		}
	case streamCandidateXML:
		const open, close = "<tool_call>", "</tool_call>"
		if len(body) >= len(open) {
			if closeAt := indexFold(body[len(open):], close); closeAt >= 0 {
				payloadEnd := len(open) + closeAt
				return trimmedStart + payloadEnd + len(close), body[len(open):payloadEnd], nil, repairComplete
			}
		}
	case streamCandidateJSON:
		if jsonEnd := completeJSONValueEnd(body); jsonEnd > 0 {
			return trimmedStart + jsonEnd, body[:jsonEnd], nil, repairComplete
		}
	}
	return 0, "", nil, repairPrefix
}

func completeJSONValueEnd(text string) int {
	if text == "" || (text[0] != '{' && text[0] != '[') {
		return 0
	}
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return i + 1
			}
			if depth < 0 {
				return i + 1
			}
		}
	}
	return 0
}

func incompleteNamesAllowedTool(text string, allowed map[string]bool) bool {
	trimmed := strings.TrimLeftFunc(text, unicode.IsSpace)
	if possibleRepairSyntax(trimmed) {
		scan := scanRepairSyntax(trimmed, false)
		if scan.name != "" && allowed[scan.name] {
			return true
		}
	}
	lower := strings.ToLower(text)
	for name := range allowed {
		quoted := string(rune(34)) + strings.ToLower(name) + string(rune(34))
		if strings.Contains(lower, quoted) || strings.Contains(lower, ">"+strings.ToLower(name)+"<") {
			return true
		}
	}
	return false
}

func indexFold(s, substr string) int {
	return strings.Index(strings.ToLower(s), strings.ToLower(substr))
}
