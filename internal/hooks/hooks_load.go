package hooks

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadHookMD parses a HOOK.md file and returns a Hook.
// The hookKey is taken from the directory name.
func LoadHookMD(path string, src Source) (*Hook, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	fm, body, err := parseFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("HOOK.md frontmatter: %w", err)
	}

	// Pre-process JSON5 quirks (same format as SKILL.md).
	fm = preprocessFrontmatter(fm)

	var m HookManifest
	if err := yaml.Unmarshal(fm, &m); err != nil {
		return nil, fmt.Errorf("HOOK.md yaml: %w", err)
	}
	m.Body = string(bytes.TrimSpace(body))

	hookKey := filepath.Base(filepath.Dir(path))
	if m.Name == "" {
		m.Name = hookKey
	}

	return &Hook{
		HookKey:  hookKey,
		Manifest: m,
		Source:   src,
		FilePath: path,
		BaseDir:  filepath.Dir(path),
	}, nil
}

// ScanDir scans a directory for hooks.  Each immediate subdirectory that
// contains a HOOK.md file is treated as one hook.
// ScanDir scans a directory for hooks.  Each immediate subdirectory that
// contains a HOOK.md file is treated as one hook.
func ScanDir(dir string, src Source) ([]*Hook, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var hooks []*Hook
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		hookMD := filepath.Join(dir, e.Name(), "HOOK.md")
		if _, err := os.Stat(hookMD); os.IsNotExist(err) {
			continue
		}
		h, err := LoadHookMD(hookMD, src)
		if err != nil {
			continue // skip malformed entries
		}
		hooks = append(hooks, h)
	}
	return hooks, nil
}

// BundledHooksDir returns the directory containing bundled hooks.
// Resolution order:
//  1. METIQ_BUNDLED_HOOKS_DIR env
//  2. hooks/ sibling to the running binary
//  3. Walk up from cwd looking for hooks/ (dev mode)
//
// BundledHooksDir returns the directory containing bundled hooks.
// Resolution order:
//  1. METIQ_BUNDLED_HOOKS_DIR env
//  2. hooks/ sibling to the running binary
//  3. Walk up from cwd looking for hooks/ (dev mode)
func BundledHooksDir() string {
	if d := os.Getenv("METIQ_BUNDLED_HOOKS_DIR"); d != "" {
		return d
	}
	// Binary sibling
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "hooks")
		if looksLikeBundledHooksDir(candidate) {
			return candidate
		}
	}
	// Walk up from cwd (repo dev mode)
	cwd, _ := os.Getwd()
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(cwd, "hooks")
		if looksLikeBundledHooksDir(candidate) {
			return candidate
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			break
		}
		cwd = parent
	}
	return ""
}

func looksLikeBundledHooksDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "HOOK.md")); err == nil {
			return true
		}
	}
	return false
}

// ManagedHooksDir returns the directory for user-managed hooks.
// METIQ_MANAGED_HOOKS_DIR env overrides the default.
// ManagedHooksDir returns the directory for user-managed hooks.
// METIQ_MANAGED_HOOKS_DIR env overrides the default.
func ManagedHooksDir() string {
	if d := os.Getenv("METIQ_MANAGED_HOOKS_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".metiq", "hooks")
}

// ────────────────────────────────────────────────────────────────────────────
// YAML frontmatter helpers (mirrors skills package)
// ────────────────────────────────────────────────────────────────────────────

func parseFrontmatter(data []byte) (fm []byte, body []byte, err error) {
	data = bytes.TrimSpace(data)
	if !bytes.HasPrefix(data, []byte("---")) {
		return nil, data, nil
	}
	rest := data[3:]
	// Find closing ---
	idx := bytes.Index(rest, []byte("\n---"))
	if idx < 0 {
		return data, nil, nil
	}
	fm = rest[:idx]
	body = rest[idx+4:]
	if len(body) > 0 && body[0] == '\n' {
		body = body[1:]
	}
	return fm, body, nil
}

func preprocessFrontmatter(data []byte) []byte {
	data = joinFlowOnNextLine(data)
	for {
		next := trailingCommaPass(data)
		if bytes.Equal(next, data) {
			break
		}
		data = next
	}
	return data
}

func joinFlowOnNextLine(data []byte) []byte {
	lines := bytes.Split(data, []byte("\n"))
	out := make([][]byte, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := bytes.TrimRight(line, " \t")
		if bytes.HasSuffix(trimmed, []byte(":")) && i+1 < len(lines) {
			next := bytes.TrimLeft(lines[i+1], " \t")
			if bytes.HasPrefix(next, []byte("{")) || bytes.HasPrefix(next, []byte("[")) {
				joined := append(bytes.TrimRight(line, " \t"), ' ')
				joined = append(joined, next...)
				out = append(out, joined)
				i++
				continue
			}
		}
		out = append(out, line)
	}
	return bytes.Join(out, []byte("\n"))
}

func trailingCommaPass(data []byte) []byte {
	lines := bytes.Split(data, []byte("\n"))
	out := make([][]byte, len(lines))
	for i, line := range lines {
		stripped := bytes.TrimRight(line, " \t")
		if bytes.HasSuffix(stripped, []byte(",")) {
			// Look at next non-empty line.
			for j := i + 1; j < len(lines); j++ {
				next := bytes.TrimLeft(lines[j], " \t")
				if len(next) == 0 {
					continue
				}
				if bytes.HasPrefix(next, []byte("}")) || bytes.HasPrefix(next, []byte("]")) {
					stripped = stripped[:len(stripped)-1]
				}
				break
			}
			out[i] = stripped
		} else {
			out[i] = line
		}
	}
	return bytes.Join(out, []byte("\n"))
}

// ────────────────────────────────────────────────────────────────────────────
// Serialise hook status for RPC
// ────────────────────────────────────────────────────────────────────────────

// StatusToMap converts a HookStatus to a map[string]any for JSON-RPC responses.
