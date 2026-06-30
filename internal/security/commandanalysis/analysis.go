package commandanalysis

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Segment is one argv-bearing command segment extracted from a shell line.
type Segment struct {
	Raw     string   `json:"raw"`
	Argv    []string `json:"argv"`
	Carrier bool     `json:"carrier,omitempty"`
	Nested  bool     `json:"nested,omitempty"`
}

// Analysis is the approval-facing summary of command structure and risks.
type Analysis struct {
	CommandText   string    `json:"command_text,omitempty"`
	Argv          []string  `json:"argv,omitempty"`
	Segments      []Segment `json:"segments,omitempty"`
	Warnings      []string  `json:"warnings,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	SafeBin       bool      `json:"safe_bin,omitempty"`
	UnsafeWrapper bool      `json:"unsafe_wrapper,omitempty"`
	InlineEval    bool      `json:"inline_eval,omitempty"`
	PipeToShell   bool      `json:"pipe_to_shell,omitempty"`
	Signature     string    `json:"signature,omitempty"`
	AllowAlways   bool      `json:"allow_always_available,omitempty"`
}

var defaultSafeBins = map[string]bool{
	"cat": true, "cd": true, "egrep": true, "env": true, "fgrep": true, "find": true,
	"git": true, "grep": true, "head": true, "ls": true, "pwd": true, "rg": true,
	"sed": true, "sort": true, "tail": true, "test": true, "true": true, "wc": true,
}

var dangerousPatterns = []struct {
	re   *regexp.Regexp
	text string
}{
	{regexp.MustCompile(`(?i)\brm\s+[^\n;|&]*-[^\n;|&]*r[^\n;|&]*f\s+(/|\*)`), "recursive forced delete"},
	{regexp.MustCompile(`(?i)\b(dd|mkfs|fdisk|diskutil)\b`), "disk or filesystem mutation"},
	{regexp.MustCompile(`(?i)\bsudo\b`), "privilege escalation via sudo"},
	{regexp.MustCompile(`(?i)\b(chmod|chown)\b`), "permission or ownership change"},
	{regexp.MustCompile(`(?i)\b(password|api[_-]?key|secret|token)=`), "secret-like value in command"},
	{regexp.MustCompile("\\$\\(|`[^`]+`"), "command substitution"},
}

// Analyze inspects a command string or argv and returns approval warnings and a stable signature.
func Analyze(commandText string, argv []string) Analysis {
	commandText = strings.TrimSpace(commandText)
	if commandText == "" && len(argv) > 0 {
		commandText = strings.Join(argv, " ")
	}
	out := Analysis{CommandText: commandText, Argv: append([]string(nil), argv...)}
	if len(argv) > 0 {
		out.Segments = append(out.Segments, Segment{Raw: strings.Join(argv, " "), Argv: append([]string(nil), argv...)})
	} else if commandText != "" {
		out.Segments = parseShellSegments(commandText)
	}
	seen := map[string]bool{}
	warn := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out.Warnings = append(out.Warnings, s)
		}
	}
	lower := strings.ToLower(commandText)
	if regexp.MustCompile("(?i)(curl|wget)[^|;&]*\\|\\s*(sudo\\s+)?(sh|bash|zsh|dash)\\b").MatchString(commandText) {
		out.PipeToShell = true
		out.UnsafeWrapper = true
		warn("downloads remote content and pipes it directly to a shell")
	}
	if strings.Contains(lower, " eval ") || strings.HasPrefix(lower, "eval ") {
		out.InlineEval = true
		out.UnsafeWrapper = true
		warn("uses eval to execute dynamically constructed shell text")
	}
	for _, p := range dangerousPatterns {
		if p.re.MatchString(commandText) {
			warn(p.text)
		}
	}
	for i := range out.Segments {
		seg := &out.Segments[i]
		if len(seg.Argv) == 0 {
			continue
		}
		bin := base(seg.Argv[0])
		if isCarrier(bin, seg.Argv) {
			seg.Carrier = true
			out.UnsafeWrapper = true
			warn(fmt.Sprintf("%s can dispatch nested commands", bin))
			if nested := nestedCommand(bin, seg.Argv); nested != "" {
				out.InlineEval = true
				seg.Nested = true
				warn(fmt.Sprintf("%s executes inline command text", bin))
			}
		}
	}
	out.SafeBin = len(out.Segments) == 1 && len(out.Segments[0].Argv) > 0 && defaultSafeBins[base(out.Segments[0].Argv[0])] && !out.UnsafeWrapper && !out.InlineEval && !out.PipeToShell
	out.AllowAlways = out.SafeBin
	out.Signature = StableSignature(out)
	out.Summary = summarize(out)
	return out
}

func parseShellSegments(command string) []Segment {
	parts := splitTopLevel(command)
	segs := make([]Segment, 0, len(parts))
	for _, p := range parts {
		argv := tokenize(p)
		if len(argv) > 0 {
			segs = append(segs, Segment{Raw: strings.TrimSpace(p), Argv: argv})
		}
	}
	return segs
}

func splitTopLevel(s string) []string {
	var out []string
	var b strings.Builder
	quote := rune(0)
	for _, r := range s {
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			b.WriteRune(r)
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			b.WriteRune(r)
			continue
		}
		if r == '|' || r == ';' || r == '&' {
			if strings.TrimSpace(b.String()) != "" {
				out = append(out, b.String())
			}
			b.Reset()
			continue
		}
		b.WriteRune(r)
	}
	if strings.TrimSpace(b.String()) != "" {
		out = append(out, b.String())
	}
	return out
}

func tokenize(s string) []string {
	var out []string
	var b strings.Builder
	quote := rune(0)
	esc := false
	for _, r := range s {
		if esc {
			b.WriteRune(r)
			esc = false
			continue
		}
		if r == '\\' {
			esc = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' {
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
			continue
		}
		b.WriteRune(r)
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

func isCarrier(bin string, argv []string) bool {
	switch bin {
	case "sh", "bash", "zsh", "dash":
		for _, a := range argv[1:] {
			if a == "-c" || a == "-lc" {
				return true
			}
		}
	case "eval", "xargs":
		return true
	case "env":
		return len(argv) > 1 && isCarrier(base(lastNonAssignment(argv[1:])), argv[1:])
	}
	return false
}

func nestedCommand(bin string, argv []string) string {
	if bin == "sh" || bin == "bash" || bin == "zsh" || bin == "dash" {
		for i, a := range argv {
			if (a == "-c" || a == "-lc") && i+1 < len(argv) {
				return argv[i+1]
			}
		}
	}
	if bin == "eval" && len(argv) > 1 {
		return strings.Join(argv[1:], " ")
	}
	return ""
}

func StableSignature(a Analysis) string {
	parts := []string{"exec"}
	if len(a.Segments) == 1 && len(a.Segments[0].Argv) > 0 && !a.UnsafeWrapper && !a.InlineEval && !a.PipeToShell {
		argv := append([]string(nil), a.Segments[0].Argv...)
		argv[0] = base(argv[0])
		parts = append(parts, argv...)
	} else {
		sum := sha256.Sum256([]byte(a.CommandText))
		parts = append(parts, "shell", hex.EncodeToString(sum[:8]))
	}
	b, _ := json.Marshal(parts)
	return string(b)
}

func IsAllowAlwaysSafe(a Analysis) bool { return a.AllowAlways && a.Signature != "" }

func summarize(a Analysis) string {
	if len(a.Warnings) > 0 {
		return fmt.Sprintf("%d command segment(s), %d warning(s)", len(a.Segments), len(a.Warnings))
	}
	if a.SafeBin {
		return "safe-bin command eligible for allow-always"
	}
	return fmt.Sprintf("%d command segment(s) detected", len(a.Segments))
}

func base(s string) string { return strings.ToLower(filepath.Base(strings.TrimSpace(s))) }
func lastNonAssignment(argv []string) string {
	for _, a := range argv {
		if !strings.Contains(a, "=") {
			return a
		}
	}
	return ""
}
