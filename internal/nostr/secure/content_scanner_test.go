package secure

import (
	"strings"
	"testing"
)

func TestContentScanner_EmptyInput(t *testing.T) {
	s := NewContentScanner()
	r := s.Scan("")
	if !r.Clean {
		t.Fatal("empty input should be clean")
	}
}

func TestContentScanner_NilScanner(t *testing.T) {
	var s *ContentScanner
	r := s.Scan("nsec1abc")
	if !r.Clean {
		t.Fatal("nil scanner should return clean")
	}
}

func TestContentScanner_NormalText(t *testing.T) {
	s := NewContentScanner()
	normals := []string{
		"Hello, how are you today?",
		"The weather in Portland is nice",
		"Let me check relay wss://relay.example.com for your profile",
		"Your pubkey is npub1abc123def456",
		"Event ID: abc123def456",
		"I found 42 results matching your search query",
		"The quick brown fox jumps over the lazy dog",
		"kind:30078 replaceable event for app state",
	}
	for _, text := range normals {
		r := s.Scan(text)
		if !r.Clean {
			t.Errorf("false positive on %q: %v", text, r.FindingNames())
		}
	}
}

// ── Nostr keys ──────────────────────────────────────────────────────────────

func TestContentScanner_NostrNsec(t *testing.T) {
	s := NewContentScanner()
	// A realistic nsec1 key (bech32-encoded, 63 chars total)
	nsec := "nsec1" + strings.Repeat("q", 58)
	r := s.Scan("my key is " + nsec)
	if r.Clean {
		t.Fatal("should detect nsec key")
	}
	assertFinding(t, r, "nostr-nsec", ScanCritical)
}

func TestContentScanner_NostrHexPrivkey(t *testing.T) {
	s := NewContentScanner()
	hex64 := strings.Repeat("a1b2c3d4", 8) // 64 hex chars
	r := s.Scan("private_key=" + hex64)
	if r.Clean {
		t.Fatal("should detect hex private key assignment")
	}
	assertFinding(t, r, "nostr-hex-privkey", ScanCritical)

	// Bare 64-char hex without context should NOT match (could be event ID or pubkey)
	r2 := s.Scan("event " + hex64 + " was published")
	for _, f := range r2.Findings {
		if f.PatternName == "nostr-hex-privkey" {
			t.Fatal("bare 64-char hex should not match nostr-hex-privkey")
		}
	}
}

// ── PEM keys ────────────────────────────────────────────────────────────────

func TestContentScanner_PEMPrivateKey(t *testing.T) {
	s := NewContentScanner()
	pems := []string{
		"-----BEGIN PRIVATE KEY-----",
		"-----BEGIN RSA PRIVATE KEY-----",
		"-----BEGIN EC PRIVATE KEY-----",
		"-----BEGIN OPENSSH PRIVATE KEY-----",
	}
	for _, pem := range pems {
		r := s.Scan("here is the key:\n" + pem + "\nMIIE...")
		if r.Clean {
			t.Errorf("should detect PEM key: %s", pem)
		}
		assertFinding(t, r, "pem-private-key", ScanCritical)
	}
}

// ── AI provider keys ────────────────────────────────────────────────────────

func TestContentScanner_OpenAIKey(t *testing.T) {
	s := NewContentScanner()
	keys := []string{
		"sk-" + strings.Repeat("a", 48),                    // classic format
		"sk-proj-" + strings.Repeat("b", 40),               // project-scoped
		"sk-" + strings.Repeat("Ab1Cd2Ef3Gh4Ij5Kl6Mn7", 3), // mixed case
	}
	for _, key := range keys {
		r := s.Scan("api key: " + key)
		if r.Clean {
			t.Errorf("should detect OpenAI key: %s", key[:20])
		}
		assertFinding(t, r, "openai-api-key", ScanCritical)
	}
}

func TestContentScanner_AnthropicKey(t *testing.T) {
	s := NewContentScanner()
	r := s.Scan("ANTHROPIC_API_KEY=sk-ant-abc123-" + strings.Repeat("x", 20))
	if r.Clean {
		t.Fatal("should detect Anthropic key")
	}
	assertFinding(t, r, "anthropic-api-key", ScanCritical)
}

// ── Cloud provider keys ─────────────────────────────────────────────────────

func TestContentScanner_AWSAccessKey(t *testing.T) {
	s := NewContentScanner()
	r := s.Scan("AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE")
	if r.Clean {
		t.Fatal("should detect AWS access key")
	}
	assertFinding(t, r, "aws-access-key-id", ScanCritical)
}

func TestContentScanner_GCPKey(t *testing.T) {
	s := NewContentScanner()
	r := s.Scan("key=AIzaSyC" + strings.Repeat("a", 32))
	if r.Clean {
		t.Fatal("should detect GCP API key")
	}
	assertFinding(t, r, "gcp-api-key", ScanHigh)
}

// ── Platform tokens ─────────────────────────────────────────────────────────

func TestContentScanner_GitHubToken(t *testing.T) {
	s := NewContentScanner()
	tokens := []string{
		"ghp_" + strings.Repeat("A", 36), // PAT
		"gho_" + strings.Repeat("B", 36), // OAuth
		"ghs_" + strings.Repeat("C", 36), // server-to-server
	}
	for _, tok := range tokens {
		r := s.Scan("token: " + tok)
		if r.Clean {
			t.Errorf("should detect GitHub token: %s", tok[:10])
		}
		assertFinding(t, r, "github-token", ScanCritical)
	}
}

func TestContentScanner_GitLabToken(t *testing.T) {
	s := NewContentScanner()
	r := s.Scan("GITLAB_TOKEN=glpat-" + strings.Repeat("x", 20))
	if r.Clean {
		t.Fatal("should detect GitLab token")
	}
	assertFinding(t, r, "gitlab-token", ScanHigh)
}

// ── JWT ─────────────────────────────────────────────────────────────────────

func TestContentScanner_JWT(t *testing.T) {
	s := NewContentScanner()
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	r := s.Scan("Authorization: Bearer " + jwt)
	if r.Clean {
		t.Fatal("should detect JWT")
	}
	assertFinding(t, r, "jwt-token", ScanHigh)
}

// ── Payment keys ────────────────────────────────────────────────────────────

func TestContentScanner_StripeKey(t *testing.T) {
	s := NewContentScanner()
	r := s.Scan("STRIPE_KEY=sk_live_" + strings.Repeat("a", 24))
	if r.Clean {
		t.Fatal("should detect Stripe key")
	}
	assertFinding(t, r, "stripe-secret-key", ScanCritical)
}

// ── Communication tokens ────────────────────────────────────────────────────

func TestContentScanner_SlackToken(t *testing.T) {
	s := NewContentScanner()
	r := s.Scan("SLACK_TOKEN=xoxb-1234567890-abcdefghij")
	if r.Clean {
		t.Fatal("should detect Slack token")
	}
	assertFinding(t, r, "slack-token", ScanHigh)
}

// ── Connection strings ──────────────────────────────────────────────────────

func TestContentScanner_ConnectionString(t *testing.T) {
	s := NewContentScanner()
	connStrs := []string{
		"postgresql://user:secret@host:5432/db",
		"mysql://root:password@localhost/mydb",
		"mongodb+srv://admin:pass@cluster.example.com/prod",
		"redis://default:mysecret@redis.example.com:6379",
	}
	for _, cs := range connStrs {
		r := s.Scan("DATABASE_URL=" + cs)
		if r.Clean {
			t.Errorf("should detect connection string: %s", cs[:30])
		}
		assertFinding(t, r, "connection-string", ScanHigh)
	}
}

// ── Crypto keys ─────────────────────────────────────────────────────────────

func TestContentScanner_BIP32XPRV(t *testing.T) {
	s := NewContentScanner()
	r := s.Scan("xprv" + strings.Repeat("a", 107))
	if r.Clean {
		t.Fatal("should detect BIP32 xprv key")
	}
	assertFinding(t, r, "bip32-xprv", ScanCritical)
}

// ── Generic patterns ────────────────────────────────────────────────────────

func TestContentScanner_BearerToken(t *testing.T) {
	s := NewContentScanner()
	r := s.Scan("Authorization: Bearer eyJhbGciOiJSUzI1NiIsIn")
	if r.Clean {
		t.Fatal("should detect bearer token")
	}
	assertFinding(t, r, "bearer-token", ScanHigh)
}

func TestContentScanner_PasswordAssignment(t *testing.T) {
	s := NewContentScanner()
	r := s.Scan(`password=SuperSecret123!`)
	if r.Clean {
		t.Fatal("should detect password assignment")
	}
	assertFinding(t, r, "password-assignment", ScanMedium)
}

func TestContentScanner_SecretHexAssignment(t *testing.T) {
	s := NewContentScanner()
	hex32 := strings.Repeat("ab", 16) // 32 hex chars
	r := s.Scan("secret=" + hex32)
	if r.Clean {
		t.Fatal("should detect secret hex assignment")
	}
	assertFinding(t, r, "secret-hex-assignment", ScanHigh)
}

// ── ScanStrings ─────────────────────────────────────────────────────────────

func TestContentScanner_ScanStrings(t *testing.T) {
	s := NewContentScanner()
	r := s.ScanStrings(
		"normal text",
		"AKIAIOSFODNN7EXAMPLE",
		"more normal text",
	)
	if r.Clean {
		t.Fatal("should detect AWS key across multiple strings")
	}
	if len(r.Findings) == 0 {
		t.Fatal("should have findings")
	}
}

// ── Result methods ──────────────────────────────────────────────────────────

func TestScanResult_FindingNames(t *testing.T) {
	r := ScanResult{
		Findings: []ScanFinding{
			{PatternName: "a"},
			{PatternName: "b"},
			{PatternName: "a"}, // duplicate
		},
	}
	names := r.FindingNames()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("expected [a, b], got %v", names)
	}
}

func TestScanResult_HasCritical(t *testing.T) {
	r := ScanResult{Findings: []ScanFinding{{Severity: ScanHigh}}}
	if r.HasCritical() {
		t.Fatal("no critical findings")
	}
	r.Findings = append(r.Findings, ScanFinding{Severity: ScanCritical})
	if !r.HasCritical() {
		t.Fatal("should have critical finding")
	}
}

func TestScanResult_Summary(t *testing.T) {
	r := ScanResult{Clean: true}
	if r.Summary() != "clean" {
		t.Fatalf("expected 'clean', got %q", r.Summary())
	}
	r = ScanResult{Findings: []ScanFinding{
		{PatternName: "x"},
		{PatternName: "y"},
	}}
	if r.Summary() != "x, y" {
		t.Fatalf("expected 'x, y', got %q", r.Summary())
	}
}

func TestContentScanner_PatternCount(t *testing.T) {
	s := NewContentScanner()
	if s.PatternCount() < 20 {
		t.Fatalf("expected at least 20 patterns, got %d", s.PatternCount())
	}
}

// ── False positive guardrails ───────────────────────────────────────────────

func TestContentScanner_NoFalsePositive_EventIDs(t *testing.T) {
	s := NewContentScanner()
	// 64-char hex strings are valid Nostr event IDs / pubkeys — should NOT trigger
	hex64 := strings.Repeat("a1b2c3d4", 8)
	texts := []string{
		"event id: " + hex64,
		"pubkey " + hex64 + " is active",
		hex64, // bare hex
	}
	for _, text := range texts {
		r := s.Scan(text)
		for _, f := range r.Findings {
			if f.PatternName == "nostr-hex-privkey" {
				t.Errorf("false positive on event ID/pubkey: %q triggered %s", text[:40], f.PatternName)
			}
		}
	}
}

func TestContentScanner_NoFalsePositive_RelayURLs(t *testing.T) {
	s := NewContentScanner()
	urls := []string{
		"wss://relay.example.com",
		"wss://relay2.example.com",
		"wss://relay.example.com",
	}
	for _, u := range urls {
		r := s.Scan(u)
		if !r.Clean {
			t.Errorf("false positive on relay URL %q: %v", u, r.FindingNames())
		}
	}
}

func TestContentScanner_NoFalsePositive_ShortWords(t *testing.T) {
	s := NewContentScanner()
	texts := []string{
		"skip this section",                 // contains "sk" prefix but is a word
		"the skeleton key was old",          // "sk" prefix
		"password policy requires 12 chars", // mentions password but no assignment
		"secret santa gift exchange",        // mentions secret but no assignment
	}
	for _, text := range texts {
		r := s.Scan(text)
		if !r.Clean {
			t.Errorf("false positive on %q: %v", text, r.FindingNames())
		}
	}
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func assertFinding(t *testing.T, r ScanResult, name, severity string) {
	t.Helper()
	for _, f := range r.Findings {
		if f.PatternName == name {
			if f.Severity != severity {
				t.Errorf("finding %q: expected severity %s, got %s", name, severity, f.Severity)
			}
			return
		}
	}
	t.Errorf("expected finding %q not found in: %v", name, r.FindingNames())
}
