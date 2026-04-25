package devtools

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ─── Project Search ──────────────────────────────────────────────────────────

// FileSearchResult represents a file matching a search query.
type FileSearchResult struct {
	Path         string        `json:"path"`
	RelativePath string        `json:"relative_path"`
	IsDir        bool          `json:"is_dir"`
	Size         int64         `json:"size,omitempty"`
	Matches      []LineMatch   `json:"matches,omitempty"`
}

// LineMatch represents a line matching a content search.
type LineMatch struct {
	LineNumber int    `json:"line_number"`
	Content    string `json:"content"`
	Preview    string `json:"preview,omitempty"` // Truncated for display
}

// FileSearchOptions configures file search behavior.
type FileSearchOptions struct {
	// Pattern is a glob pattern for file names (e.g., "*.go").
	Pattern string

	// ContentPattern is a regex pattern to search within files.
	ContentPattern string

	// MaxResults limits the number of results (0 = unlimited).
	MaxResults int

	// IncludeHidden includes hidden files/directories.
	IncludeHidden bool

	// ExcludePatterns are glob patterns to exclude.
	ExcludePatterns []string

	// MaxDepth limits directory traversal depth (0 = unlimited).
	MaxDepth int

	// IncludeDirs includes directories in results.
	IncludeDirs bool

	// CaseSensitive for pattern matching.
	CaseSensitive bool
}

// DefaultFileSearchOptions returns sensible defaults.
func DefaultFileSearchOptions() FileSearchOptions {
	return FileSearchOptions{
		MaxResults:      100,
		IncludeHidden:   false,
		ExcludePatterns: []string{".git", "node_modules", "__pycache__", ".cache", "vendor"},
		MaxDepth:        0,
		IncludeDirs:     false,
		CaseSensitive:   true,
	}
}

// SearchFiles searches for files matching the given criteria.
func SearchFiles(ctx context.Context, rootDir string, opts FileSearchOptions) ([]FileSearchResult, error) {
	rootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}

	var results []FileSearchResult
	var contentRegex *regexp.Regexp

	if opts.ContentPattern != "" {
		flags := ""
		if !opts.CaseSensitive {
			flags = "(?i)"
		}
		contentRegex, err = regexp.Compile(flags + opts.ContentPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid content pattern: %w", err)
		}
	}

	err = filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip inaccessible paths
		}

		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		relPath, _ := filepath.Rel(rootDir, path)
		if relPath == "." {
			return nil
		}

		// Skip hidden files/directories unless requested
		name := d.Name()
		if !opts.IncludeHidden && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Check exclude patterns
		for _, excl := range opts.ExcludePatterns {
			if matched, _ := filepath.Match(excl, name); matched {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		// Check depth limit
		if opts.MaxDepth > 0 {
			depth := strings.Count(relPath, string(filepath.Separator)) + 1
			if depth > opts.MaxDepth {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		// Skip directories unless requested
		if d.IsDir() {
			if !opts.IncludeDirs {
				return nil
			}
		}

		// Check name pattern
		if opts.Pattern != "" {
			pattern := opts.Pattern
			if !opts.CaseSensitive {
				pattern = strings.ToLower(pattern)
				name = strings.ToLower(name)
			}
			if matched, _ := filepath.Match(pattern, name); !matched {
				return nil
			}
		}

		// Get file info
		info, err := d.Info()
		if err != nil {
			return nil
		}

		result := FileSearchResult{
			Path:         path,
			RelativePath: relPath,
			IsDir:        d.IsDir(),
			Size:         info.Size(),
		}

		// Search content if pattern specified and this is a file
		if contentRegex != nil && !d.IsDir() {
			matches, err := searchFileContent(path, contentRegex, 10) // Max 10 matches per file
			if err != nil || len(matches) == 0 {
				return nil // Skip files that don't match or can't be read
			}
			result.Matches = matches
		}

		results = append(results, result)

		// Check result limit
		if opts.MaxResults > 0 && len(results) >= opts.MaxResults {
			return filepath.SkipAll
		}

		return nil
	})

	if err != nil && err != filepath.SkipAll {
		return results, err
	}

	return results, nil
}

// searchFileContent searches for regex matches within a file.
func searchFileContent(path string, regex *regexp.Regexp, maxMatches int) ([]LineMatch, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var matches []LineMatch
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() && len(matches) < maxMatches {
		lineNum++
		line := scanner.Text()

		if regex.MatchString(line) {
			preview := line
			if len(preview) > 200 {
				preview = preview[:197] + "..."
			}
			matches = append(matches, LineMatch{
				LineNumber: lineNum,
				Content:    line,
				Preview:    preview,
			})
		}
	}

	return matches, scanner.Err()
}

// ─── Directory Tree ──────────────────────────────────────────────────────────

// TreeNode represents a node in a directory tree.
type TreeNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	IsDir    bool        `json:"is_dir"`
	Size     int64       `json:"size,omitempty"`
	Children []*TreeNode `json:"children,omitempty"`
}

// TreeOptions configures tree generation.
type TreeOptions struct {
	MaxDepth      int
	IncludeHidden bool
	ExcludeDirs   []string
	IncludeFiles  bool
	MaxFiles      int // Max files per directory
}

// DefaultTreeOptions returns sensible defaults.
func DefaultTreeOptions() TreeOptions {
	return TreeOptions{
		MaxDepth:      5,
		IncludeHidden: false,
		ExcludeDirs:   []string{".git", "node_modules", "__pycache__", ".cache", "vendor"},
		IncludeFiles:  true,
		MaxFiles:      50,
	}
}

// GenerateTree generates a directory tree structure.
func GenerateTree(ctx context.Context, rootDir string, opts TreeOptions) (*TreeNode, error) {
	rootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}

	info, err := os.Stat(rootDir)
	if err != nil {
		return nil, fmt.Errorf("stat root: %w", err)
	}

	root := &TreeNode{
		Name:  filepath.Base(rootDir),
		Path:  rootDir,
		IsDir: info.IsDir(),
	}

	if info.IsDir() {
		if err := buildTree(ctx, root, rootDir, opts, 0); err != nil {
			return root, err
		}
	}

	return root, nil
}

func buildTree(ctx context.Context, node *TreeNode, path string, opts TreeOptions, depth int) error {
	if opts.MaxDepth > 0 && depth >= opts.MaxDepth {
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil // Skip unreadable directories
	}

	// Sort: directories first, then files
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})

	fileCount := 0
	for _, entry := range entries {
		name := entry.Name()

		// Skip hidden
		if !opts.IncludeHidden && strings.HasPrefix(name, ".") {
			continue
		}

		// Skip excluded directories
		if entry.IsDir() {
			skip := false
			for _, excl := range opts.ExcludeDirs {
				if name == excl {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
		}

		// Skip files if not included or limit reached
		if !entry.IsDir() {
			if !opts.IncludeFiles {
				continue
			}
			fileCount++
			if opts.MaxFiles > 0 && fileCount > opts.MaxFiles {
				// Add a "..." placeholder
				node.Children = append(node.Children, &TreeNode{
					Name: fmt.Sprintf("... and %d more files", len(entries)-fileCount),
				})
				break
			}
		}

		childPath := filepath.Join(path, name)
		info, err := entry.Info()
		if err != nil {
			continue
		}

		child := &TreeNode{
			Name:  name,
			Path:  childPath,
			IsDir: entry.IsDir(),
			Size:  info.Size(),
		}

		if entry.IsDir() {
			if err := buildTree(ctx, child, childPath, opts, depth+1); err != nil {
				return err
			}
		}

		node.Children = append(node.Children, child)
	}

	return nil
}

// TreeToString converts a tree to an ASCII representation.
func TreeToString(node *TreeNode) string {
	var sb strings.Builder
	treeToStringHelper(&sb, node, "", true)
	return sb.String()
}

func treeToStringHelper(sb *strings.Builder, node *TreeNode, prefix string, isLast bool) {
	// Current node
	marker := "├── "
	if isLast {
		marker = "└── "
	}
	if prefix == "" {
		marker = ""
	}

	name := node.Name
	if node.IsDir {
		name += "/"
	}
	sb.WriteString(prefix + marker + name + "\n")

	// Children
	childPrefix := prefix
	if prefix != "" {
		if isLast {
			childPrefix += "    "
		} else {
			childPrefix += "│   "
		}
	}

	for i, child := range node.Children {
		isChildLast := i == len(node.Children)-1
		treeToStringHelper(sb, child, childPrefix, isChildLast)
	}
}

// ─── Symbol Search ───────────────────────────────────────────────────────────

// Symbol represents a code symbol (function, class, etc.).
type Symbol struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"` // function, class, method, variable, etc.
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Column     int    `json:"column,omitempty"`
	Container  string `json:"container,omitempty"` // Parent class/module
	Signature  string `json:"signature,omitempty"`
	Visibility string `json:"visibility,omitempty"` // public, private, etc.
}

// SymbolSearchOptions configures symbol search.
type SymbolSearchOptions struct {
	Pattern    string   // Symbol name pattern (glob)
	Kinds      []string // Filter by kind
	MaxResults int
}

// SearchSymbols searches for code symbols in Go files.
// This is a simplified implementation - a full LSP integration would be better.
func SearchSymbols(ctx context.Context, rootDir string, opts SymbolSearchOptions) ([]Symbol, error) {
	var symbols []Symbol

	// Simple Go symbol extraction using regex
	funcRegex := regexp.MustCompile(`^func\s+(?:\((\w+)\s+\*?(\w+)\)\s+)?(\w+)\s*\(`)
	typeRegex := regexp.MustCompile(`^type\s+(\w+)\s+(struct|interface)`)
	constVarRegex := regexp.MustCompile(`^(const|var)\s+(\w+)`)

	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Only process Go files for now
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		// Skip test files unless pattern matches
		if strings.HasSuffix(path, "_test.go") && opts.Pattern != "" {
			// Include test files only if explicitly searching for test patterns
		}

		fileSymbols, err := extractGoSymbols(path, funcRegex, typeRegex, constVarRegex)
		if err != nil {
			return nil // Skip files that can't be parsed
		}

		for _, sym := range fileSymbols {
			// Filter by pattern
			if opts.Pattern != "" {
				matched, _ := filepath.Match(opts.Pattern, sym.Name)
				if !matched {
					continue
				}
			}

			// Filter by kind
			if len(opts.Kinds) > 0 {
				found := false
				for _, k := range opts.Kinds {
					if sym.Kind == k {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}

			symbols = append(symbols, sym)

			if opts.MaxResults > 0 && len(symbols) >= opts.MaxResults {
				return filepath.SkipAll
			}
		}

		return nil
	})

	return symbols, err
}

func extractGoSymbols(path string, funcRe, typeRe, constVarRe *regexp.Regexp) ([]Symbol, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var symbols []Symbol
	scanner := bufio.NewScanner(file)
	lineNum := 0
	var currentType string

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Function/method
		if matches := funcRe.FindStringSubmatch(trimmed); matches != nil {
			sym := Symbol{
				Path: path,
				Line: lineNum,
				Kind: "function",
			}
			if matches[2] != "" {
				// Method
				sym.Name = matches[3]
				sym.Container = matches[2]
				sym.Kind = "method"
			} else {
				sym.Name = matches[3]
			}
			symbols = append(symbols, sym)
			continue
		}

		// Type definition
		if matches := typeRe.FindStringSubmatch(trimmed); matches != nil {
			currentType = matches[1]
			sym := Symbol{
				Name: matches[1],
				Path: path,
				Line: lineNum,
				Kind: matches[2], // "struct" or "interface"
			}
			symbols = append(symbols, sym)
			continue
		}

		// Const/var
		if matches := constVarRe.FindStringSubmatch(trimmed); matches != nil {
			sym := Symbol{
				Name:      matches[2],
				Path:      path,
				Line:      lineNum,
				Kind:      matches[1], // "const" or "var"
				Container: currentType,
			}
			symbols = append(symbols, sym)
		}
	}

	return symbols, scanner.Err()
}

// ─── Code Context ────────────────────────────────────────────────────────────

// CodeContext represents code around a specific location.
type CodeContext struct {
	Path       string   `json:"path"`
	StartLine  int      `json:"start_line"`
	EndLine    int      `json:"end_line"`
	Lines      []string `json:"lines"`
	TargetLine int      `json:"target_line,omitempty"`
}

// GetCodeContext retrieves code context around a specific line.
func GetCodeContext(path string, targetLine, contextLines int) (*CodeContext, error) {
	lines, err := ReadFileLines(path)
	if err != nil {
		return nil, err
	}

	start := targetLine - contextLines - 1
	if start < 0 {
		start = 0
	}
	end := targetLine + contextLines
	if end > len(lines) {
		end = len(lines)
	}

	return &CodeContext{
		Path:       path,
		StartLine:  start + 1,
		EndLine:    end,
		Lines:      lines[start:end],
		TargetLine: targetLine,
	}, nil
}

// GetFunctionContext retrieves the full function/method containing a line.
func GetFunctionContext(path string, line int) (*CodeContext, error) {
	lines, err := ReadFileLines(path)
	if err != nil {
		return nil, err
	}

	if line < 1 || line > len(lines) {
		return nil, fmt.Errorf("line %d out of range", line)
	}

	// Simple approach: find the enclosing func ... { } block
	// This is Go-specific and simplified

	funcStart := -1
	braceCount := 0
	inFunc := false

	// Scan backwards to find function start
	for i := line - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "func ") {
			funcStart = i
			break
		}
	}

	if funcStart == -1 {
		// No function found, return context around line
		return GetCodeContext(path, line, 10)
	}

	// Scan forward to find function end
	funcEnd := funcStart
	for i := funcStart; i < len(lines); i++ {
		l := lines[i]
		braceCount += strings.Count(l, "{") - strings.Count(l, "}")
		if braceCount == 0 && inFunc {
			funcEnd = i
			break
		}
		if braceCount > 0 {
			inFunc = true
		}
		funcEnd = i
	}

	return &CodeContext{
		Path:       path,
		StartLine:  funcStart + 1,
		EndLine:    funcEnd + 1,
		Lines:      lines[funcStart : funcEnd+1],
		TargetLine: line,
	}, nil
}
