// Package toolbuiltin/lsp_query provides an LSP-backed code intelligence tool:
//   - lsp_query → go-to-definition, find-references, hover, diagnostics, symbols
//
// The tool manages LSP server processes automatically (one per language+workspace),
// starting them lazily and reusing them across queries. Supported servers:
// gopls (Go), pyright-langserver (Python), typescript-language-server (TS/JS),
// rust-analyzer (Rust), clangd (C/C++).
package toolbuiltin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"metiq/internal/agent"
)

// ─── LSP Registry ────────────────────────────────────────────────────────────

// LSPRegistry manages LSP server instances, keyed by language + workspace root.
type LSPRegistry struct {
	mu       sync.Mutex
	servers  map[string]*lspServer // key: "lang:rootDir"
	bgCtx    context.Context
	bgCancel context.CancelFunc
}

// NewLSPRegistry creates a registry for managing LSP server lifecycles.
func NewLSPRegistry() *LSPRegistry {
	ctx, cancel := context.WithCancel(context.Background())
	return &LSPRegistry{
		servers:  make(map[string]*lspServer),
		bgCtx:    ctx,
		bgCancel: cancel,
	}
}

// Shutdown gracefully terminates all running LSP servers.
func (r *LSPRegistry) Shutdown() {
	r.bgCancel()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.servers {
		s.close()
	}
	r.servers = make(map[string]*lspServer)
}

func (r *LSPRegistry) getOrStart(ctx context.Context, lang, rootDir string) (*lspServer, error) {
	key := lang + ":" + rootDir

	r.mu.Lock()
	if s, ok := r.servers[key]; ok {
		r.mu.Unlock()
		// Check if still alive.
		select {
		case <-s.done:
			// Server died — remove and fall through to restart.
			r.mu.Lock()
			delete(r.servers, key)
			r.mu.Unlock()
		default:
			return s, nil
		}
	} else {
		r.mu.Unlock()
	}

	// Start a new server.
	s, err := startLSPServer(r.bgCtx, lang, rootDir)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.servers[key] = s
	r.mu.Unlock()

	return s, nil
}

// ─── LSP Server ──────────────────────────────────────────────────────────────

type lspServer struct {
	writeMu sync.Mutex // guards stdin writes
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	reader  *bufio.Reader
	rootDir string
	rootURI string
	lang    string
	nextID  atomic.Int64

	openMu sync.Mutex
	opened map[string]int // URI → version

	pendingMu sync.Mutex
	pending   map[int64]chan *lspResponse

	diagMu      sync.Mutex
	diagnostics map[string][]lspDiagnosticEntry // URI → diagnostics

	done chan struct{} // closed when readLoop exits
}

// ── JSON-RPC types ───────────────────────────────────────────────────────────

type lspRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type lspNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type lspResponse struct {
	ID     int64            `json:"id"`
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *lspResponseError `json:"error,omitempty"`
}

type lspResponseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ── LSP result types ─────────────────────────────────────────────────────────

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
	// Enriched output fields (1-based).
	File string `json:"file,omitempty"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspSymbolResult struct {
	Name     string            `json:"name"`
	Kind     string            `json:"kind"`
	Line     int               `json:"line"`     // 1-based
	EndLine  int               `json:"end_line"` // 1-based
	Children []lspSymbolResult `json:"children,omitempty"`
}

type lspDiagnosticEntry struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity"` // 1=Error 2=Warning 3=Info 4=Hint
	Message  string   `json:"message"`
	Source   string   `json:"source,omitempty"`
	Code     any      `json:"code,omitempty"`
}

// ─── Server lifecycle ────────────────────────────────────────────────────────

func startLSPServer(ctx context.Context, lang, rootDir string) (*lspServer, error) {
	cmdName, args := lspServerCommand(lang)
	if cmdName == "" {
		return nil, fmt.Errorf("no LSP server known for language %q", lang)
	}

	// Check if the primary binary exists; try fallback if not.
	if _, err := exec.LookPath(cmdName); err != nil {
		alt, altArgs := lspServerFallback(lang)
		if alt == "" {
			return nil, fmt.Errorf("LSP server %q not found in PATH; install it first", cmdName)
		}
		if _, err := exec.LookPath(alt); err != nil {
			return nil, fmt.Errorf("LSP servers %q and %q not found in PATH", cmdName, alt)
		}
		cmdName, args = alt, altArgs
	}

	cmd := exec.CommandContext(ctx, cmdName, args...)
	cmd.Dir = rootDir

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp stdin pipe: %v", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp stdout pipe: %v", err)
	}
	cmd.Stderr = io.Discard // prevent buffer fill-up

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("lsp start %s: %v", cmdName, err)
	}

	s := &lspServer{
		cmd:         cmd,
		stdin:       stdinPipe,
		reader:      bufio.NewReaderSize(stdoutPipe, 64*1024),
		rootDir:     rootDir,
		rootURI:     pathToFileURI(rootDir),
		lang:        lang,
		opened:      make(map[string]int),
		pending:     make(map[int64]chan *lspResponse),
		diagnostics: make(map[string][]lspDiagnosticEntry),
		done:        make(chan struct{}),
	}

	go s.readLoop()

	// Send initialize + initialized handshake.
	if err := s.initialize(ctx); err != nil {
		s.close()
		return nil, fmt.Errorf("lsp initialize: %v", err)
	}

	return s, nil
}

func (s *lspServer) close() {
	// Attempt graceful LSP shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_, _ = s.sendRequest(ctx, "shutdown", nil)
	cancel()
	_ = s.sendNotify("exit", nil)
	s.stdin.Close()

	timer := time.NewTimer(2 * time.Second)
	select {
	case <-s.done:
	case <-timer.C:
		if s.cmd.Process != nil {
			s.cmd.Process.Kill()
		}
	}
	timer.Stop()
	_ = s.cmd.Wait()
}

// ─── JSON-RPC transport ─────────────────────────────────────────────────────

func (s *lspServer) readLoop() {
	defer close(s.done)
	for {
		msg, err := s.readMessage()
		if err != nil {
			return
		}
		s.handleMessage(msg)
	}
}

func (s *lspServer) readMessage() ([]byte, error) {
	contentLength := 0
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // blank line separates headers from body
		}
		if strings.HasPrefix(line, "Content-Length:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
			if n, err := strconv.Atoi(val); err == nil {
				contentLength = n
			}
		}
	}
	if contentLength == 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	body := make([]byte, contentLength)
	_, err := io.ReadFull(s.reader, body)
	return body, err
}

func (s *lspServer) writeMessage(data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, err := io.WriteString(s.stdin, header); err != nil {
		return err
	}
	_, err := s.stdin.Write(data)
	return err
}

func (s *lspServer) handleMessage(msg []byte) {
	var envelope struct {
		ID     *json.RawMessage `json:"id"`
		Method string           `json:"method"`
		Result json.RawMessage  `json:"result"`
		Error  *lspResponseError `json:"error"`
		Params json.RawMessage  `json:"params"`
	}
	if err := json.Unmarshal(msg, &envelope); err != nil {
		return
	}

	// Server-to-client request (has both id AND method): send a default response.
	if envelope.ID != nil && envelope.Method != "" {
		var id int64
		if json.Unmarshal(*envelope.ID, &id) == nil {
			s.respondToServerRequest(id, envelope.Method, envelope.Params)
		}
		return
	}

	// Response to one of our requests (has id, no method).
	if envelope.ID != nil {
		var id int64
		if json.Unmarshal(*envelope.ID, &id) == nil {
			resp := &lspResponse{ID: id, Result: envelope.Result, Error: envelope.Error}
			s.pendingMu.Lock()
			if ch, ok := s.pending[id]; ok {
				ch <- resp
				delete(s.pending, id)
			}
			s.pendingMu.Unlock()
		}
		return
	}

	// Notification from server (no id).
	if envelope.Method == "textDocument/publishDiagnostics" && envelope.Params != nil {
		var params struct {
			URI         string               `json:"uri"`
			Diagnostics []lspDiagnosticEntry `json:"diagnostics"`
		}
		if json.Unmarshal(envelope.Params, &params) == nil {
			s.diagMu.Lock()
			s.diagnostics[params.URI] = params.Diagnostics
			s.diagMu.Unlock()
		}
	}
}

// respondToServerRequest handles server→client requests that require a response.
func (s *lspServer) respondToServerRequest(id int64, method string, params json.RawMessage) {
	var result any

	switch method {
	case "workspace/configuration":
		// Return an array of empty config objects matching the number of items requested.
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		if json.Unmarshal(params, &p) == nil {
			items := make([]any, len(p.Items))
			for i := range items {
				items[i] = map[string]any{}
			}
			result = items
		}
	case "window/workDoneProgress/create":
		result = nil // acknowledge
	case "client/registerCapability":
		result = nil // acknowledge
	default:
		result = nil
	}

	resp := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
	data, _ := json.Marshal(resp)
	_ = s.writeMessage(data)
}

func (s *lspServer) sendRequest(ctx context.Context, method string, params any) (*lspResponse, error) {
	id := s.nextID.Add(1)

	ch := make(chan *lspResponse, 1)
	s.pendingMu.Lock()
	s.pending[id] = ch
	s.pendingMu.Unlock()

	req := lspRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	data, err := json.Marshal(req)
	if err != nil {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return nil, err
	}

	if err := s.writeMessage(data); err != nil {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return nil, err
	}

	const rpcTimeout = 30 * time.Second
	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(rpcTimeout):
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return nil, fmt.Errorf("LSP %s timed out after %v", method, rpcTimeout)
	case <-ctx.Done():
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return nil, ctx.Err()
	case <-s.done:
		return nil, fmt.Errorf("LSP server exited during %s", method)
	}
}

func (s *lspServer) sendNotify(method string, params any) error {
	notif := lspNotification{JSONRPC: "2.0", Method: method, Params: params}
	data, err := json.Marshal(notif)
	if err != nil {
		return err
	}
	return s.writeMessage(data)
}

// ─── LSP initialization ─────────────────────────────────────────────────────

func (s *lspServer) initialize(ctx context.Context) error {
	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   s.rootURI,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"definition":     map[string]any{},
				"references":     map[string]any{},
				"hover":          map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
				"documentSymbol": map[string]any{},
				"publishDiagnostics": map[string]any{
					"relatedInformation": true,
				},
			},
			"workspace": map[string]any{
				"configuration": true,
			},
		},
	}

	resp, err := s.sendRequest(ctx, "initialize", params)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("initialize error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	return s.sendNotify("initialized", map[string]any{})
}

// ─── Document management ────────────────────────────────────────────────────

func (s *lspServer) ensureOpen(uri, langID, content string) error {
	s.openMu.Lock()
	version, isOpen := s.opened[uri]
	if isOpen {
		version++
		s.opened[uri] = version
		s.openMu.Unlock()
		// Send didChange with full content sync.
		return s.sendNotify("textDocument/didChange", map[string]any{
			"textDocument": map[string]any{
				"uri":     uri,
				"version": version,
			},
			"contentChanges": []map[string]any{
				{"text": content},
			},
		})
	}
	s.opened[uri] = 1
	s.openMu.Unlock()

	return s.sendNotify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": langID,
			"version":    1,
			"text":       content,
		},
	})
}

// ─── LSP query methods ──────────────────────────────────────────────────────

func (s *lspServer) definition(ctx context.Context, uri string, line, char int) ([]lspLocation, error) {
	resp, err := s.sendRequest(ctx, "textDocument/definition", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": char},
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("LSP error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return parseLSPLocations(resp.Result)
}

func (s *lspServer) references(ctx context.Context, uri string, line, char int) ([]lspLocation, error) {
	resp, err := s.sendRequest(ctx, "textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": char},
		"context":      map[string]any{"includeDeclaration": true},
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("LSP error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return parseLSPLocations(resp.Result)
}

func (s *lspServer) hover(ctx context.Context, uri string, line, char int) (string, error) {
	resp, err := s.sendRequest(ctx, "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": char},
	})
	if err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("LSP error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return parseLSPHover(resp.Result)
}

func (s *lspServer) symbols(ctx context.Context, uri string) ([]lspSymbolResult, error) {
	resp, err := s.sendRequest(ctx, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("LSP error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return parseLSPSymbols(resp.Result)
}

func (s *lspServer) getDiagnostics(uri string) []lspDiagnosticEntry {
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	diags := s.diagnostics[uri]
	out := make([]lspDiagnosticEntry, len(diags))
	copy(out, diags)
	return out
}

// ─── Response parsers ────────────────────────────────────────────────────────

func parseLSPLocations(raw json.RawMessage) ([]lspLocation, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	// Try as array first (most common).
	var locs []lspLocation
	if err := json.Unmarshal(raw, &locs); err == nil {
		for i := range locs {
			enrichLocation(&locs[i])
		}
		return locs, nil
	}

	// Try as single location.
	var loc lspLocation
	if err := json.Unmarshal(raw, &loc); err != nil {
		return nil, fmt.Errorf("cannot parse location(s): %s", truncateLSPMsg(raw))
	}
	enrichLocation(&loc)
	return []lspLocation{loc}, nil
}

func enrichLocation(l *lspLocation) {
	l.File = fileURIToPath(l.URI)
	l.Line = l.Range.Start.Line + 1
	l.Col = l.Range.Start.Character + 1
}

func parseLSPHover(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "(no hover info)", nil
	}

	var hover struct {
		Contents json.RawMessage `json:"contents"`
	}
	if err := json.Unmarshal(raw, &hover); err != nil {
		return "", err
	}

	// Try MarkupContent { kind, value }.
	var markup struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
	if json.Unmarshal(hover.Contents, &markup) == nil && markup.Value != "" {
		return markup.Value, nil
	}

	// Try plain string.
	var str string
	if json.Unmarshal(hover.Contents, &str) == nil && str != "" {
		return str, nil
	}

	// Try MarkedString { language, value }.
	var marked struct {
		Language string `json:"language"`
		Value    string `json:"value"`
	}
	if json.Unmarshal(hover.Contents, &marked) == nil && marked.Value != "" {
		return marked.Value, nil
	}

	// Try array of strings / MarkedStrings.
	var arr []json.RawMessage
	if json.Unmarshal(hover.Contents, &arr) == nil {
		var parts []string
		for _, item := range arr {
			var s string
			if json.Unmarshal(item, &s) == nil {
				parts = append(parts, s)
				continue
			}
			var ms struct {
				Language string `json:"language"`
				Value    string `json:"value"`
			}
			if json.Unmarshal(item, &ms) == nil && ms.Value != "" {
				parts = append(parts, ms.Value)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n\n"), nil
		}
	}

	return string(hover.Contents), nil
}

func parseLSPSymbols(raw json.RawMessage) ([]lspSymbolResult, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	// Peek at the first element to distinguish DocumentSymbol vs SymbolInformation.
	// DocumentSymbol has "selectionRange"; SymbolInformation has "location".
	var peek []map[string]json.RawMessage
	if json.Unmarshal(raw, &peek) != nil || len(peek) == 0 {
		return nil, fmt.Errorf("cannot parse symbols response")
	}

	if _, hasLocation := peek[0]["location"]; hasLocation {
		// SymbolInformation[] (flat).
		var symInfos []struct {
			Name     string      `json:"name"`
			Kind     int         `json:"kind"`
			Location lspLocation `json:"location"`
		}
		if err := json.Unmarshal(raw, &symInfos); err != nil {
			return nil, fmt.Errorf("cannot parse SymbolInformation: %v", err)
		}
		var result []lspSymbolResult
		for _, si := range symInfos {
			result = append(result, lspSymbolResult{
				Name:    si.Name,
				Kind:    lspSymbolKindName(si.Kind),
				Line:    si.Location.Range.Start.Line + 1,
				EndLine: si.Location.Range.End.Line + 1,
			})
		}
		return result, nil
	}

	// DocumentSymbol[] (hierarchical).
	var docSymbols []struct {
		Name           string          `json:"name"`
		Kind           int             `json:"kind"`
		Range          lspRange        `json:"range"`
		SelectionRange lspRange        `json:"selectionRange"`
		Children       json.RawMessage `json:"children,omitempty"`
	}
	if err := json.Unmarshal(raw, &docSymbols); err != nil {
		return nil, fmt.Errorf("cannot parse DocumentSymbol: %v", err)
	}
	var result []lspSymbolResult
	for _, ds := range docSymbols {
		sym := lspSymbolResult{
			Name:    ds.Name,
			Kind:    lspSymbolKindName(ds.Kind),
			Line:    ds.Range.Start.Line + 1,
			EndLine: ds.Range.End.Line + 1,
		}
		if len(ds.Children) > 0 {
			sym.Children, _ = parseLSPSymbols(ds.Children)
		}
		result = append(result, sym)
	}
	return result, nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func pathToFileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return "file://" + abs
}

func fileURIToPath(uri string) string {
	if strings.HasPrefix(uri, "file://") {
		return strings.TrimPrefix(uri, "file://")
	}
	return uri
}

func lspServerCommand(lang string) (string, []string) {
	switch lang {
	case "go":
		return "gopls", nil
	case "python":
		return "pyright-langserver", []string{"--stdio"}
	case "typescript", "javascript":
		return "typescript-language-server", []string{"--stdio"}
	case "rust":
		return "rust-analyzer", nil
	case "c", "cpp":
		return "clangd", nil
	default:
		return "", nil
	}
}

func lspServerFallback(lang string) (string, []string) {
	switch lang {
	case "python":
		return "pylsp", nil
	default:
		return "", nil
	}
}

func detectLSPLanguage(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		return "go"
	case ".py", ".pyw":
		return "python"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".rs":
		return "rust"
	case ".c", ".h":
		return "c"
	case ".cpp", ".cxx", ".cc", ".hpp", ".hxx":
		return "cpp"
	default:
		return ""
	}
}

func lspLanguageID(lang string) string {
	// LSP language identifiers (most are same as our internal names).
	return lang
}

func findLSPProjectRoot(filePath, lang string) string {
	dir := filepath.Dir(filePath)
	markers := map[string][]string{
		"go":         {"go.mod"},
		"python":     {"pyproject.toml", "setup.py", "setup.cfg"},
		"typescript": {"tsconfig.json", "package.json"},
		"javascript": {"package.json", "jsconfig.json"},
		"rust":       {"Cargo.toml"},
		"c":          {"CMakeLists.txt", "Makefile", "compile_commands.json"},
		"cpp":        {"CMakeLists.txt", "Makefile", "compile_commands.json"},
	}

	files := markers[lang]
	if len(files) == 0 {
		return dir
	}

	current := dir
	for {
		for _, f := range files {
			if _, err := os.Stat(filepath.Join(current, f)); err == nil {
				return current
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			break // reached filesystem root
		}
		current = parent
	}
	return dir // fallback to file's directory
}

func lspSymbolKindName(kind int) string {
	names := map[int]string{
		1: "file", 2: "module", 3: "namespace", 4: "package",
		5: "class", 6: "method", 7: "property", 8: "field",
		9: "constructor", 10: "enum", 11: "interface", 12: "function",
		13: "variable", 14: "constant", 15: "string", 16: "number",
		17: "boolean", 18: "array", 19: "object", 20: "key",
		21: "null", 22: "enum_member", 23: "struct", 24: "event",
		25: "operator", 26: "type_parameter",
	}
	if name, ok := names[kind]; ok {
		return name
	}
	return fmt.Sprintf("kind_%d", kind)
}

func lspDiagSeverityName(sev int) string {
	switch sev {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "info"
	case 4:
		return "hint"
	default:
		return "unknown"
	}
}

func truncateLSPMsg(raw json.RawMessage) string {
	s := string(raw)
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

// ─── Tool function ───────────────────────────────────────────────────────────

// LSPQueryTool returns a ToolFunc that queries LSP servers for code intelligence.
func LSPQueryTool(reg *LSPRegistry, opts FilesystemOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		action := agent.ArgString(args, "action")
		filePath := agent.ArgString(args, "file")

		if action == "" {
			return "", fmt.Errorf("lsp_query: 'action' is required")
		}
		if filePath == "" {
			return "", fmt.Errorf("lsp_query: 'file' is required")
		}

		// Resolve workspace-relative path.
		resolved, err := opts.resolvePath(filePath)
		if err != nil {
			return "", fmt.Errorf("lsp_query: %v", err)
		}
		filePath = resolved

		// Detect or use provided language.
		lang := agent.ArgString(args, "language")
		if lang == "" {
			lang = detectLSPLanguage(filePath)
		}
		if lang == "" {
			return "", fmt.Errorf("lsp_query: cannot detect language for %q; provide 'language' parameter", filePath)
		}

		// Find workspace root.
		rootDir := findLSPProjectRoot(filePath, lang)

		// Get or start the LSP server.
		server, err := reg.getOrStart(ctx, lang, rootDir)
		if err != nil {
			return "", fmt.Errorf("lsp_query: %v", err)
		}

		// Read file and ensure it's open in the LSP server.
		content, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("lsp_query: read file: %v", err)
		}
		uri := pathToFileURI(filePath)
		if err := server.ensureOpen(uri, lspLanguageID(lang), string(content)); err != nil {
			return "", fmt.Errorf("lsp_query: open file: %v", err)
		}

		switch action {
		case "definition":
			return lspDoPositionQuery(ctx, server, args, func(ctx context.Context, uri string, line, char int) (any, error) {
				locs, err := server.definition(ctx, uri, line, char)
				if err != nil {
					return nil, err
				}
				if len(locs) == 0 {
					return map[string]any{"definitions": []any{}, "message": "no definition found"}, nil
				}
				return map[string]any{"definitions": locs}, nil
			}, uri)

		case "references":
			return lspDoPositionQuery(ctx, server, args, func(ctx context.Context, uri string, line, char int) (any, error) {
				locs, err := server.references(ctx, uri, line, char)
				if err != nil {
					return nil, err
				}
				return map[string]any{"references": locs, "total": len(locs)}, nil
			}, uri)

		case "hover":
			return lspDoPositionQuery(ctx, server, args, func(ctx context.Context, uri string, line, char int) (any, error) {
				info, err := server.hover(ctx, uri, line, char)
				if err != nil {
					return nil, err
				}
				return map[string]any{"hover": info}, nil
			}, uri)

		case "diagnostics":
			// Allow a moment for the server to produce diagnostics after didOpen.
			time.Sleep(500 * time.Millisecond)
			diags := server.getDiagnostics(uri)
			var items []map[string]any
			for _, d := range diags {
				items = append(items, map[string]any{
					"line":     d.Range.Start.Line + 1,
					"col":      d.Range.Start.Character + 1,
					"end_line": d.Range.End.Line + 1,
					"end_col":  d.Range.End.Character + 1,
					"severity": lspDiagSeverityName(d.Severity),
					"message":  d.Message,
					"source":   d.Source,
				})
			}
			if items == nil {
				items = []map[string]any{}
			}
			raw, _ := json.Marshal(map[string]any{"diagnostics": items, "total": len(items)})
			return string(raw), nil

		case "symbols":
			syms, err := server.symbols(ctx, uri)
			if err != nil {
				return "", fmt.Errorf("lsp_query: %v", err)
			}
			if syms == nil {
				syms = []lspSymbolResult{}
			}
			raw, _ := json.Marshal(map[string]any{"symbols": syms, "total": len(syms)})
			return string(raw), nil

		default:
			return "", fmt.Errorf("lsp_query: unknown action %q; use definition, references, hover, diagnostics, or symbols", action)
		}
	}
}

// lspDoPositionQuery extracts line/character from args, converts to 0-based, and calls fn.
func lspDoPositionQuery(
	ctx context.Context,
	_ *lspServer,
	args map[string]any,
	fn func(ctx context.Context, uri string, line, char int) (any, error),
	uri string,
) (string, error) {
	line := agent.ArgInt(args, "line", 0)
	char := agent.ArgInt(args, "character", 0)
	if line < 1 || char < 1 {
		return "", fmt.Errorf("lsp_query: 'line' and 'character' are required (1-based)")
	}
	result, err := fn(ctx, uri, line-1, char-1)
	if err != nil {
		return "", fmt.Errorf("lsp_query: %v", err)
	}
	raw, _ := json.Marshal(result)
	return string(raw), nil
}

// ─── Tool definition ────────────────────────────────────────────────────────

var LSPQueryDef = agent.ToolDefinition{
	Name: "lsp_query",
	Description: `Query a Language Server Protocol (LSP) server for IDE-level code intelligence. Automatically detects the language and starts the appropriate LSP server (gopls, pyright, typescript-language-server, rust-analyzer, clangd). Servers are reused across calls.

Actions:
  definition  — Jump to where a symbol is defined
  references  — Find all usages of a symbol
  hover       — Get type info and documentation for a symbol
  diagnostics — Get errors, warnings, and hints for a file
  symbols     — List all symbols (functions, types, etc.) in a file

Line and character numbers are 1-based.`,
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"action": {
				Type:        "string",
				Description: "LSP action to perform.",
				Enum:        []string{"definition", "references", "hover", "diagnostics", "symbols"},
			},
			"file": {
				Type:        "string",
				Description: "Path to the source file to query.",
			},
			"line": {
				Type:        "integer",
				Description: "1-based line number (required for definition, references, hover).",
			},
			"character": {
				Type:        "integer",
				Description: "1-based column number (required for definition, references, hover).",
			},
			"language": {
				Type:        "string",
				Description: "Override auto-detection. One of: go, python, typescript, javascript, rust, c, cpp.",
				Enum:        []string{"go", "python", "typescript", "javascript", "rust", "c", "cpp"},
			},
		},
		Required: []string{"action", "file"},
	},
}
