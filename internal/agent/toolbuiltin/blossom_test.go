package toolbuiltin

import "testing"

// ─── mimeFromExt ──────────────────────────────────────────────────────────────

func TestMimeFromExt(t *testing.T) {
	tests := []struct{ ext, want string }{
		{".jpg", "image/jpeg"},
		{".jpeg", "image/jpeg"},
		{".JPG", "image/jpeg"},
		{".png", "image/png"},
		{".gif", "image/gif"},
		{".webp", "image/webp"},
		{".mp4", "video/mp4"},
		{".webm", "video/webm"},
		{".mp3", "audio/mpeg"},
		{".ogg", "audio/ogg"},
		{".pdf", "application/pdf"},
		{".txt", "text/plain"},
		{".md", "text/markdown"},
		{".json", "application/json"},
		{".xyz", "application/octet-stream"},
		{"", "application/octet-stream"},
	}
	for _, tt := range tests {
		got := mimeFromExt(tt.ext)
		if got != tt.want {
			t.Errorf("mimeFromExt(%q) = %q, want %q", tt.ext, got, tt.want)
		}
	}
}

// ─── isPathAllowed (blossom local) ────────────────────────────────────────────

func TestBlossomIsPathAllowed_NoRoots(t *testing.T) {
	if !isPathAllowed("/any/path", nil) {
		t.Error("nil roots should allow all")
	}
	if !isPathAllowed("/any/path", []string{}) {
		t.Error("empty roots should allow all")
	}
}

func TestBlossomIsPathAllowed_Inside(t *testing.T) {
	roots := []string{"/home/user/workspace"}
	if !isPathAllowed("/home/user/workspace/file.txt", roots) {
		t.Error("should be allowed")
	}
}

func TestBlossomIsPathAllowed_Outside(t *testing.T) {
	roots := []string{"/home/user/workspace"}
	if isPathAllowed("/etc/passwd", roots) {
		t.Error("should not be allowed")
	}
}
