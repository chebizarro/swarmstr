package skills

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// BundledSkillsDir returns the directory containing the bundled SKILL.md files.
// Resolution order:
//  1. METIQ_BUNDLED_SKILLS_DIR env var
//  2. A `skills/` directory next to the running executable
//  3. A `skills/` directory walked up from the current working directory (dev mode)
func BundledSkillsDir() string {
	// 1. Explicit override.
	if override := strings.TrimSpace(os.Getenv("METIQ_BUNDLED_SKILLS_DIR")); override != "" {
		return override
	}

	// 2. Sibling to the executable (production binary).
	if exe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), "skills")
		if looksLikeBundledSkillsDir(sibling) {
			return sibling
		}
	}

	// 3. Walk up from cwd looking for a `skills/` directory (dev / repo checkout).
	cwd, _ := os.Getwd()
	if cwd == "" {
		cwd = "."
	}
	current := cwd
	for depth := 0; depth < 8; depth++ {
		candidate := filepath.Join(current, "skills")
		if looksLikeBundledSkillsDir(candidate) {
			return candidate
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return ""
}

// looksLikeBundledSkillsDir returns true if dir contains at least one SKILL.md
// in a subdirectory — the canonical bundled skills structure.
// looksLikeBundledSkillsDir returns true if dir contains at least one SKILL.md
// in a subdirectory — the canonical bundled skills structure.
func looksLikeBundledSkillsDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "SKILL.md")); err == nil {
			return true
		}
	}
	return false
}

// ─── Scanners ─────────────────────────────────────────────────────────────────

// ScanBundledDir scans a bundled skills directory for SKILL.md files.
// Each immediate subdirectory is checked for a SKILL.md file.
// Returns all found skills sorted by skill key.
// ScanBundledDir scans a bundled skills directory for SKILL.md files.
// Each immediate subdirectory is checked for a SKILL.md file.
// Returns all found skills sorted by skill key.
func ScanBundledDir(dir string) ([]*Skill, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}
	rootReal, err := filepath.EvalSymlinks(dir)
	if err != nil {
		rootReal = dir
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read bundled skills dir %q: %w", dir, err)
	}

	var skills []*Skill
	for _, entry := range entries {
		fullPath := filepath.Join(dir, entry.Name())
		if !isDirOrSymlinkDir(fullPath, entry) || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if !isPathContained(rootReal, fullPath) {
			log.Printf("skills: skipping bundled skill outside root: %s", fullPath)
			continue
		}
		skillMDPath := filepath.Join(fullPath, "SKILL.md")
		if _, err := os.Stat(skillMDPath); err != nil {
			continue // no SKILL.md in this subdir, skip
		}
		s, err := LoadSkillMD(skillMDPath)
		if err != nil {
			log.Printf("skills: skipping bundled skill %s: %v", skillMDPath, err)
			continue // skip malformed skills
		}
		s.Bundled = true
		skills = append(skills, s)
	}

	sort.Slice(skills, func(i, j int) bool {
		return skills[i].SkillKey < skills[j].SkillKey
	})
	return skills, nil
}

// ScanWorkspace scans a workspace directory for skill files.
// It finds both SKILL.md files (in subdirectories) and legacy .yaml/.yml files (flat).
// Non-skill files and directories are silently skipped.
// ScanWorkspace scans a workspace directory for skill files.
// It finds both SKILL.md files (in subdirectories) and legacy .yaml/.yml files (flat).
// Non-skill files and directories are silently skipped.
func ScanWorkspace(dir string) ([]*Skill, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("workspace directory must not be empty")
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // empty workspace is not an error
		}
		return nil, fmt.Errorf("stat workspace: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace path %q is not a directory", dir)
	}

	rootReal, err := filepath.EvalSymlinks(dir)
	if err != nil {
		rootReal = dir
	}
	var skills []*Skill

	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		base := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(base, ".") || base == "node_modules" {
				return filepath.SkipDir
			}
			if !isPathContained(rootReal, path) {
				log.Printf("skills: skipping workspace directory outside root: %s", path)
				return filepath.SkipDir
			}
			// Check for SKILL.md in this subdirectory.
			skillMDPath := filepath.Join(path, "SKILL.md")
			if _, statErr := os.Stat(skillMDPath); statErr == nil {
				s, loadErr := LoadSkillMD(skillMDPath)
				if loadErr == nil {
					skills = append(skills, s)
				} else {
					log.Printf("skills: skipping workspace skill %s: %v", skillMDPath, loadErr)
				}
				return filepath.SkipDir // don't recurse further into skill dirs
			}
			return nil
		}
		// Legacy: flat .yaml/.yml skill files.
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".yaml" || ext == ".yml" {
			s, loadErr := LoadManifest(path)
			if loadErr == nil {
				skills = append(skills, s)
			} else {
				log.Printf("skills: skipping workspace manifest %s: %v", path, loadErr)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk workspace: %w", err)
	}

	sort.Slice(skills, func(i, j int) bool {
		return skills[i].SkillKey < skills[j].SkillKey
	})
	return skills, nil
}

// ─── Requirement checker ─────────────────────────────────────────────────────

// CheckRequirements verifies all requirement fields and returns which are missing.
// eligible is true when nothing is missing.
// WorkspaceDir resolves the agent workspace directory from config.
// Resolution order:
//  1. extra["skills"]["workspace"] config key
//  2. METIQ_WORKSPACE env var
//  3. ~/metiq/workspace/<agentID>
func WorkspaceDir(extra map[string]any, agentID string) string {
	if extra != nil {
		if rawSkills, ok := extra["skills"].(map[string]any); ok {
			if ws, ok := rawSkills["workspace"].(string); ok && strings.TrimSpace(ws) != "" {
				return strings.TrimSpace(ws)
			}
		}
	}
	if ws := os.Getenv("METIQ_WORKSPACE"); strings.TrimSpace(ws) != "" {
		return strings.TrimSpace(ws)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	if agentID == "" {
		agentID = "main"
	}
	return filepath.Join(home, "metiq", "workspace", agentID)
}

// ManagedSkillsDir returns the directory where installed/managed skills are stored.
// Resolution: METIQ_MANAGED_SKILLS_DIR env → ~/.metiq/skills
// ManagedSkillsDir returns the directory where installed/managed skills are stored.
// Resolution: METIQ_MANAGED_SKILLS_DIR env → ~/.metiq/skills
func ManagedSkillsDir() string {
	if d := strings.TrimSpace(os.Getenv("METIQ_MANAGED_SKILLS_DIR")); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	return filepath.Join(home, ".metiq", "skills")
}

// ─── Bins aggregate ──────────────────────────────────────────────────────────

// AggregateBins collects all unique bin names declared across a set of skills.
func isDirOrSymlinkDir(fullPath string, entry os.DirEntry) bool {
	if entry.IsDir() {
		return true
	}
	if entry.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(fullPath)
	return err == nil && info.IsDir()
}

func isPathContained(rootReal, candidate string) bool {
	candidateReal, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		candidateReal = candidate
	}
	rel, err := filepath.Rel(rootReal, candidateReal)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && rel != "..")
}
