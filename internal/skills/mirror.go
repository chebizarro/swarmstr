package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func SyncPromptSkillsToWorkspace(catalog *SkillCatalog) (map[string]string, error) {
	out := map[string]string{}
	if catalog == nil || strings.TrimSpace(catalog.WorkspaceDir) == "" {
		return out, nil
	}
	mirrorRoot := filepath.Join(catalog.WorkspaceDir, ".metiq", "skills")
	if err := os.MkdirAll(mirrorRoot, 0o755); err != nil {
		return out, err
	}
	workspaceReal, err := filepath.EvalSymlinks(catalog.WorkspaceDir)
	if err != nil {
		workspaceReal = catalog.WorkspaceDir
	}
	for _, resolved := range PromptVisibleSkills(catalog) {
		if resolved == nil || resolved.Skill == nil {
			continue
		}
		if isPathContained(workspaceReal, resolved.Skill.FilePath) {
			out[resolved.Skill.SkillKey] = resolved.Skill.FilePath
			continue
		}
		destDir := filepath.Join(mirrorRoot, mirrorDirName(resolved.Skill.SkillKey))
		if err := os.RemoveAll(destDir); err != nil {
			log.Printf("skills: mirror cleanup failed skill=%s err=%v", resolved.Skill.SkillKey, err)
			out[resolved.Skill.SkillKey] = resolved.Skill.FilePath
			continue
		}
		if err := copySkillTree(resolved.Skill.BaseDir, destDir); err != nil {
			log.Printf("skills: mirror sync failed skill=%s err=%v", resolved.Skill.SkillKey, err)
			out[resolved.Skill.SkillKey] = resolved.Skill.FilePath
			continue
		}
		out[resolved.Skill.SkillKey] = filepath.Join(destDir, "SKILL.md")
	}
	return out, nil
}

func sanitizeMirrorName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return "skill"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	trimmed := strings.Trim(b.String(), "-")
	if trimmed == "" {
		return "skill"
	}
	return trimmed
}

func mirrorDirName(skillKey string) string {
	slug := sanitizeMirrorName(skillKey)
	sum := sha256.Sum256([]byte(normalizedSkillKey(skillKey)))
	return fmt.Sprintf("%s-%s", slug, hex.EncodeToString(sum[:4]))
}

func copySkillTree(srcDir, destDir string) error {
	info, err := os.Stat(srcDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("skill source is not a directory: %s", srcDir)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(info.Name(), ".") && info.IsDir() {
			return filepath.SkipDir
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(destDir, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if info.Size() > MaxSkillFileBytes {
			return nil
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}
