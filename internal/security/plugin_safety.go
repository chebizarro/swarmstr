package security

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// CodeSafetyFinding describes a dangerous API pattern in plugin source.
type CodeSafetyFinding struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	CheckID  string `json:"check_id"`
	Severity string `json:"severity"`
	Snippet  string `json:"snippet,omitempty"`
}

type safetyPattern struct {
	id, severity string
	re           *regexp.Regexp
}

var pluginDangerousPatterns = []safetyPattern{
	{"plugin-dangerous-eval", SeverityCritical, regexp.MustCompile(`\b(eval|Function)\s*\(`)},
	{"plugin-dangerous-child-process", SeverityCritical, regexp.MustCompile(`\b(child_process|execSync|spawnSync|\.exec\s*\()`)},
	{"plugin-dangerous-fs-write", SeverityWarn, regexp.MustCompile(`\b(fs\.(writeFile|appendFile|rm|unlink|rmdir)|Deno\.writeTextFile)\b`)},
	{"plugin-dangerous-network", SeverityWarn, regexp.MustCompile(`\b(fetch|XMLHttpRequest|http\.request|https\.request|net\.connect)\s*\(`)},
	{"plugin-dangerous-shell", SeverityCritical, regexp.MustCompile(`\b(os/exec|syscall\.Exec)\b`)},
}

// ScanPluginSafety scans JS/TS/Go plugin source for dangerous APIs.
func ScanPluginSafety(pluginPath string) ([]CodeSafetyFinding, error) {
	var findings []CodeSafetyFinding
	if err := filepath.WalkDir(pluginPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "node_modules" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !scanSourceFile(path) {
			return nil
		}
		fileFindings, err := scanSource(path)
		if err != nil {
			return err
		}
		findings = append(findings, fileFindings...)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("scan plugin source: %w", err)
	}
	return findings, nil
}

func scanSourceFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".mjs", ".cjs", ".ts", ".tsx", ".go":
		return true
	default:
		return false
	}
}

func scanSource(path string) ([]CodeSafetyFinding, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var findings []CodeSafetyFinding
	s := bufio.NewScanner(f)
	lineNo := 0
	for s.Scan() {
		lineNo++
		line := strings.TrimSpace(s.Text())
		for _, pattern := range pluginDangerousPatterns {
			if pattern.re.MatchString(line) {
				findings = append(findings, CodeSafetyFinding{Path: path, Line: lineNo, CheckID: pattern.id, Severity: pattern.severity, Snippet: trimSnippet(line)})
			}
		}
	}
	return findings, s.Err()
}

func trimSnippet(line string) string {
	if len(line) <= 160 {
		return line
	}
	return line[:160] + "…"
}

func auditPluginSafety(paths []string) []Finding {
	var findings []Finding
	for _, pluginPath := range paths {
		safetyFindings, err := ScanPluginSafety(pluginPath)
		if err != nil {
			findings = append(findings, Finding{CheckID: "plugin-safety-scan-error", Severity: SeverityWarn, Message: fmt.Sprintf("plugin %s safety scan failed: %v", pluginPath, err), Remediation: "Inspect plugin source and rerun the scanner before loading."})
			continue
		}
		for _, sf := range safetyFindings {
			findings = append(findings, Finding{CheckID: sf.CheckID, Severity: sf.Severity, Message: fmt.Sprintf("plugin source %s:%d uses dangerous API: %s", sf.Path, sf.Line, sf.Snippet), Remediation: "Review the plugin, sandbox it, or remove the dangerous API before enabling it."})
		}
	}
	return findings
}
