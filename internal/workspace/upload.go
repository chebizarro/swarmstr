package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Terminal upload staging (WS-A/A7 terminal.upload). Metiq deviation from
// OpenClaw: instead of staging into a host temp directory, the file is written
// into the PTY session's working directory under the same os.Root containment
// the session-files browser uses, so a hostile name can never escape the
// workspace.

const (
	// maxStagedUploadNameBytes bounds the sanitized on-disk file name.
	maxStagedUploadNameBytes = 180
	// maxUploadNameCollisionAttempts bounds the "-N" suffix probe so a
	// directory full of colliding names cannot loop forever.
	maxUploadNameCollisionAttempts = 100
)

var uploadForbiddenNameRunes = map[rune]struct{}{
	'<': {}, '>': {}, ':': {}, '"': {}, '/': {}, '\\': {}, '|': {}, '?': {}, '*': {}, '%': {}, '!': {},
}

var windowsReservedUploadName = regexp.MustCompile(`(?i)^(?:con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)`)

// UploadResult reports where one staged file landed.
type UploadResult struct {
	// Path is the absolute host path of the staged file.
	Path string
	// Size is the number of bytes written.
	Size int
}

// sanitizeUploadName reduces a client-provided file name to a safe basename:
// path separators are stripped, control and portability-hostile characters
// become underscores, Windows reserved names are prefixed, and the result is
// bounded and never empty (mirrors the OpenClaw staging sanitizer).
func sanitizeUploadName(name string) string {
	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	var b strings.Builder
	for _, r := range base {
		if r <= 0x1f || r == 0x7f {
			b.WriteRune('_')
			continue
		}
		if _, forbidden := uploadForbiddenNameRunes[r]; forbidden {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(r)
	}
	cleaned := strings.TrimRight(strings.TrimSpace(b.String()), ". ")
	if windowsReservedUploadName.MatchString(cleaned) {
		cleaned = "_" + cleaned
	}
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		cleaned = "upload"
	}
	cleaned = truncateUTF8(cleaned, maxStagedUploadNameBytes)
	if cleaned == "" {
		cleaned = "upload"
	}
	return cleaned
}

// truncateUTF8 shortens value to at most maxBytes without splitting a rune.
func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	var b strings.Builder
	bytes := 0
	for _, r := range value {
		next := len(string(r))
		if bytes+next > maxBytes {
			break
		}
		b.WriteRune(r)
		bytes += next
	}
	return b.String()
}

// uploadNameCandidate derives the Nth collision-avoidance candidate by
// inserting "-N" before the extension ("notes.txt" -> "notes-1.txt").
func uploadNameCandidate(base string, attempt int) string {
	if attempt == 0 {
		return base
	}
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if stem == "" {
		stem = base
		ext = ""
	}
	return fmt.Sprintf("%s-%d%s", stem, attempt, ext)
}

// StageTerminalUpload writes one uploaded file into dir under os.Root
// containment and returns the staged absolute path. Existing files are never
// overwritten: colliding names gain a numeric suffix.
func StageTerminalUpload(dir, name string, data []byte) (UploadResult, error) {
	root, canonical, err := openWorkspaceRoot(dir)
	if err != nil {
		return UploadResult{}, fmt.Errorf("terminal upload target unavailable")
	}
	defer func() { _ = root.Close() }()
	base := sanitizeUploadName(name)
	for attempt := 0; attempt < maxUploadNameCollisionAttempts; attempt++ {
		candidate := uploadNameCandidate(base, attempt)
		f, err := root.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return UploadResult{}, fmt.Errorf("terminal upload failed")
		}
		_, writeErr := f.Write(data)
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			_ = root.Remove(candidate)
			return UploadResult{}, fmt.Errorf("terminal upload failed")
		}
		return UploadResult{Path: filepath.Join(canonical, candidate), Size: len(data)}, nil
	}
	return UploadResult{}, fmt.Errorf("terminal upload failed: too many name conflicts")
}
