package toolbuiltin

import "testing"

func TestIsPrivateHost(t *testing.T) {
	private := []string{
		"localhost", "127.0.0.1", "10.0.0.1",
		"192.168.1.1", "172.16.0.1", "172.31.255.255",
		"169.254.1.1", "::1",
		"localhost:8080", "127.0.0.1:9000",
	}
	for _, h := range private {
		if !IsPrivateHost(h) {
			t.Errorf("expected %q to be private", h)
		}
	}
	public := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34"}
	for _, h := range public {
		if IsPrivateHost(h) {
			t.Errorf("expected %q to be public", h)
		}
	}
}

func TestValidateFetchURL(t *testing.T) {
	// Valid http/https.
	for _, u := range []string{"https://93.184.216.34/page", "http://93.184.216.34/page"} {
		if err := ValidateFetchURL(u, false); err != nil {
			t.Errorf("unexpected error for %q: %v", u, err)
		}
	}
	// Reject ftp.
	if err := ValidateFetchURL("ftp://example.com/", false); err == nil {
		t.Error("expected error for ftp scheme")
	}
	// Reject empty.
	if err := ValidateFetchURL("", false); err == nil {
		t.Error("expected error for empty URL")
	}
	// SSRF: reject private without allowLocal.
	if err := ValidateFetchURL("http://127.0.0.1/", false); err == nil {
		t.Error("expected SSRF rejection for 127.0.0.1")
	}
	// Allow with allowLocal=true.
	if err := ValidateFetchURL("http://127.0.0.1/", true); err != nil {
		t.Errorf("expected no error with allowLocal=true: %v", err)
	}
}

func TestIsPathAllowed(t *testing.T) {
	// nil roots → allow everything.
	if !IsPathAllowed("/tmp/file.pdf", nil) {
		t.Error("nil roots: should allow all paths")
	}
	// Matching root.
	if !IsPathAllowed("/tmp/file.pdf", []string{"/tmp"}) {
		t.Error("should allow file under /tmp")
	}
	// Nested.
	if !IsPathAllowed("/tmp/subdir/file.pdf", []string{"/tmp"}) {
		t.Error("should allow nested file under /tmp")
	}
	// Non-matching root.
	if IsPathAllowed("/etc/passwd", []string{"/tmp"}) {
		t.Error("should reject /etc/passwd when root is /tmp")
	}
}

func TestTruncate(t *testing.T) {
	s := "hello world"
	if got := Truncate(s, 100); got != s {
		t.Errorf("expected no truncation, got %q", got)
	}
	got := Truncate(s, 5)
	if got != "hello\n[truncated]" {
		t.Errorf("unexpected truncation result: %q", got)
	}
	// maxChars=0 → no-op.
	if got := Truncate(s, 0); got != s {
		t.Error("maxChars=0 should not truncate")
	}
}
