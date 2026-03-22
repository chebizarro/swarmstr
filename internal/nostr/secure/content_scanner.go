// Package secure – content_scanner.go detects sensitive content (secrets,
// credentials, private keys) in text before it is published to Nostr relays.
//
// The scanner uses a curated set of high-precision regex patterns designed to
// catch real secrets while minimising false positives on normal conversation.
// Each pattern has a severity level:
//
//   - critical: Almost certainly a real secret (nsec, PEM key, cloud API key)
//   - high:     Very likely a secret (JWT, connection string, bearer token)
//   - medium:   Possibly sensitive (password assignment patterns)
package secure

import (
	"regexp"
	"strings"
)

// Scan severity levels.
const (
	ScanCritical = "critical"
	ScanHigh     = "high"
	ScanMedium   = "medium"
)

// ScanFinding represents a single detected sensitive pattern.
type ScanFinding struct {
	// PatternName is a machine-readable identifier (e.g. "nostr-nsec").
	PatternName string
	// Severity is critical, high, or medium.
	Severity string
	// Excerpt is a truncated sample of the matched text (max 20 chars).
	Excerpt string
	// Position is the byte offset in the scanned text.
	Position int
}

// ScanResult is the outcome of scanning text for sensitive content.
type ScanResult struct {
	// Clean is true when no findings were detected.
	Clean bool
	// Findings contains all detected patterns (empty when Clean is true).
	Findings []ScanFinding
}

// FindingNames returns deduplicated pattern names from findings.
func (r ScanResult) FindingNames() []string {
	seen := make(map[string]struct{}, len(r.Findings))
	var names []string
	for _, f := range r.Findings {
		if _, ok := seen[f.PatternName]; !ok {
			seen[f.PatternName] = struct{}{}
			names = append(names, f.PatternName)
		}
	}
	return names
}

// compiledPattern is a pre-compiled regex with metadata.
type compiledPattern struct {
	name     string
	severity string
	re       *regexp.Regexp
}

// ContentScanner detects sensitive content in text.
type ContentScanner struct {
	patterns []compiledPattern
}

// NewContentScanner creates a scanner with the built-in pattern set.
func NewContentScanner() *ContentScanner {
	s := &ContentScanner{}
	for _, def := range builtinPatterns {
		s.patterns = append(s.patterns, compiledPattern{
			name:     def.name,
			severity: def.severity,
			re:       regexp.MustCompile(def.pattern),
		})
	}
	return s
}

// Scan checks text for sensitive content.
func (s *ContentScanner) Scan(text string) ScanResult {
	if s == nil || text == "" {
		return ScanResult{Clean: true}
	}
	var findings []ScanFinding
	for _, p := range s.patterns {
		locs := p.re.FindAllStringIndex(text, -1)
		for _, loc := range locs {
			matched := text[loc[0]:loc[1]]
			excerpt := matched
			if len(excerpt) > 20 {
				excerpt = excerpt[:17] + "..."
			}
			findings = append(findings, ScanFinding{
				PatternName: p.name,
				Severity:    p.severity,
				Excerpt:     excerpt,
				Position:    loc[0],
			})
		}
	}
	return ScanResult{
		Clean:    len(findings) == 0,
		Findings: findings,
	}
}

// ScanStrings scans multiple strings and merges findings.
func (s *ContentScanner) ScanStrings(texts ...string) ScanResult {
	if s == nil {
		return ScanResult{Clean: true}
	}
	var all []ScanFinding
	for _, text := range texts {
		r := s.Scan(text)
		all = append(all, r.Findings...)
	}
	return ScanResult{
		Clean:    len(all) == 0,
		Findings: all,
	}
}

// PatternCount returns the number of active patterns.
func (s *ContentScanner) PatternCount() int {
	if s == nil {
		return 0
	}
	return len(s.patterns)
}

// ── Built-in pattern definitions ─────────────────────────────────────────────

type patternDef struct {
	name     string
	severity string
	pattern  string
}

// builtinPatterns is the curated set of secret-detection regexes.
// Ordered from highest to lowest confidence. All patterns are compiled once
// at scanner creation time.
//
// Design principles:
//   - High precision: each pattern targets a specific, well-known secret format
//   - Bounded matches: patterns use length constraints to avoid runaway matching
//   - Case handling: only case-insensitive where the format genuinely varies
var builtinPatterns = []patternDef{
	// ── Nostr keys ──────────────────────────────────────────────────────
	{
		name:     "nostr-nsec",
		severity: ScanCritical,
		pattern:  `nsec1[qpzry9x8gf2tvdw0s3jn54khce6mua7l]{58}`,
	},
	{
		name:     "nostr-hex-privkey",
		severity: ScanCritical,
		pattern:  `(?i)(?:private[_-]?key|priv[_-]?key|secret[_-]?key)\s*[:=]\s*[0-9a-f]{64}`,
	},

	// ── PEM private keys ────────────────────────────────────────────────
	{
		name:     "pem-private-key",
		severity: ScanCritical,
		pattern:  `-----BEGIN\s+(?:RSA |EC |DSA |OPENSSH |ED25519 )?PRIVATE KEY-----`,
	},

	// ── AI provider API keys ────────────────────────────────────────────
	{
		name:     "openai-api-key",
		severity: ScanCritical,
		// OpenAI keys: sk-<org/proj prefix optional><48+ alphanumeric chars>
		pattern: `sk-(?:proj-)?[a-zA-Z0-9]{32,}`,
	},
	{
		name:     "anthropic-api-key",
		severity: ScanCritical,
		pattern:  `sk-ant-[a-zA-Z0-9_-]{20,}`,
	},

	// ── Cloud provider keys ─────────────────────────────────────────────
	{
		name:     "aws-access-key-id",
		severity: ScanCritical,
		pattern:  `AKIA[0-9A-Z]{16}`,
	},
	{
		name:     "aws-secret-key",
		severity: ScanHigh,
		pattern:  `(?i)(?:aws[_-]?secret|secret[_-]?access[_-]?key)\s*[:=]\s*[A-Za-z0-9/+=]{40}`,
	},
	{
		name:     "gcp-api-key",
		severity: ScanHigh,
		pattern:  `AIza[0-9A-Za-z_-]{35}`,
	},
	{
		name:     "azure-key",
		severity: ScanHigh,
		pattern:  `(?i)(?:azure[_-]?(?:storage|account)[_-]?key)\s*[:=]\s*[A-Za-z0-9/+=]{44,}`,
	},

	// ── VCS / platform tokens ───────────────────────────────────────────
	{
		name:     "github-token",
		severity: ScanCritical,
		// ghp_ (PAT), gho_ (OAuth), ghu_ (user-to-server), ghs_ (server-to-server), ghr_ (refresh)
		pattern: `gh[pousr]_[A-Za-z0-9_]{36,}`,
	},
	{
		name:     "gitlab-token",
		severity: ScanHigh,
		pattern:  `glpat-[A-Za-z0-9_-]{20,}`,
	},

	// ── JWT ─────────────────────────────────────────────────────────────
	{
		name:     "jwt-token",
		severity: ScanHigh,
		pattern:  `eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`,
	},

	// ── Payment / fintech ───────────────────────────────────────────────
	{
		name:     "stripe-secret-key",
		severity: ScanCritical,
		// sk_live_ or sk_test_ or rk_live_ / rk_test_
		pattern: `(?:sk|rk)_(?:live|test)_[a-zA-Z0-9]{20,}`,
	},

	// ── Communication platforms ─────────────────────────────────────────
	{
		name:     "slack-token",
		severity: ScanHigh,
		pattern:  `xox[bpras]-[0-9a-zA-Z-]{10,}`,
	},
	{
		name:     "discord-bot-token",
		severity: ScanHigh,
		pattern:  `[MN][A-Za-z0-9]{23,}\.[A-Za-z0-9_-]{6}\.[A-Za-z0-9_-]{27,}`,
	},

	// ── Database connection strings ─────────────────────────────────────
	{
		name:     "connection-string",
		severity: ScanHigh,
		// postgresql://, mysql://, mongodb+srv://, redis://, amqp://
		pattern: `(?i)(?:postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis|amqp)://[^\s"']{10,}`,
	},

	// ── Crypto wallet keys ──────────────────────────────────────────────
	{
		name:     "bip32-xprv",
		severity: ScanCritical,
		pattern:  `xprv[a-zA-Z0-9]{100,}`,
	},

	// ── npm / package managers ──────────────────────────────────────────
	{
		name:     "npm-token",
		severity: ScanHigh,
		pattern:  `npm_[a-zA-Z0-9]{36}`,
	},

	// ── Sendgrid ────────────────────────────────────────────────────────
	{
		name:     "sendgrid-api-key",
		severity: ScanHigh,
		pattern:  `SG\.[a-zA-Z0-9_-]{22}\.[a-zA-Z0-9_-]{43}`,
	},

	// ── Twilio ──────────────────────────────────────────────────────────
	{
		name:     "twilio-api-key",
		severity: ScanHigh,
		pattern:  `SK[a-f0-9]{32}`,
	},

	// ── Generic sensitive assignments ───────────────────────────────────
	// These are lower-confidence patterns that match common config formats
	// where a secret value is being assigned.
	{
		name:     "bearer-token",
		severity: ScanHigh,
		pattern:  `(?i)bearer\s+[a-zA-Z0-9._~+/=-]{20,}`,
	},
	{
		name:     "secret-hex-assignment",
		severity: ScanHigh,
		pattern:  `(?i)(?:secret|token|credential|auth[_-]?key)\s*[:=]\s*[0-9a-f]{32,}`,
	},
	{
		name:     "password-assignment",
		severity: ScanMedium,
		pattern:  `(?i)(?:password|passwd)\s*[:=]\s*(?:"[^"]{8,}"|'[^']{8,}'|\S{8,})`,
	},

	// ── Inline private key material ─────────────────────────────────────
	{
		name:     "base64-private-key-blob",
		severity: ScanHigh,
		// Long base64 blob preceded by a key-like label
		pattern: `(?i)(?:private[_-]?key|secret[_-]?key)\s*[:=]\s*[A-Za-z0-9+/]{40,}={0,2}`,
	},
}

// HasCritical returns true if any finding has critical severity.
func (r ScanResult) HasCritical() bool {
	for _, f := range r.Findings {
		if f.Severity == ScanCritical {
			return true
		}
	}
	return false
}

// Summary returns a human-readable summary of findings.
func (r ScanResult) Summary() string {
	if r.Clean {
		return "clean"
	}
	names := r.FindingNames()
	return strings.Join(names, ", ")
}
