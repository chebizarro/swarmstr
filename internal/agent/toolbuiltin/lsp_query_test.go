package toolbuiltin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ─── detectLSPLanguage ──────────────────────────────────────────────────────

func TestDetectLSPLanguage(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"main.go", "go"},
		{"lib.py", "python"},
		{"script.pyw", "python"},
		{"app.ts", "typescript"},
		{"app.tsx", "typescript"},
		{"index.js", "javascript"},
		{"index.jsx", "javascript"},
		{"index.mjs", "javascript"},
		{"index.cjs", "javascript"},
		{"lib.rs", "rust"},
		{"main.c", "c"},
		{"util.h", "c"},
		{"main.cpp", "cpp"},
		{"main.cxx", "cpp"},
		{"main.cc", "cpp"},
		{"util.hpp", "cpp"},
		{"util.hxx", "cpp"},
		{"README.md", ""},
		{"data.json", ""},
		{"Makefile", ""},
	}
	for _, tt := range tests {
		got := detectLSPLanguage(tt.path)
		if got != tt.want {
			t.Errorf("detectLSPLanguage(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// ─── lspServerCommand ───────────────────────────────────────────────────────

func TestLSPServerCommand(t *testing.T) {
	tests := []struct {
		lang    string
		wantCmd string
	}{
		{"go", "gopls"},
		{"python", "pyright-langserver"},
		{"typescript", "typescript-language-server"},
		{"javascript", "typescript-language-server"},
		{"rust", "rust-analyzer"},
		{"c", "clangd"},
		{"cpp", "clangd"},
		{"unknown", ""},
	}
	for _, tt := range tests {
		cmd, _ := lspServerCommand(tt.lang)
		if cmd != tt.wantCmd {
			t.Errorf("lspServerCommand(%q) cmd = %q, want %q", tt.lang, cmd, tt.wantCmd)
		}
	}
}

func TestLSPServerFallback(t *testing.T) {
	cmd, _ := lspServerFallback("python")
	if cmd != "pylsp" {
		t.Errorf("python fallback = %q, want pylsp", cmd)
	}
	cmd, _ = lspServerFallback("go")
	if cmd != "" {
		t.Errorf("go fallback = %q, want empty", cmd)
	}
}

// ─── URI conversion ─────────────────────────────────────────────────────────

func TestPathToFileURI(t *testing.T) {
	got := pathToFileURI("/home/user/project/main.go")
	if got != "file:///home/user/project/main.go" {
		t.Errorf("pathToFileURI = %q", got)
	}
}

func TestFileURIToPath(t *testing.T) {
	tests := []struct {
		uri  string
		want string
	}{
		{"file:///home/user/project/main.go", "/home/user/project/main.go"},
		{"/just/a/path", "/just/a/path"},
	}
	for _, tt := range tests {
		got := fileURIToPath(tt.uri)
		if got != tt.want {
			t.Errorf("fileURIToPath(%q) = %q, want %q", tt.uri, got, tt.want)
		}
	}
}

// ─── parseLSPLocations ─────────────────────────────────────────────────────

func TestParseLSPLocations_Single(t *testing.T) {
	raw := json.RawMessage(`{
		"uri": "file:///foo/bar.go",
		"range": {"start": {"line": 9, "character": 5}, "end": {"line": 9, "character": 15}}
	}`)
	locs, err := parseLSPLocations(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 1 {
		t.Fatalf("got %d locations, want 1", len(locs))
	}
	if locs[0].File != "/foo/bar.go" {
		t.Errorf("file = %q", locs[0].File)
	}
	if locs[0].Line != 10 {
		t.Errorf("line = %d, want 10 (1-based)", locs[0].Line)
	}
	if locs[0].Col != 6 {
		t.Errorf("col = %d, want 6 (1-based)", locs[0].Col)
	}
}

func TestParseLSPLocations_Array(t *testing.T) {
	raw := json.RawMessage(`[
		{"uri": "file:///a.go", "range": {"start": {"line": 0, "character": 0}, "end": {"line": 0, "character": 5}}},
		{"uri": "file:///b.go", "range": {"start": {"line": 4, "character": 2}, "end": {"line": 4, "character": 8}}}
	]`)
	locs, err := parseLSPLocations(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 2 {
		t.Fatalf("got %d locations, want 2", len(locs))
	}
	if locs[0].Line != 1 || locs[0].Col != 1 {
		t.Errorf("first: line=%d col=%d, want 1:1", locs[0].Line, locs[0].Col)
	}
	if locs[1].Line != 5 || locs[1].Col != 3 {
		t.Errorf("second: line=%d col=%d, want 5:3", locs[1].Line, locs[1].Col)
	}
}

func TestParseLSPLocations_Null(t *testing.T) {
	locs, err := parseLSPLocations(json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 0 {
		t.Errorf("expected empty, got %d", len(locs))
	}
}

func TestParseLSPLocations_Empty(t *testing.T) {
	locs, err := parseLSPLocations(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 0 {
		t.Errorf("expected empty, got %d", len(locs))
	}
}

// ─── parseLSPHover ──────────────────────────────────────────────────────────

func TestParseLSPHover_MarkupContent(t *testing.T) {
	raw := json.RawMessage(`{"contents": {"kind": "markdown", "value": "func Foo(x int) string"}}`)
	got, err := parseLSPHover(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != "func Foo(x int) string" {
		t.Errorf("hover = %q", got)
	}
}

func TestParseLSPHover_PlainString(t *testing.T) {
	raw := json.RawMessage(`{"contents": "hello world"}`)
	got, err := parseLSPHover(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello world" {
		t.Errorf("hover = %q", got)
	}
}

func TestParseLSPHover_MarkedString(t *testing.T) {
	raw := json.RawMessage(`{"contents": {"language": "go", "value": "type Foo struct{}"}}`)
	got, err := parseLSPHover(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != "type Foo struct{}" {
		t.Errorf("hover = %q", got)
	}
}

func TestParseLSPHover_Array(t *testing.T) {
	raw := json.RawMessage(`{"contents": [
		"Documentation for Foo",
		{"language": "go", "value": "func Foo()"}
	]}`)
	got, err := parseLSPHover(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Documentation for Foo\n\nfunc Foo()" {
		t.Errorf("hover = %q", got)
	}
}

func TestParseLSPHover_Null(t *testing.T) {
	got, err := parseLSPHover(json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "(no hover info)" {
		t.Errorf("hover = %q", got)
	}
}

// ─── parseLSPSymbols ────────────────────────────────────────────────────────

func TestParseLSPSymbols_DocumentSymbol(t *testing.T) {
	raw := json.RawMessage(`[
		{
			"name": "MyFunc",
			"kind": 12,
			"range": {"start": {"line": 5, "character": 0}, "end": {"line": 10, "character": 1}},
			"selectionRange": {"start": {"line": 5, "character": 5}, "end": {"line": 5, "character": 11}},
			"children": [
				{
					"name": "localVar",
					"kind": 13,
					"range": {"start": {"line": 6, "character": 1}, "end": {"line": 6, "character": 10}},
					"selectionRange": {"start": {"line": 6, "character": 1}, "end": {"line": 6, "character": 9}}
				}
			]
		}
	]`)
	syms, err := parseLSPSymbols(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 1 {
		t.Fatalf("got %d symbols, want 1", len(syms))
	}
	if syms[0].Name != "MyFunc" {
		t.Errorf("name = %q", syms[0].Name)
	}
	if syms[0].Kind != "function" {
		t.Errorf("kind = %q, want function", syms[0].Kind)
	}
	if syms[0].Line != 6 {
		t.Errorf("line = %d, want 6", syms[0].Line)
	}
	if syms[0].EndLine != 11 {
		t.Errorf("end_line = %d, want 11", syms[0].EndLine)
	}
	if len(syms[0].Children) != 1 {
		t.Fatalf("got %d children, want 1", len(syms[0].Children))
	}
	if syms[0].Children[0].Name != "localVar" {
		t.Errorf("child name = %q", syms[0].Children[0].Name)
	}
	if syms[0].Children[0].Kind != "variable" {
		t.Errorf("child kind = %q, want variable", syms[0].Children[0].Kind)
	}
}

func TestParseLSPSymbols_SymbolInformation(t *testing.T) {
	raw := json.RawMessage(`[
		{
			"name": "Foo",
			"kind": 5,
			"location": {
				"uri": "file:///test.go",
				"range": {"start": {"line": 2, "character": 0}, "end": {"line": 8, "character": 1}}
			}
		}
	]`)
	syms, err := parseLSPSymbols(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 1 {
		t.Fatalf("got %d symbols, want 1", len(syms))
	}
	if syms[0].Name != "Foo" || syms[0].Kind != "class" {
		t.Errorf("name=%q kind=%q, want Foo/class", syms[0].Name, syms[0].Kind)
	}
	if syms[0].Line != 3 {
		t.Errorf("line = %d, want 3", syms[0].Line)
	}
}

func TestParseLSPSymbols_Null(t *testing.T) {
	syms, err := parseLSPSymbols(json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 0 {
		t.Errorf("expected nil, got %d", len(syms))
	}
}

// ─── lspSymbolKindName ──────────────────────────────────────────────────────

func TestLSPSymbolKindName(t *testing.T) {
	tests := map[int]string{
		1:  "file",
		2:  "module",
		5:  "class",
		6:  "method",
		8:  "field",
		12: "function",
		13: "variable",
		14: "constant",
		23: "struct",
		26: "type_parameter",
		99: "kind_99",
	}
	for k, want := range tests {
		got := lspSymbolKindName(k)
		if got != want {
			t.Errorf("lspSymbolKindName(%d) = %q, want %q", k, got, want)
		}
	}
}

// ─── lspDiagSeverityName ────────────────────────────────────────────────────

func TestLSPDiagSeverityName(t *testing.T) {
	tests := map[int]string{
		1: "error",
		2: "warning",
		3: "info",
		4: "hint",
		0: "unknown",
		5: "unknown",
	}
	for sev, want := range tests {
		got := lspDiagSeverityName(sev)
		if got != want {
			t.Errorf("lspDiagSeverityName(%d) = %q, want %q", sev, got, want)
		}
	}
}

// ─── findLSPProjectRoot ─────────────────────────────────────────────────────

func TestFindLSPProjectRoot_GoMod(t *testing.T) {
	tmp := t.TempDir()
	subDir := filepath.Join(tmp, "pkg", "handlers")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(subDir, "handler.go")
	if err := os.WriteFile(file, []byte("package handlers\n"), 0644); err != nil {
		t.Fatal(err)
	}

	root := findLSPProjectRoot(file, "go")
	if root != tmp {
		t.Errorf("findLSPProjectRoot = %q, want %q", root, tmp)
	}
}

func TestFindLSPProjectRoot_PackageJSON(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src", "components")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(src, "App.tsx")
	if err := os.WriteFile(file, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	root := findLSPProjectRoot(file, "typescript")
	// tsconfig.json is checked first but doesn't exist; package.json is found.
	if root != tmp {
		t.Errorf("findLSPProjectRoot = %q, want %q", root, tmp)
	}
}

func TestFindLSPProjectRoot_NoMarker(t *testing.T) {
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "a", "b")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sub, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	root := findLSPProjectRoot(file, "go")
	// No go.mod found, should fall back to file's directory.
	if root != sub {
		t.Errorf("findLSPProjectRoot = %q, want %q (file dir fallback)", root, sub)
	}
}

func TestFindLSPProjectRoot_UnknownLang(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "data.csv")
	os.WriteFile(file, []byte(""), 0644)

	root := findLSPProjectRoot(file, "csv")
	if root != tmp {
		t.Errorf("findLSPProjectRoot = %q, want %q", root, tmp)
	}
}

// ─── lspLanguageID ──────────────────────────────────────────────────────────

func TestLSPLanguageID(t *testing.T) {
	tests := []struct {
		lang string
		want string
	}{
		{"go", "go"},
		{"python", "python"},
		{"typescript", "typescript"},
		{"rust", "rust"},
	}
	for _, tt := range tests {
		got := lspLanguageID(tt.lang)
		if got != tt.want {
			t.Errorf("lspLanguageID(%q) = %q, want %q", tt.lang, got, tt.want)
		}
	}
}

// ─── truncateLSPMsg ─────────────────────────────────────────────────────────

func TestTruncateLSPMsg_Short(t *testing.T) {
	raw := json.RawMessage(`{"hello": "world"}`)
	got := truncateLSPMsg(raw)
	if got != `{"hello": "world"}` {
		t.Errorf("truncate = %q", got)
	}
}

func TestTruncateLSPMsg_Long(t *testing.T) {
	// Build a 300-char message.
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'a'
	}
	got := truncateLSPMsg(json.RawMessage(long))
	if len(got) != 203 { // 200 + "..."
		t.Errorf("truncated length = %d, want 203", len(got))
	}
}

// ─── enrichLocation ─────────────────────────────────────────────────────────

func TestEnrichLocation(t *testing.T) {
	loc := lspLocation{
		URI: "file:///home/user/main.go",
		Range: lspRange{
			Start: lspPosition{Line: 9, Character: 4},
			End:   lspPosition{Line: 9, Character: 12},
		},
	}
	enrichLocation(&loc)
	if loc.File != "/home/user/main.go" {
		t.Errorf("file = %q", loc.File)
	}
	if loc.Line != 10 {
		t.Errorf("line = %d, want 10", loc.Line)
	}
	if loc.Col != 5 {
		t.Errorf("col = %d, want 5", loc.Col)
	}
}

// ─── NewLSPRegistry ─────────────────────────────────────────────────────────

func TestNewLSPRegistry(t *testing.T) {
	reg := NewLSPRegistry()
	if reg == nil {
		t.Fatal("NewLSPRegistry returned nil")
	}
	if len(reg.servers) != 0 {
		t.Errorf("expected empty servers map")
	}
	reg.Shutdown()
}
