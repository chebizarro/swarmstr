// Package secrets implements secrets loading and resolution for swarmstr.
//
// Sources (in priority order):
//  1. Environment variables (always included, highest priority)
//  2. .env files configured in secrets.sources (or ~/.swarmstr/.env by default)
//
// Reference formats supported by Resolve:
//   - $VARNAME or ${VARNAME} — environment variable / loaded .env value
//   - env:VARNAME             — same as $VARNAME
//   - Plain string            — returned as-is (no substitution)
//
// Future: op://vault/item (1Password CLI), doppler://project/config/key
package secrets

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Store loads and caches secrets from .env files and the process environment.
type Store struct {
	mu      sync.RWMutex
	values  map[string]string // loaded secret values (env overrides .env)
	sources []string          // absolute paths to .env files
	loaded  int               // count from last reload
	warnings []string
}

// NewStore creates a Store with the given .env file paths.
// If no paths are given, the default path (~/.swarmstr/.env) is used.
func NewStore(paths []string) *Store {
	if len(paths) == 0 {
		paths = defaultPaths()
	}
	return &Store{
		sources: paths,
		values:  map[string]string{},
	}
}

func defaultPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, ".swarmstr", ".env")}
}

// Reload re-reads all source files and merges with live env.
// Returns the number of secrets loaded from files (not counting env pass-through).
func (s *Store) Reload() (int, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	newValues := map[string]string{}
	var warnings []string
	count := 0

	for _, src := range s.sources {
		kvs, err := parseEnvFile(src)
		if err != nil {
			if !os.IsNotExist(err) {
				warnings = append(warnings, fmt.Sprintf("secrets: %s: %v", src, err))
			}
			continue
		}
		for k, v := range kvs {
			if _, exists := newValues[k]; !exists {
				newValues[k] = v
				count++
			}
		}
	}

	s.values = newValues
	s.loaded = count
	s.warnings = warnings
	return count, warnings
}

// Resolve resolves a single secret reference string.
//
// Supported formats:
//   - "$VARNAME" / "${VARNAME}" / "env:VARNAME" → look up in env then .env files
//   - Plain string  → return as-is
//
// Returns the resolved value and whether it was found. If not found, value is
// the original ref and found=false.
func (s *Store) Resolve(ref string) (value string, found bool) {
	varName, ok := parseSecretRef(ref)
	if !ok {
		return ref, true // plain string
	}

	// Live env has highest priority.
	if v, ok := os.LookupEnv(varName); ok {
		return v, true
	}

	// Fallback to loaded .env values.
	s.mu.RLock()
	v, ok2 := s.values[varName]
	s.mu.RUnlock()
	if ok2 {
		return v, true
	}

	return ref, false
}

// ResolveMany resolves a list of refs and returns detailed results.
func (s *Store) ResolveMany(refs []string) []Resolution {
	out := make([]Resolution, len(refs))
	for i, ref := range refs {
		varName, isRef := parseSecretRef(ref)
		if !isRef {
			out[i] = Resolution{Ref: ref, VarName: "", Found: true, IsPlain: true}
			continue
		}
		v, found := s.Resolve(ref)
		out[i] = Resolution{
			Ref:     ref,
			VarName: varName,
			Found:   found,
			Value:   v,
		}
	}
	return out
}

// Count returns the number of secrets loaded from files on the last Reload.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loaded
}

// Warnings returns non-fatal warnings from the last Reload.
func (s *Store) Warnings() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string{}, s.warnings...)
}

// Resolution is the result of resolving a single reference.
type Resolution struct {
	Ref     string `json:"ref"`
	VarName string `json:"varName,omitempty"`
	Found   bool   `json:"found"`
	IsPlain bool   `json:"isPlain,omitempty"`
	// Value is intentionally omitted from JSON to avoid logging secrets.
	Value string `json:"-"`
}

// ────────────────────────────────────────────────────────────────────────────
// .env file parser
// ────────────────────────────────────────────────────────────────────────────

// parseEnvFile reads a dotenv-style KEY=VALUE file.
// Supports:
//   - Lines starting with # are comments
//   - export KEY=VALUE (strips 'export ')
//   - KEY="VALUE" or KEY='VALUE' (strips quotes)
//   - Empty lines are skipped
func parseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	result := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip leading "export " (common in .env files).
		line = strings.TrimPrefix(line, "export ")
		line = strings.TrimSpace(line)

		eqIdx := strings.IndexByte(line, '=')
		if eqIdx < 0 {
			continue // skip malformed lines
		}
		key := strings.TrimSpace(line[:eqIdx])
		val := strings.TrimSpace(line[eqIdx+1:])

		// Strip surrounding quotes (single or double).
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') ||
				(val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}

		if key != "" {
			result[key] = val
		}
	}
	return result, scanner.Err()
}

// ────────────────────────────────────────────────────────────────────────────
// Reference parsing
// ────────────────────────────────────────────────────────────────────────────

// parseSecretRef extracts the variable name from a ref like $VARNAME,
// ${VARNAME}, or env:VARNAME. Returns ("", false) if not a secret ref.
func parseSecretRef(ref string) (varName string, isRef bool) {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "${") && strings.HasSuffix(ref, "}") {
		return ref[2 : len(ref)-1], true
	}
	if strings.HasPrefix(ref, "$") {
		return ref[1:], true
	}
	if strings.HasPrefix(ref, "env:") {
		return ref[4:], true
	}
	return "", false
}
