package hooks

import (
	"fmt"
	"regexp"
	"strings"
)

var dangerousCommandRules = []struct {
	name string
	re   *regexp.Regexp
	msg  string
}{
	{"rm-rf-root", regexp.MustCompile(`(?i)(^|\s)rm\s+(-[^\n;&|]*r[^\n;&|]*f|-rx?f|-fr)\s+(?:/|~|\$HOME)(?:\s|$)`), "dangerous recursive delete targeting a root or home directory"},
	{"chmod-777", regexp.MustCompile(`(?i)(^|\s)chmod\s+(?:-R\s+)?777\b`), "world-writable chmod 777 weakens file permissions"},
	{"chown-recursive-root", regexp.MustCompile(`(?i)(^|\s)chown\s+-R\s+[^\n;&|]+\s+(?:/|~|\$HOME)(?:\s|$)`), "recursive chown over root/home can damage the system"},
	{"curl-pipe-shell", regexp.MustCompile(`(?i)(curl|wget)\b[^\n|;&]*(\||>)\s*(sh|bash|zsh)\b`), "remote script execution should be reviewed before running"},
	{"mkfs-dd", regexp.MustCompile(`(?i)\b(mkfs(?:\.[a-z0-9]+)?|dd\s+if=)\b`), "destructive disk operation detected"},
}

var credentialRules = []struct {
	name string
	re   *regexp.Regexp
}{
	{"generic-api-key", regexp.MustCompile(`(?i)\b(api[_-]?key|token|secret|password)\s*[:=]\s*['"]?[^\s'"]{16,}`)},
	{"aws-access-key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"openai-key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`)},
}

var unsafeFileRules = []struct {
	name string
	re   *regexp.Regexp
	msg  string
}{
	{"write-etc", regexp.MustCompile(`(?i)(^|\s|=)(?:/etc/|/private/etc/)`), "file operation targets system configuration"},
	{"write-ssh", regexp.MustCompile(`(?i)(^|\s|=)(?:~/?\.ssh|/[^\s]*/\.ssh)(?:/|\s|$)`), "file operation targets SSH material"},
	{"path-traversal", regexp.MustCompile(`(^|/|\\)\.\.(?:/|\\|$)`), "path traversal segment detected"},
}

func makeSecurityGuidanceHandler(opts BundledHandlerOpts) HookHandler {
	return func(ev *Event) error {
		findings := AnalyzeSecurityGuidance(ev.Context)
		for _, finding := range findings {
			ev.Messages = append(ev.Messages, "⚠️ security-guidance: "+finding.Message)
		}
		return nil
	}
}

// SecurityFinding is a warning produced by the security guidance hook.
type SecurityFinding struct {
	Rule     string
	Severity string
	Message  string
}

// AnalyzeSecurityGuidance inspects hook context for dangerous shell commands,
// leaked credentials, and unsafe file paths/operations.
func AnalyzeSecurityGuidance(ctx map[string]any) []SecurityFinding {
	if ctx == nil {
		return nil
	}
	var out []SecurityFinding
	command := firstContextString(ctx, "command", "cmd", "input", "script", "shell")
	if args := firstContextString(ctx, "args", "arguments"); command == "" && args != "" {
		command = args
	}
	if command != "" {
		for _, rule := range dangerousCommandRules {
			if rule.re.MatchString(command) {
				out = append(out, SecurityFinding{Rule: rule.name, Severity: "warn", Message: rule.msg})
			}
		}
	}
	output := strings.Join([]string{firstContextString(ctx, "output", "stdout", "stderr", "result"), command}, "\n")
	for _, rule := range credentialRules {
		if rule.re.MatchString(output) {
			out = append(out, SecurityFinding{Rule: rule.name, Severity: "warn", Message: "possible credential exposure detected in command/output"})
		}
	}
	fileText := strings.Join([]string{firstContextString(ctx, "path", "file", "target", "destination", "operation"), command}, " ")
	for _, rule := range unsafeFileRules {
		if rule.re.MatchString(fileText) {
			out = append(out, SecurityFinding{Rule: rule.name, Severity: "warn", Message: rule.msg})
		}
	}
	return dedupeFindings(out)
}

func firstContextString(ctx map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := ctx[key]; ok {
			switch x := v.(type) {
			case string:
				if strings.TrimSpace(x) != "" {
					return x
				}
			case []string:
				return strings.Join(x, " ")
			case []any:
				parts := make([]string, 0, len(x))
				for _, item := range x {
					parts = append(parts, fmt.Sprintf("%v", item))
				}
				return strings.Join(parts, " ")
			default:
				s := strings.TrimSpace(fmt.Sprintf("%v", x))
				if s != "" && s != "<nil>" {
					return s
				}
			}
		}
	}
	return ""
}

func dedupeFindings(in []SecurityFinding) []SecurityFinding {
	seen := map[string]bool{}
	out := make([]SecurityFinding, 0, len(in))
	for _, f := range in {
		if seen[f.Rule] {
			continue
		}
		seen[f.Rule] = true
		out = append(out, f)
	}
	return out
}
