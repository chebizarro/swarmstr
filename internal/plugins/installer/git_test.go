package installer

import "testing"

func TestNormalizeGitRepoSupportsGitHubShorthandAndRefs(t *testing.T) {
	repo, ref, err := normalizeGitRepo("owner/repo#v1.2.3")
	if err != nil {
		t.Fatalf("normalizeGitRepo: %v", err)
	}
	if repo != "https://github.com/owner/repo.git" || ref != "v1.2.3" {
		t.Fatalf("unexpected repo/ref: %q %q", repo, ref)
	}
}

func TestNormalizeGitRepoRejectsUnsafeScheme(t *testing.T) {
	if _, _, err := normalizeGitRepo("file:///tmp/plugin"); err == nil {
		t.Fatal("expected file:// repo rejection")
	}
}
