package installer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestPluginURLRejectsNonPublicDestinations(t *testing.T) {
	for _, raw := range []string{
		"https://127.0.0.1/plugin.js",
		"https://10.0.0.1/plugin.js",
		"https://169.254.169.254/latest/meta-data",
		"https://[::1]/plugin.js",
		"https://[fe80::1]/plugin.js",
	} {
		if _, err := DownloadURL(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "blocked address") {
			t.Fatalf("DownloadURL(%q) error = %v, want blocked address", raw, err)
		}
	}
}

func TestDestinationGuardRejectsMixedDNSAnswers(t *testing.T) {
	old := installerLookupIP
	installerLookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("127.0.0.1")}}, nil
	}
	t.Cleanup(func() { installerLookupIP = old })
	if _, err := DownloadURL(context.Background(), "https://plugins.example/plugin.js"); err == nil || !strings.Contains(err.Error(), "blocked address") {
		t.Fatalf("mixed DNS answer error = %v", err)
	}
}

type verifierFunc func(context.Context, ArtifactVerificationInput) (*ArtifactVerification, error)

func (f verifierFunc) Verify(ctx context.Context, input ArtifactVerificationInput) (*ArtifactVerification, error) {
	return f(ctx, input)
}

func TestDownloadURLProvenanceAndPinnedDial(t *testing.T) {
	body := []byte("plugin artifact")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	useInstallerHTTPClient(t, srv.Client())

	result, err := DownloadURLWithOptions(context.Background(), srv.URL+"/plugin.js", DownloadOptions{RequireVerification: true, Verifier: verifierFunc(func(_ context.Context, input ArtifactVerificationInput) (*ArtifactVerification, error) {
		if input.Path == "" || input.Provenance.Artifact.Hash == "" {
			t.Fatal("verifier received incomplete provenance")
		}
		return &ArtifactVerification{Verifier: "test-signature", Identity: "test-key"}, nil
	})})
	if err != nil {
		t.Fatalf("DownloadURLWithOptions: %v", err)
	}
	defer os.Remove(result.Path)
	want := sha256.Sum256(body)
	if result.Provenance.Artifact.Hash != hex.EncodeToString(want[:]) || result.Provenance.Artifact.SizeBytes != int64(len(body)) {
		t.Fatalf("artifact provenance = %+v", result.Provenance.Artifact)
	}
	if len(result.Provenance.ResolvedHosts) != 1 || result.Provenance.ResolvedHosts[0].IPs[0] != "93.184.216.34" {
		t.Fatalf("resolved hosts = %+v", result.Provenance.ResolvedHosts)
	}
	if result.Provenance.Verification == nil || result.Provenance.Verification.Verifier != "test-signature" {
		t.Fatalf("verification = %+v", result.Provenance.Verification)
	}
}

func TestDownloadURLRequiredVerificationFailsWithoutVerifier(t *testing.T) {
	_, err := DownloadURLWithOptions(context.Background(), "https://plugins.example/plugin.js", DownloadOptions{RequireVerification: true})
	if err == nil || !strings.Contains(err.Error(), "verifier is required") {
		t.Fatalf("required verification error = %v", err)
	}
}

func TestSHA256ArtifactVerifierRejectsMismatch(t *testing.T) {
	verifier, err := NewSHA256ArtifactVerifier("sha256:" + strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	_, err = verifier.Verify(context.Background(), ArtifactVerificationInput{Provenance: InstallProvenance{
		Artifact: ArtifactDigest{Algorithm: "sha256", Hash: strings.Repeat("b", 64)},
	}})
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("digest mismatch error = %v", err)
	}
}

func TestRedirectToPrivateAddressFailsClosed(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://private.internal/private", http.StatusFound)
	}))
	defer srv.Close()
	useInstallerHTTPClient(t, srv.Client())
	installerLookupIP = func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host == "private.internal" {
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	if _, err := DownloadURL(context.Background(), srv.URL+"/start"); err == nil || !strings.Contains(err.Error(), "unsafe plugin redirect") {
		t.Fatalf("redirect error = %v", err)
	}
}
