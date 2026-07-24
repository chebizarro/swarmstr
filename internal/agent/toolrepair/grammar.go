package toolrepair

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	endToolRequestMarker = "[END_TOOL_REQUEST]"
	harmonyChannelMarker = "<|channel|>"
	harmonyMessageMarker = "<|message|>"
	harmonyCallMarker    = "<|call|>"
	maxRepairToolNameLen = 120
)

type repairScanState uint8

const (
	repairInvalid repairScanState = iota
	repairPrefix
	repairComplete
)

type repairScan struct {
	state repairScanState
	end   int
	name  string
	args  map[string]any
}

func isPlainToolNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

func isXMLNameByte(c byte) bool {
	return isPlainToolNameByte(c) || c == '.' || c == ':'
}

func skipHorizontal(text string, at int) int {
	for at < len(text) && (text[at] == ' ' || text[at] == '\t') {
		at++
	}
	return at
}

func skipAllSpace(text string, at int) int {
	for at < len(text) {
		r, size := utf8.DecodeRuneInString(text[at:])
		if !unicode.IsSpace(r) {
			break
		}
		at += size
	}
	return at
}

func markerPrefixFold(text, marker string) bool {
	if len(text) >= len(marker) {
		return false
	}
	return strings.HasPrefix(strings.ToLower(marker), strings.ToLower(text))
}

func startsMarkerFold(text string, at int, marker string) bool {
	return at >= 0 && at+len(marker) <= len(text) && strings.EqualFold(text[at:at+len(marker)], marker)
}

func scanName(text string, at int, xmlish bool) (end int, state repairScanState) {
	start := at
	for at < len(text) {
		valid := isPlainToolNameByte(text[at])
		if xmlish {
			valid = isXMLNameByte(text[at])
		}
		if !valid {
			break
		}
		at++
		if at-start > maxRepairToolNameLen {
			return at, repairInvalid
		}
	}
	if at == start {
		if at == len(text) {
			return at, repairPrefix
		}
		return at, repairInvalid
	}
	if at == len(text) {
		return at, repairPrefix
	}
	return at, repairComplete
}

func scanJSONObject(text string, start int) (int, repairScanState) {
	if start >= len(text) {
		return start, repairPrefix
	}
	if text[start] != '{' {
		return start, repairInvalid
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
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
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1, repairComplete
			}
			if depth < 0 {
				return i + 1, repairInvalid
			}
		}
	}
	return len(text), repairPrefix
}

func parseArgumentObject(text string, start, end int) (map[string]any, bool) {
	var args map[string]any
	if start < 0 || end > len(text) || start >= end || json.Unmarshal([]byte(text[start:end]), &args) != nil || args == nil {
		return nil, false
	}
	return args, true
}

func consumeOptionalClosing(text string, at int, name string, final bool) (int, repairScanState) {
	markerStart := skipAllSpace(text, at)
	closings := []string{harmonyCallMarker, endToolRequestMarker, "[/" + name + "]"}
	for _, marker := range closings {
		if strings.HasPrefix(text[markerStart:], marker) {
			return markerStart + len(marker), repairComplete
		}
		if markerStart < len(text) && strings.HasPrefix(marker, text[markerStart:]) {
			return len(text), repairPrefix
		}
	}
	if markerStart == len(text) && !final {
		return len(text), repairPrefix
	}
	return at, repairComplete
}

func scanBracketRepair(text string, final bool) repairScan {
	if text == "" || text[0] != '[' {
		return repairScan{state: repairInvalid}
	}
	at := 1
	syntaxTool := false
	if strings.HasPrefix(text[at:], "tool:") {
		syntaxTool = true
		at += len("tool:")
	} else if markerPrefixFold(text[at:], "tool:") {
		return repairScan{state: repairPrefix}
	}
	nameStart := at
	nameEnd, nameState := scanName(text, at, false)
	if nameState != repairComplete {
		name := ""
		if nameEnd > nameStart {
			name = text[nameStart:nameEnd]
		}
		return repairScan{state: nameState, name: name}
	}
	name := text[nameStart:nameEnd]
	if text[nameEnd] != ']' {
		return repairScan{state: repairInvalid, name: name}
	}
	at = nameEnd + 1
	if !syntaxTool {
		horizontalEnd := skipHorizontal(text, at)
		if horizontalEnd == len(text) {
			return repairScan{state: repairPrefix, name: name}
		}
		if text[horizontalEnd] == '\r' {
			horizontalEnd++
			if horizontalEnd < len(text) && text[horizontalEnd] == '\n' {
				horizontalEnd++
			}
		} else if text[horizontalEnd] == '\n' {
			horizontalEnd++
		} else {
			return repairScan{state: repairInvalid, name: name}
		}
		at = horizontalEnd
	}
	at = skipAllSpace(text, at)
	if at == len(text) {
		return repairScan{state: repairPrefix, name: name}
	}
	if text[at] == '<' {
		return scanXMLParameters(text, at, name, !syntaxTool, final)
	}
	jsonEnd, state := scanJSONObject(text, at)
	if state != repairComplete {
		return repairScan{state: state, name: name}
	}
	args, ok := parseArgumentObject(text, at, jsonEnd)
	if !ok {
		return repairScan{state: repairInvalid, name: name}
	}
	if !syntaxTool {
		markerStart := skipAllSpace(text, jsonEnd)
		if markerStart == len(text) && !final {
			return repairScan{state: repairPrefix, name: name}
		}
		for _, marker := range []string{endToolRequestMarker, "[/" + name + "]"} {
			if strings.HasPrefix(text[markerStart:], marker) {
				return repairScan{state: repairComplete, end: markerStart + len(marker), name: name, args: args}
			}
			if markerStart < len(text) && strings.HasPrefix(marker, text[markerStart:]) {
				return repairScan{state: repairPrefix, name: name}
			}
		}
		return repairScan{state: repairInvalid, name: name}
	}
	end, closeState := consumeOptionalClosing(text, jsonEnd, name, final)
	return repairScan{state: closeState, end: end, name: name, args: args}
}

func scanHarmonyRepair(text string, final bool) repairScan {
	at := 0
	if strings.HasPrefix(text, harmonyChannelMarker) {
		at += len(harmonyChannelMarker)
	} else if strings.HasPrefix(harmonyChannelMarker, text) {
		return repairScan{state: repairPrefix}
	} else if strings.HasPrefix(text, "<") {
		return repairScan{state: repairInvalid}
	}
	channel := ""
	for _, candidate := range []string{"commentary", "analysis", "final"} {
		if strings.HasPrefix(text[at:], candidate) {
			channel = candidate
			break
		}
		if strings.HasPrefix(candidate, text[at:]) {
			return repairScan{state: repairPrefix}
		}
	}
	if channel == "" {
		return repairScan{state: repairInvalid}
	}
	at += len(channel)
	if at == len(text) {
		return repairScan{state: repairPrefix}
	}
	if text[at] != ' ' && text[at] != '\t' {
		return repairScan{state: repairInvalid}
	}
	at = skipHorizontal(text, at)
	if !strings.HasPrefix(text[at:], "to=") {
		if strings.HasPrefix("to=", text[at:]) {
			return repairScan{state: repairPrefix}
		}
		return repairScan{state: repairInvalid}
	}
	at += len("to=")
	nameStart := at
	nameEnd, nameState := scanName(text, at, false)
	name := ""
	if nameEnd > nameStart {
		name = text[nameStart:nameEnd]
	}
	if nameState != repairComplete {
		return repairScan{state: nameState, name: name}
	}
	at = nameEnd
	if text[at] != ' ' && text[at] != '\t' {
		return repairScan{state: repairInvalid, name: name}
	}
	at = skipHorizontal(text, at)
	if !strings.HasPrefix(text[at:], "code") {
		if strings.HasPrefix("code", text[at:]) {
			return repairScan{state: repairPrefix, name: name}
		}
		return repairScan{state: repairInvalid, name: name}
	}
	at = skipAllSpace(text, at+len("code"))
	if strings.HasPrefix(text[at:], harmonyMessageMarker) {
		at = skipAllSpace(text, at+len(harmonyMessageMarker))
	} else if strings.HasPrefix(harmonyMessageMarker, text[at:]) {
		return repairScan{state: repairPrefix, name: name}
	}
	jsonEnd, state := scanJSONObject(text, at)
	if state != repairComplete {
		return repairScan{state: state, name: name}
	}
	args, ok := parseArgumentObject(text, at, jsonEnd)
	if !ok {
		return repairScan{state: repairInvalid, name: name}
	}
	end, closeState := consumeOptionalClosing(text, jsonEnd, name, final)
	return repairScan{state: closeState, end: end, name: name, args: args}
}

func scanFunctionRepair(text string, final bool) repairScan {
	const open = "<function="
	if !startsMarkerFold(text, 0, open) {
		if markerPrefixFold(text, open) {
			return repairScan{state: repairPrefix}
		}
		return repairScan{state: repairInvalid}
	}
	at := len(open)
	nameStart := at
	nameEnd, nameState := scanName(text, at, true)
	name := ""
	if nameEnd > nameStart {
		name = text[nameStart:nameEnd]
	}
	if nameState != repairComplete {
		return repairScan{state: nameState, name: name}
	}
	if text[nameEnd] != '>' {
		return repairScan{state: repairInvalid, name: name}
	}
	return scanXMLParameters(text, nameEnd+1, name, true, final)
}

func scanXMLParameters(text string, at int, name string, requireFunctionClose, final bool) repairScan {
	args := map[string]any{}
	for {
		at = skipAllSpace(text, at)
		if at == len(text) {
			if !requireFunctionClose && final && len(args) > 0 {
				return repairScan{state: repairComplete, end: at, name: name, args: args}
			}
			return repairScan{state: repairPrefix, name: name}
		}
		if startsMarkerFold(text, at, "</function>") {
			return repairScan{state: repairComplete, end: at + len("</function>"), name: name, args: args}
		}
		if markerPrefixFold(text[at:], "</function>") {
			return repairScan{state: repairPrefix, name: name}
		}
		const paramOpen = "<parameter="
		if !startsMarkerFold(text, at, paramOpen) {
			if markerPrefixFold(text[at:], paramOpen) {
				return repairScan{state: repairPrefix, name: name}
			}
			if !requireFunctionClose && len(args) > 0 {
				return repairScan{state: repairComplete, end: at, name: name, args: args}
			}
			return repairScan{state: repairInvalid, name: name}
		}
		paramNameStart := at + len(paramOpen)
		paramNameEnd, state := scanName(text, paramNameStart, true)
		if state != repairComplete {
			return repairScan{state: state, name: name}
		}
		if text[paramNameEnd] != '>' {
			return repairScan{state: repairInvalid, name: name}
		}
		valueStart := paramNameEnd + 1
		closeAt := indexFold(text[valueStart:], "</parameter>")
		if closeAt < 0 {
			return repairScan{state: repairPrefix, name: name}
		}
		closeAt += valueStart
		value := text[valueStart:closeAt]
		if strings.HasPrefix(value, "\r\n") {
			value = value[2:]
		} else if strings.HasPrefix(value, "\n") || strings.HasPrefix(value, "\r") {
			value = value[1:]
		}
		value = strings.TrimSuffix(strings.TrimSuffix(value, "\n"), "\r")
		args[text[paramNameStart:paramNameEnd]] = value
		at = closeAt + len("</parameter>")
	}
}

func scanRepairSyntax(text string, final bool) repairScan {
	switch {
	case strings.HasPrefix(text, "["):
		return scanBracketRepair(text, final)
	case strings.HasPrefix(strings.ToLower(text), "<function=") || markerPrefixFold(text, "<function="):
		return scanFunctionRepair(text, final)
	default:
		return scanHarmonyRepair(text, final)
	}
}

func possibleRepairSyntax(text string) bool {
	if text == "" {
		return false
	}
	if text[0] == '[' {
		return len(text) == 1 || isPlainToolNameByte(text[1])
	}
	for _, marker := range []string{"<function=", harmonyChannelMarker, "commentary", "analysis", "final"} {
		if strings.HasPrefix(strings.ToLower(text), strings.ToLower(marker)) || markerPrefixFold(text, marker) {
			return true
		}
	}
	return false
}
