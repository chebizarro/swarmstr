package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	MaxWorkspaceBootstrapFileBytes        = 2 * 1024 * 1024
	DefaultBootstrapMaxChars              = 20_000
	DefaultBootstrapTotalMaxChars         = 150_000
	DefaultBootstrapNearLimitRatio        = 0.85
	DefaultBootstrapPromptWarningMaxFiles = 3
	minBootstrapFileBudgetChars           = 64
	bootstrapHeadRatio                    = 0.7
	bootstrapTailRatio                    = 0.2
)

var recognizedBootstrapFiles = map[string]bool{
	"AGENTS.md":    true,
	"SOUL.md":      true,
	"TOOLS.md":     true,
	"IDENTITY.md":  true,
	"USER.md":      true,
	"HEARTBEAT.md": true,
	"BOOTSTRAP.md": true,
	"MEMORY.md":    true,
	"memory.md":    true,
}

func DefaultBootstrapFileNames() []string {
	return []string{"BOOTSTRAP.md", "SOUL.md", "IDENTITY.md", "USER.md", "AGENTS.md"}
}

type WorkspaceBootstrapFile struct {
	Name    string
	Path    string
	Content string
	Missing bool
}

type EmbeddedContextFile struct {
	Name    string
	Path    string
	Content string
}

type BootstrapInjectionStat struct {
	Name          string
	Path          string
	Missing       bool
	RawChars      int
	InjectedChars int
	Truncated     bool
	NearLimit     bool
}

type BootstrapBudgetAnalysis struct {
	Files                  []BootstrapInjectionStat
	TruncatedFiles         []BootstrapInjectionStat
	NearLimitFiles         []BootstrapInjectionStat
	TotalNearLimit         bool
	HasTruncation          bool
	TotalRawChars          int
	TotalInjectedChars     int
	TotalTruncatedChars    int
	BootstrapMaxChars      int
	BootstrapTotalMaxChars int
}

func IsRecognizedBootstrapFile(name string) bool {
	return recognizedBootstrapFiles[strings.TrimSpace(name)]
}

func LoadWorkspaceBootstrapFiles(workspaceDir string, names []string) ([]WorkspaceBootstrapFile, []string) {
	if len(names) == 0 {
		names = DefaultBootstrapFileNames()
	}
	resolved := make([]string, 0, len(names))
	for _, name := range names {
		base := filepath.Base(strings.TrimSpace(name))
		if base == "" || !IsRecognizedBootstrapFile(base) {
			continue
		}
		resolved = append(resolved, filepath.Join(workspaceDir, base))
	}
	return LoadResolvedBootstrapFiles(workspaceDir, resolved)
}

func LoadResolvedBootstrapFiles(workspaceDir string, resolvedPaths []string) ([]WorkspaceBootstrapFile, []string) {
	workspaceDir = strings.TrimSpace(workspaceDir)
	if workspaceDir == "" || len(resolvedPaths) == 0 {
		return nil, nil
	}
	realW, err := filepath.EvalSymlinks(workspaceDir)
	if err != nil {
		return nil, []string{fmt.Sprintf("bootstrap workspace unavailable: %v", err)}
	}
	seen := map[string]struct{}{}
	files := make([]WorkspaceBootstrapFile, 0, len(resolvedPaths))
	warnings := make([]string, 0)
	for _, rawPath := range resolvedPaths {
		pathValue := strings.TrimSpace(rawPath)
		if pathValue == "" {
			continue
		}
		base := filepath.Base(pathValue)
		if !IsRecognizedBootstrapFile(base) {
			warnings = append(warnings, fmt.Sprintf("skipping bootstrap file %q: unsupported basename", base))
			continue
		}
		realP, ferr := filepath.EvalSymlinks(pathValue)
		if ferr != nil {
			if os.IsNotExist(ferr) {
				continue
			}
			warnings = append(warnings, fmt.Sprintf("skipping bootstrap file %q: %v", base, ferr))
			continue
		}
		if !pathWithinRoot(realP, realW) {
			warnings = append(warnings, fmt.Sprintf("skipping bootstrap file %q: outside workspace boundary", base))
			continue
		}
		if _, dup := seen[realP]; dup {
			continue
		}
		seen[realP] = struct{}{}
		info, statErr := os.Stat(realP)
		if statErr != nil {
			warnings = append(warnings, fmt.Sprintf("skipping bootstrap file %q: %v", base, statErr))
			continue
		}
		if info.IsDir() {
			warnings = append(warnings, fmt.Sprintf("skipping bootstrap file %q: directories are not supported", base))
			continue
		}
		if info.Size() > MaxWorkspaceBootstrapFileBytes {
			warnings = append(warnings, fmt.Sprintf("skipping bootstrap file %q: %d bytes exceeds %d-byte load limit", base, info.Size(), MaxWorkspaceBootstrapFileBytes))
			continue
		}
		data, readErr := os.ReadFile(realP)
		if readErr != nil {
			warnings = append(warnings, fmt.Sprintf("skipping bootstrap file %q: %v", base, readErr))
			continue
		}
		files = append(files, WorkspaceBootstrapFile{
			Name:    base,
			Path:    realP,
			Content: string(data),
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, warnings
}

func BuildBootstrapContextFiles(files []WorkspaceBootstrapFile, warn func(string), maxChars, totalMaxChars int) []EmbeddedContextFile {
	if maxChars <= 0 {
		maxChars = DefaultBootstrapMaxChars
	}
	if totalMaxChars <= 0 {
		totalMaxChars = DefaultBootstrapTotalMaxChars
	}
	remainingTotalChars := totalMaxChars
	result := make([]EmbeddedContextFile, 0, len(files))
	for _, file := range files {
		if remainingTotalChars <= 0 {
			break
		}
		pathValue := strings.TrimSpace(file.Path)
		if pathValue == "" {
			if warn != nil {
				warn(fmt.Sprintf("skipping bootstrap file %q: missing path", file.Name))
			}
			continue
		}
		if file.Missing {
			missingText := clampBootstrapBudget("[MISSING] Expected at: "+pathValue, remainingTotalChars)
			if missingText == "" {
				break
			}
			remainingTotalChars -= len(missingText)
			result = append(result, EmbeddedContextFile{Name: file.Name, Path: pathValue, Content: missingText})
			continue
		}
		if remainingTotalChars < minBootstrapFileBudgetChars {
			if warn != nil {
				warn(fmt.Sprintf("remaining bootstrap budget is %d chars (<%d); skipping additional bootstrap files", remainingTotalChars, minBootstrapFileBudgetChars))
			}
			break
		}
		fileMaxChars := maxChars
		if fileMaxChars > remainingTotalChars {
			fileMaxChars = remainingTotalChars
		}
		trimmed, truncated, originalLength := trimBootstrapContent(file.Content, file.Name, fileMaxChars)
		contentWithinBudget := clampBootstrapBudget(trimmed, remainingTotalChars)
		if contentWithinBudget == "" {
			continue
		}
		if warn != nil && (truncated || len(contentWithinBudget) < len(trimmed)) {
			warn(fmt.Sprintf("workspace bootstrap file %s is %d chars (limit %d); truncating in injected context", file.Name, originalLength, fileMaxChars))
		}
		remainingTotalChars -= len(contentWithinBudget)
		result = append(result, EmbeddedContextFile{Name: file.Name, Path: pathValue, Content: contentWithinBudget})
	}
	return result
}

func BuildBootstrapInjectionStats(files []WorkspaceBootstrapFile, injectedFiles []EmbeddedContextFile) []BootstrapInjectionStat {
	injectedByPath := make(map[string]string, len(injectedFiles))
	injectedByBaseName := make(map[string]string, len(injectedFiles))
	for _, file := range injectedFiles {
		pathValue := strings.TrimSpace(file.Path)
		if pathValue == "" {
			continue
		}
		if _, ok := injectedByPath[pathValue]; !ok {
			injectedByPath[pathValue] = file.Content
		}
		baseName := filepath.Base(strings.ReplaceAll(pathValue, "\\", "/"))
		if _, ok := injectedByBaseName[baseName]; !ok {
			injectedByBaseName[baseName] = file.Content
		}
	}
	stats := make([]BootstrapInjectionStat, 0, len(files))
	for _, file := range files {
		pathValue := strings.TrimSpace(file.Path)
		rawChars := 0
		if !file.Missing {
			rawChars = len(strings.TrimRight(file.Content, "\n"))
		}
		injected := ""
		if pathValue != "" {
			injected = injectedByPath[pathValue]
		}
		if injected == "" {
			injected = injectedByPath[file.Name]
		}
		if injected == "" {
			injected = injectedByBaseName[file.Name]
		}
		injectedChars := len(injected)
		stats = append(stats, BootstrapInjectionStat{
			Name:          file.Name,
			Path:          pathValue,
			Missing:       file.Missing,
			RawChars:      rawChars,
			InjectedChars: injectedChars,
			Truncated:     !file.Missing && injectedChars < rawChars,
		})
	}
	return stats
}

func AnalyzeBootstrapBudget(files []BootstrapInjectionStat, bootstrapMaxChars, bootstrapTotalMaxChars int) BootstrapBudgetAnalysis {
	if bootstrapMaxChars <= 0 {
		bootstrapMaxChars = DefaultBootstrapMaxChars
	}
	if bootstrapTotalMaxChars <= 0 {
		bootstrapTotalMaxChars = DefaultBootstrapTotalMaxChars
	}
	analysis := BootstrapBudgetAnalysis{
		Files:                  make([]BootstrapInjectionStat, 0, len(files)),
		BootstrapMaxChars:      bootstrapMaxChars,
		BootstrapTotalMaxChars: bootstrapTotalMaxChars,
	}
	for _, file := range files {
		if file.Missing {
			analysis.Files = append(analysis.Files, file)
			continue
		}
		analysis.TotalRawChars += file.RawChars
		analysis.TotalInjectedChars += file.InjectedChars
		file.NearLimit = file.RawChars >= int(float64(bootstrapMaxChars)*DefaultBootstrapNearLimitRatio)
		analysis.Files = append(analysis.Files, file)
		if file.Truncated {
			analysis.TruncatedFiles = append(analysis.TruncatedFiles, file)
		}
		if file.NearLimit {
			analysis.NearLimitFiles = append(analysis.NearLimitFiles, file)
		}
	}
	analysis.TotalTruncatedChars = analysis.TotalRawChars - analysis.TotalInjectedChars
	if analysis.TotalTruncatedChars < 0 {
		analysis.TotalTruncatedChars = 0
	}
	analysis.TotalNearLimit = analysis.TotalInjectedChars >= int(float64(bootstrapTotalMaxChars)*DefaultBootstrapNearLimitRatio)
	analysis.HasTruncation = len(analysis.TruncatedFiles) > 0
	return analysis
}

func FormatBootstrapTruncationWarningLines(analysis BootstrapBudgetAnalysis, maxFiles int) []string {
	if !analysis.HasTruncation {
		return nil
	}
	if maxFiles <= 0 {
		maxFiles = DefaultBootstrapPromptWarningMaxFiles
	}
	topFiles := analysis.TruncatedFiles
	if len(topFiles) > maxFiles {
		topFiles = topFiles[:maxFiles]
	}
	lines := make([]string, 0, len(topFiles)+2)
	for _, file := range topFiles {
		pct := 0
		if file.RawChars > 0 {
			pct = int(float64(file.RawChars-file.InjectedChars) / float64(file.RawChars) * 100)
			if pct < 0 {
				pct = 0
			}
		}
		label := file.Name
		if strings.TrimSpace(file.Path) != "" {
			label = fmt.Sprintf("%s (%s)", file.Name, SanitizePromptLiteral(file.Path))
		}
		lines = append(lines, fmt.Sprintf("%s: %d raw -> %d injected (~%d%% removed).", label, file.RawChars, file.InjectedChars, pct))
	}
	if len(analysis.TruncatedFiles) > len(topFiles) {
		lines = append(lines, fmt.Sprintf("+%d more truncated file(s).", len(analysis.TruncatedFiles)-len(topFiles)))
	}
	lines = append(lines, "If unintentional, trim bootstrap files or raise the prompt budget settings.")
	return lines
}

func RenderBootstrapPromptContext(files []EmbeddedContextFile) string {
	parts := make([]string, 0, len(files))
	for _, file := range files {
		content := strings.TrimSpace(file.Content)
		if content == "" {
			continue
		}
		name := strings.TrimSpace(file.Name)
		if name == "" {
			name = filepath.Base(strings.TrimSpace(file.Path))
		}
		name = SanitizePromptLiteral(name)
		if name != "" {
			parts = append(parts, fmt.Sprintf("# %s\n\n%s", name, content))
			continue
		}
		parts = append(parts, content)
	}
	return strings.Join(parts, "\n\n---\n\n")
}

func trimBootstrapContent(content, fileName string, maxChars int) (string, bool, int) {
	trimmed := strings.TrimRight(content, "\n")
	if len(trimmed) <= maxChars {
		return trimmed, false, len(trimmed)
	}
	headChars := int(float64(maxChars) * bootstrapHeadRatio)
	tailChars := int(float64(maxChars) * bootstrapTailRatio)
	if headChars < 1 {
		headChars = 1
	}
	if tailChars < 0 {
		tailChars = 0
	}
	if headChars+tailChars > maxChars {
		tailChars = maxChars - headChars
		if tailChars < 0 {
			tailChars = 0
		}
	}
	head := trimmed[:headChars]
	tail := ""
	if tailChars > 0 && tailChars < len(trimmed) {
		tail = trimmed[len(trimmed)-tailChars:]
	}
	marker := strings.Join([]string{"", fmt.Sprintf("[...truncated, read %s for full content...]", fileName), fmt.Sprintf("…(truncated %s: kept %d+%d chars of %d)…", fileName, headChars, tailChars, len(trimmed)), ""}, "\n")
	joined := head + "\n" + marker
	if tail != "" {
		joined += "\n" + tail
	}
	return joined, true, len(trimmed)
}

func clampBootstrapBudget(content string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if len(content) <= budget {
		return content
	}
	if budget <= 3 {
		return content[:budget]
	}
	return content[:budget-3] + "..."
}

func pathWithinRoot(pathValue, root string) bool {
	rel, err := filepath.Rel(root, pathValue)
	if err != nil {
		return false
	}
	rel = filepath.Clean(rel)
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}
