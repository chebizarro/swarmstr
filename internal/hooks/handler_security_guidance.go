package hooks

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type securityPatternRule struct {
	name       string
	pathCheck  func(string) bool
	pathFilter func(string) bool
	substrings []string
	re         *regexp.Regexp
	message    string
}

var legacyDangerousCommandRules = []struct {
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

var legacyCredentialRules = []struct {
	name string
	re   *regexp.Regexp
}{
	{"generic-api-key", regexp.MustCompile(`(?i)\b(api[_-]?key|token|secret|password)\s*[:=]\s*['"]?[^\s'"]{16,}`)},
	{"aws-access-key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"openai-key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`)},
}

var legacyUnsafeFileRules = []struct {
	name string
	re   *regexp.Regexp
	msg  string
}{
	{"write-etc", regexp.MustCompile(`(?i)(^|\s|=)(?:/etc/|/private/etc/)`), "file operation targets system configuration"},
	{"write-ssh", regexp.MustCompile(`(?i)(^|\s|=)(?:~/?\.ssh|/[^\s]*/\.ssh)(?:/|\s|$)`), "file operation targets SSH material"},
	{"path-traversal", regexp.MustCompile(`(^|/|\\)\.\.(?:/|\\|$)`), "path traversal segment detected"},
}

var securityGuidanceRules = []securityPatternRule{
	{name: "github_actions_workflow", pathCheck: func(p string) bool {
		return strings.Contains(p, ".github/workflows/") && (strings.HasSuffix(p, ".yml") || strings.HasSuffix(p, ".yaml"))
	}, message: "GitHub Actions workflow edited: avoid untrusted ${{ github.event.* }} directly in run: commands; pass through env and quote values, and validate ref inputs."},
	{name: "child_process_exec", pathFilter: isJSPath, substrings: []string{"child_process.exec", "execSync("}, re: regexp.MustCompile(`(?m)(^|[^a-zA-Z0-9_\.])exec\(`), message: "child_process.exec()/execSync runs through a shell and can introduce command injection; prefer execFile/spawn with argument arrays."},
	{name: "new_function_injection", substrings: []string{"new Function"}, message: "new Function() evaluates strings as code; avoid interpolating untrusted data into generated functions."},
	{name: "eval_injection", pathFilter: func(p string) bool { return !isDocPath(p) }, re: regexp.MustCompile(`(?m)(^|[^a-zA-Z0-9_\.])eval\(`), message: "eval() executes arbitrary code; prefer JSON parsing, literal parsers, or a constrained expression evaluator."},
	{name: "react_dangerously_set_html", substrings: []string{"dangerouslySetInnerHTML"}, message: "dangerouslySetInnerHTML is an XSS sink; sanitize untrusted HTML with a vetted sanitizer such as DOMPurify."},
	{name: "document_write_xss", substrings: []string{"document.write"}, message: "document.write() is an XSS-prone sink; prefer safe DOM APIs such as createElement/appendChild."},
	{name: "innerHTML_xss", substrings: []string{".innerHTML =", ".innerHTML="}, message: "innerHTML assignment can create XSS; prefer textContent or sanitize HTML before insertion."},
	{name: "outerHTML_xss", substrings: []string{".outerHTML =", ".outerHTML="}, message: "outerHTML assignment is an XSS sink; prefer textContent or sanitize HTML before insertion."},
	{name: "insertAdjacentHTML_xss", substrings: []string{".insertAdjacentHTML("}, message: "insertAdjacentHTML is an XSS sink; prefer insertAdjacentText or sanitized HTML."},
	{name: "pickle_deserialization", pathFilter: isPythonPath, re: regexp.MustCompile(`(?m)(^|[^a-zA-Z0-9_])pickle\.(loads?|Unpickler)\b|(^|[^a-zA-Z0-9_])pkl_load\(`), message: "pickle/cPickle/cloudpickle/dill/joblib/pandas.read_pickle/torch.load can execute code when loading untrusted data; prefer JSON or schema-validated formats."},
	{name: "os_system_injection", pathFilter: isPythonPath, substrings: []string{"from os import system"}, re: regexp.MustCompile(`\bos\.system\s*\(`), message: "os.system() runs a shell and can introduce command injection; prefer subprocess.run([...]) without shell=True."},
	{name: "python_subprocess_shell", re: regexp.MustCompile(`(?s)subprocess\.(?:run|call|Popen|check_output|check_call)\(.*shell\s*=\s*True`), message: "subprocess with shell=True enables command injection; pass arguments as a list without a shell."},
	{name: "go_exec_shell_injection", re: regexp.MustCompile(`exec\.Command\(\s*"(?:sh|bash|/bin/sh|/bin/bash)"`), message: "exec.Command with sh/bash enables command injection; invoke the target binary directly with separate arguments."},
	{name: "unsafe_yaml_load", re: regexp.MustCompile(`\byaml\.load\s*\((?:[^)\n]{0,80}\bSafe)?`), message: "yaml.load()/unsafe_load can construct arbitrary Python objects; use yaml.safe_load plus schema validation."},
	{name: "yaml_unsafe_load_variants", re: regexp.MustCompile(`(?:\byaml\.unsafe_load|\.yaml_unsafe_load)\s*\(`), message: "yaml.unsafe_load can construct arbitrary Python objects; use yaml.safe_load plus schema validation."},
	{name: "node_createcipher_no_iv", re: regexp.MustCompile(`\bcrypto\.(createCipher|createDecipher)\b`), message: "crypto.createCipher/createDecipher use unsafe legacy key derivation; use createCipheriv/createDecipheriv with a unique IV."},
	{name: "aes_ecb_mode", re: regexp.MustCompile(`\bAES\.MODE_ECB\b|\bmodes\.ECB\s*\(|["']aes-\d+-ecb["']`), message: "AES ECB mode leaks plaintext structure; use an authenticated mode such as AES-GCM."},
	{name: "tls_verification_disabled", re: regexp.MustCompile(`\bverify\s*=\s*False\b|rejectUnauthorized\s*:\s*false|InsecureSkipVerify\s*:\s*true|NODE_TLS_REJECT_UNAUTHORIZED\s*=\s*["']?0|ssl\._create_unverified_context|check_hostname\s*=\s*False`), message: "TLS verification is disabled; this permits MITM attacks. Trust a development CA instead."},
	{name: "marshal_loads", re: regexp.MustCompile(`\bmarshal\.loads?\s*\(`), message: "marshal.load/loads on untrusted data can be unsafe; prefer a data-only format with validation."},
	{name: "shelve_open", re: regexp.MustCompile(`\bshelve\.open\s*\(`), message: "shelve loads pickle-backed data and can execute code when pointed at untrusted files."},
	{name: "xml_unsafe_parse", re: regexp.MustCompile(`\b(xml\.etree\.ElementTree|ElementTree|ET)\.(parse|fromstring|XML)\s*\(|\bminidom\.(parse|parseString)\s*\(|\bxml\.sax\.(parse|make_parser)\b`), message: "stdlib XML parsers can be vulnerable to XXE/billion-laughs attacks; use defusedxml for untrusted XML."},
	{name: "pickle_variants_load", re: regexp.MustCompile(`\b(cPickle|cloudpickle|dill)\.(load|loads)\s*\(`), message: "pickle-family loaders can execute arbitrary code when loading untrusted data."},
	{name: "script_src_without_sri", re: regexp.MustCompile(`<script\s+(?:[^>]{0,400})src\s*=\s*["'](?:https?:)?//[^"']{1,300}["'][^>]{0,100}>`), message: "external script tags should include Subresource Integrity (integrity + crossorigin) to reduce CDN compromise risk."},
	{name: "torch_unsafe_load", re: regexp.MustCompile(`(?:\btorch\.load|\.torch_load)\s*\(`), message: "torch.load unpickles by default; use weights_only=True for tensor-only checkpoints."},
	{name: "pickle_wrapper_load", re: regexp.MustCompile(`\bjoblib\.load\s*\(|\b(?:pd|pandas)\.read_pickle\s*\(|\.cloudpickle_load\s*\(|\b(?:np|numpy)\.load\s*\([^)\n]{0,200}allow_pickle\s*=\s*True`), message: "wrapper APIs that load pickle data can execute code; prefer data-only formats or strict schema validation."},
}

var unsafeYamlLoadRE = regexp.MustCompile(`\byaml\.load\s*\(`)
var yamlLoadSafeRE = regexp.MustCompile(`\byaml\.load\s*\([^)\n]{0,80}\bSafe`)
var scriptWithoutSRIHasIntegrityRE = regexp.MustCompile(`(?i)<script\s+[^>]*\bintegrity\s*=`)
var torchWeightsOnlyRE = regexp.MustCompile(`(?:\btorch\.load|\.torch_load)\s*\([^)\n]{0,200}weights_only\s*=\s*True`)

func makeSecurityGuidanceHandler(opts BundledHandlerOpts) HookHandler {
	return func(ev *Event) error {
		findings := AnalyzeSecurityGuidance(ev.Context)
		for _, finding := range findings {
			ev.Messages = append(ev.Messages, "⚠️ security-guidance ["+finding.Rule+"]: "+finding.Message)
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

// AnalyzeSecurityGuidance inspects hook context for file-write and command/tool
// content that matches the bundled security guidance pattern rules.
func AnalyzeSecurityGuidance(ctx map[string]any) []SecurityFinding {
	if ctx == nil {
		return nil
	}
	path := firstContextString(ctx, "file_path", "notebook_path", "path", "file", "target", "destination")
	command := firstContextString(ctx, "command", "cmd", "shell", "script", "input")
	content := firstContextString(ctx, "content", "new_string", "text", "input", "script", "command", "cmd", "shell")
	if edits, ok := ctx["edits"]; ok {
		content = strings.TrimSpace(content + " " + stringifyValue(edits))
	}
	out := CheckSecurityGuidancePatterns(path, content)
	for _, rule := range legacyDangerousCommandRules {
		if command != "" && rule.re.MatchString(command) {
			out = append(out, SecurityFinding{Rule: rule.name, Severity: "warn", Message: rule.msg})
		}
	}
	output := strings.Join([]string{firstContextString(ctx, "output", "stdout", "stderr", "result"), command, content}, "\n")
	for _, rule := range legacyCredentialRules {
		if rule.re.MatchString(output) {
			out = append(out, SecurityFinding{Rule: rule.name, Severity: "warn", Message: "possible credential exposure detected in command/output"})
		}
	}
	fileText := strings.Join([]string{path, command}, " ")
	for _, rule := range legacyUnsafeFileRules {
		if rule.re.MatchString(fileText) {
			out = append(out, SecurityFinding{Rule: rule.name, Severity: "warn", Message: rule.msg})
		}
	}
	return dedupeFindings(out)
}

// CheckSecurityGuidancePatterns evaluates the bundled pattern rules against a
// file path and content snippet. It is side-effect-free for tests and callers.
func CheckSecurityGuidancePatterns(filePath, content string) []SecurityFinding {
	normalizedPath := strings.TrimPrefix(filepath.ToSlash(filePath), "/")
	var out []SecurityFinding
	for _, rule := range securityGuidanceRules {
		if rule.pathFilter != nil && !rule.pathFilter(normalizedPath) {
			continue
		}
		matched := false
		if rule.pathCheck != nil && rule.pathCheck(normalizedPath) {
			matched = true
		}
		for _, needle := range rule.substrings {
			if !matched && strings.Contains(content, needle) {
				matched = true
			}
		}
		if !matched && rule.re != nil && rule.re.MatchString(content) {
			matched = true
		}
		if matched {
			if rule.name == "unsafe_yaml_load" && (!unsafeYamlLoadRE.MatchString(content) || yamlLoadSafeRE.MatchString(content)) {
				continue
			}
			if rule.name == "script_src_without_sri" && scriptWithoutSRIHasIntegrityRE.MatchString(content) {
				continue
			}
			if rule.name == "torch_unsafe_load" && torchWeightsOnlyRE.MatchString(content) {
				continue
			}
			out = append(out, SecurityFinding{Rule: rule.name, Severity: "warn", Message: rule.message})
		}
	}
	return dedupeFindings(out)
}

func firstContextString(ctx map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := ctx[key]; ok {
			s := stringifyValue(v)
			if strings.TrimSpace(s) != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func stringifyValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []string:
		return strings.Join(x, " ")
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			parts = append(parts, stringifyValue(item))
		}
		return strings.Join(parts, " ")
	case map[string]any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			parts = append(parts, stringifyValue(item))
		}
		return strings.Join(parts, " ")
	default:
		return fmt.Sprintf("%v", x)
	}
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

func isJSPath(path string) bool {
	return hasAnySuffix(path, ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".mts", ".cts", ".vue", ".svelte")
}
func isPythonPath(path string) bool { return hasAnySuffix(path, ".py", ".pyi", ".ipynb") }
func isDocPath(path string) bool {
	return hasAnySuffix(path, ".md", ".mdx", ".txt", ".rst", ".json", ".yaml", ".yml")
}

func hasAnySuffix(path string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}
