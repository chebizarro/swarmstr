package agent

import (
	"strings"
	"testing"
)

// ─── ChunkCode ──────────────────────────────────────────────────────────────

const goSample = `package main

import (
	"fmt"
	"os"
)

// Greeter greets people.
type Greeter struct {
	Name string
}

// Greet returns a greeting.
func (g *Greeter) Greet() string {
	return "Hello, " + g.Name
}

func main() {
	g := &Greeter{Name: "World"}
	fmt.Println(g.Greet())
	os.Exit(0)
}
`

func TestChunkCode_Go(t *testing.T) {
	chunks := ChunkCode(goSample)
	if len(chunks) < 3 {
		t.Fatalf("got %d chunks, want >= 3", len(chunks))
	}

	// First chunk should be header (package + imports).
	if chunks[0].Kind != "header" {
		t.Errorf("chunk[0].Kind = %q, want header", chunks[0].Kind)
	}
	if !strings.Contains(chunks[0].Content, "package main") {
		t.Error("header should contain package declaration")
	}
	if !strings.Contains(chunks[0].Content, "import") {
		t.Error("header should contain imports")
	}

	// Find the type chunk.
	var typeChunk *CodeChunk
	for i := range chunks {
		if chunks[i].Kind == "type" && chunks[i].Name == "Greeter" {
			typeChunk = &chunks[i]
			break
		}
	}
	if typeChunk == nil {
		t.Fatal("expected a type chunk for Greeter")
	}
	if !strings.Contains(typeChunk.Content, "struct") {
		t.Error("type chunk should contain struct keyword")
	}

	// Find function chunks.
	funcNames := map[string]bool{}
	for _, c := range chunks {
		if c.Kind == "function" {
			funcNames[c.Name] = true
		}
	}
	if !funcNames["Greet"] {
		t.Error("expected function chunk for Greet")
	}
	if !funcNames["main"] {
		t.Error("expected function chunk for main")
	}
}

const pythonSample = `import os
from pathlib import Path

# Configuration constants
MAX_RETRIES = 3

@dataclass
class Config:
    host: str
    port: int = 8080

    def url(self):
        return f"http://{self.host}:{self.port}"

def process(config: Config) -> bool:
    """Process the configuration."""
    for i in range(MAX_RETRIES):
        if try_connect(config):
            return True
    return False

class Worker:
    def __init__(self, cfg):
        self.cfg = cfg

    def run(self):
        process(self.cfg)
`

func TestChunkCode_Python(t *testing.T) {
	chunks := ChunkCode(pythonSample)
	if len(chunks) < 3 {
		t.Fatalf("got %d chunks, want >= 3", len(chunks))
	}

	// Header should contain imports.
	if chunks[0].Kind != "header" {
		t.Errorf("chunk[0].Kind = %q, want header", chunks[0].Kind)
	}

	// Should have Config class (with @dataclass decorator).
	var configChunk *CodeChunk
	for i := range chunks {
		if chunks[i].Name == "Config" {
			configChunk = &chunks[i]
			break
		}
	}
	if configChunk == nil {
		t.Fatal("expected chunk for Config")
	}
	if configChunk.Kind != "class" {
		t.Errorf("Config kind = %q, want class", configChunk.Kind)
	}
	// Decorator should be included.
	if !strings.Contains(configChunk.Content, "@dataclass") {
		t.Error("Config chunk should include @dataclass decorator")
	}

	// Should have process function.
	var processChunk *CodeChunk
	for i := range chunks {
		if chunks[i].Name == "process" {
			processChunk = &chunks[i]
			break
		}
	}
	if processChunk == nil {
		t.Fatal("expected chunk for process")
	}
	if processChunk.Kind != "function" {
		t.Errorf("process kind = %q, want function", processChunk.Kind)
	}
}

const rustSample = `use std::io;
use std::collections::HashMap;

/// A simple key-value store.
#[derive(Debug)]
pub struct Store {
    data: HashMap<String, String>,
}

impl Store {
    pub fn new() -> Self {
        Store { data: HashMap::new() }
    }

    pub fn get(&self, key: &str) -> Option<&String> {
        self.data.get(key)
    }
}

pub fn run() -> io::Result<()> {
    let store = Store::new();
    println!("{:?}", store);
    Ok(())
}
`

func TestChunkCode_Rust(t *testing.T) {
	chunks := ChunkCode(rustSample)
	if len(chunks) < 3 {
		t.Fatalf("got %d chunks, want >= 3", len(chunks))
	}

	kinds := map[string][]string{}
	for _, c := range chunks {
		kinds[c.Kind] = append(kinds[c.Kind], c.Name)
	}

	if _, ok := kinds["type"]; !ok {
		t.Error("expected type chunk for Store")
	}
	if _, ok := kinds["impl"]; !ok {
		t.Error("expected impl chunk for Store")
	}
	if _, ok := kinds["function"]; !ok {
		t.Error("expected function chunk for run")
	}

	// Check that #[derive(Debug)] is included with the struct.
	for _, c := range chunks {
		if c.Name == "Store" && c.Kind == "type" {
			if !strings.Contains(c.Content, "#[derive(Debug)]") {
				t.Error("Store chunk should include #[derive(Debug)] attribute")
			}
		}
	}
}

const jsSample = `import React from 'react';
import { useState } from 'react';

export interface AppProps {
  title: string;
}

export class App extends React.Component<AppProps> {
  render() {
    return <div>{this.props.title}</div>;
  }
}

export function greet(name: string): string {
  return "Hello, " + name;
}

export default function Main() {
  const [count, setCount] = useState(0);
  return <App title="Hello" />;
}
`

func TestChunkCode_JavaScript(t *testing.T) {
	chunks := ChunkCode(jsSample)
	if len(chunks) < 3 {
		t.Fatalf("got %d chunks, want >= 3", len(chunks))
	}

	names := map[string]string{}
	for _, c := range chunks {
		if c.Name != "" {
			names[c.Name] = c.Kind
		}
	}

	if names["AppProps"] != "interface" {
		t.Errorf("AppProps kind = %q, want interface", names["AppProps"])
	}
	if names["App"] != "class" {
		t.Errorf("App kind = %q, want class", names["App"])
	}
	if names["greet"] != "function" {
		t.Errorf("greet kind = %q, want function", names["greet"])
	}
	if names["Main"] != "function" {
		t.Errorf("Main kind = %q, want function", names["Main"])
	}
}

func TestChunkCode_Empty(t *testing.T) {
	chunks := ChunkCode("")
	if chunks != nil {
		t.Errorf("expected nil, got %d chunks", len(chunks))
	}

	chunks = ChunkCode("   \n  \n  ")
	if chunks != nil {
		t.Errorf("expected nil for whitespace-only, got %d chunks", len(chunks))
	}
}

func TestChunkCode_NoDeclarations(t *testing.T) {
	content := "# Just a comment\nx = 1\ny = 2\nprint(x + y)\n"
	chunks := ChunkCode(content)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if chunks[0].Kind != "block" {
		t.Errorf("kind = %q, want block", chunks[0].Kind)
	}
}

func TestChunkCode_CommentsBeforeFunc(t *testing.T) {
	content := `package main

// Add adds two numbers.
// It returns their sum.
func Add(a, b int) int {
	return a + b
}

// Sub subtracts b from a.
func Sub(a, b int) int {
	return a - b
}
`
	chunks := ChunkCode(content)

	// Should have header + 2 functions.
	funcChunks := 0
	for _, c := range chunks {
		if c.Kind == "function" {
			funcChunks++
			// Comments should be included in the function chunk.
			if c.Name == "Add" && !strings.Contains(c.Content, "// Add adds two numbers.") {
				t.Error("Add chunk should include its doc comment")
			}
			if c.Name == "Sub" && !strings.Contains(c.Content, "// Sub subtracts") {
				t.Error("Sub chunk should include its doc comment")
			}
		}
	}
	if funcChunks != 2 {
		t.Errorf("got %d function chunks, want 2", funcChunks)
	}
}

// ─── matchCodeDecl ──────────────────────────────────────────────────────────

func TestMatchCodeDecl_Go(t *testing.T) {
	tests := []struct {
		line     string
		wantKind string
		wantName string
	}{
		{"func main() {", "function", "main"},
		{"func (r *Repo) Save() error {", "function", "Save"},
		{"type Config struct {", "type", "Config"},
		{"var ErrNotFound = errors.New(\"not found\")", "variable", "ErrNotFound"},
		{"const MaxRetries = 3", "constant", "MaxRetries"},
	}
	for _, tt := range tests {
		kind, name := matchCodeDecl(tt.line)
		if kind != tt.wantKind || name != tt.wantName {
			t.Errorf("matchCodeDecl(%q) = (%q, %q), want (%q, %q)",
				tt.line, kind, name, tt.wantKind, tt.wantName)
		}
	}
}

func TestMatchCodeDecl_Python(t *testing.T) {
	kind, name := matchCodeDecl("def process(data):")
	if kind != "function" || name != "process" {
		t.Errorf("got (%q, %q)", kind, name)
	}
	kind, name = matchCodeDecl("class MyClass(Base):")
	if kind != "class" || name != "MyClass" {
		t.Errorf("got (%q, %q)", kind, name)
	}
}

func TestMatchCodeDecl_Rust(t *testing.T) {
	tests := []struct {
		line     string
		wantKind string
		wantName string
	}{
		{"fn main() {", "function", "main"},
		{"pub fn new() -> Self {", "function", "new"},
		{"pub(crate) async fn process() {", "function", "process"},
		{"struct Config {", "type", "Config"},
		{"pub struct Config {", "type", "Config"},
		{"impl Config {", "impl", "Config"},
		{"pub enum State {", "type", "State"},
		{"trait Handler {", "type", "Handler"},
	}
	for _, tt := range tests {
		kind, name := matchCodeDecl(tt.line)
		if kind != tt.wantKind || name != tt.wantName {
			t.Errorf("matchCodeDecl(%q) = (%q, %q), want (%q, %q)",
				tt.line, kind, name, tt.wantKind, tt.wantName)
		}
	}
}

func TestMatchCodeDecl_JS(t *testing.T) {
	tests := []struct {
		line     string
		wantKind string
		wantName string
	}{
		{"function greet() {", "function", "greet"},
		{"async function fetchData() {", "function", "fetchData"},
		{"export function helper() {", "function", "helper"},
		{"export default function Main() {", "function", "Main"},
		{"class App {", "class", "App"},
		{"export class Widget {", "class", "Widget"},
		{"export interface Config {", "interface", "Config"},
		{"export type Result = string | number;", "type", "Result"},
		{"export enum Status {", "type", "Status"},
	}
	for _, tt := range tests {
		kind, name := matchCodeDecl(tt.line)
		if kind != tt.wantKind || name != tt.wantName {
			t.Errorf("matchCodeDecl(%q) = (%q, %q), want (%q, %q)",
				tt.line, kind, name, tt.wantKind, tt.wantName)
		}
	}
}

func TestMatchCodeDecl_IndentedNotMatched(t *testing.T) {
	// Indented lines should NOT match (not top-level).
	lines := []string{
		"    func inner() {",
		"\tdef helper():",
		"  class Nested:",
		"    pub fn method() {",
	}
	for _, line := range lines {
		kind, _ := matchCodeDecl(line)
		if kind != "" {
			t.Errorf("indented line %q should not match, got kind=%q", line, kind)
		}
	}
}

func TestMatchCodeDecl_CommentsNotMatched(t *testing.T) {
	lines := []string{
		"// func notReal() {",
		"/* type Fake struct */",
		"# def not_a_function():",
		"#!/usr/bin/env python",
	}
	for _, line := range lines {
		kind, _ := matchCodeDecl(line)
		if kind != "" {
			t.Errorf("comment line %q should not match, got kind=%q", line, kind)
		}
	}
}

// ─── isCodeAnnotation ───────────────────────────────────────────────────────

func TestIsCodeAnnotation(t *testing.T) {
	positive := []string{
		"// Go comment",
		"/// Rust doc comment",
		"/* Block comment */",
		"* Continuation",
		"*/",
		"# Python comment",
		"@decorator",
		"@dataclass",
		"#[derive(Debug)]",
		"#[cfg(test)]",
	}
	for _, line := range positive {
		if !isCodeAnnotation(line) {
			t.Errorf("isCodeAnnotation(%q) = false, want true", line)
		}
	}

	negative := []string{
		"",
		"func main() {",
		"x = 1",
		"#!/usr/bin/env python",
	}
	for _, line := range negative {
		if isCodeAnnotation(line) {
			t.Errorf("isCodeAnnotation(%q) = true, want false", line)
		}
	}
}

// ─── IsLikelyCode ───────────────────────────────────────────────────────────

func TestIsLikelyCode_SourceCode(t *testing.T) {
	if !IsLikelyCode(goSample) {
		t.Error("Go sample should be detected as code")
	}
	if !IsLikelyCode(pythonSample) {
		t.Error("Python sample should be detected as code")
	}
	if !IsLikelyCode(rustSample) {
		t.Error("Rust sample should be detected as code")
	}
	if !IsLikelyCode(jsSample) {
		t.Error("JS sample should be detected as code")
	}
}

func TestIsLikelyCode_NotCode(t *testing.T) {
	prose := "This is a regular text document.\nIt has multiple lines.\nBut no code constructs.\nJust plain English text.\n"
	if IsLikelyCode(prose) {
		t.Error("plain prose should NOT be detected as code")
	}

	jsonData := `{"key": "value", "count": 42, "items": [1, 2, 3]}`
	if IsLikelyCode(jsonData) {
		t.Error("JSON data should NOT be detected as code")
	}
}

// ─── TruncateCodeAware ─────────────────────────────────────────────────────

// largeSample is big enough (~900 chars) for truncation tests where the
// suffix and 200-char minimum budget must be satisfied.
const largeSample = `package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Config holds server configuration.
type Config struct {
	Host         string
	Port         int
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	MaxConns     int
}

// Server is an HTTP server with graceful shutdown.
type Server struct {
	cfg    Config
	mu     sync.Mutex
	conns  int
	router *http.ServeMux
}

// New creates a new Server with the given config.
func New(cfg Config) *Server {
	return &Server{
		cfg:    cfg,
		router: http.NewServeMux(),
	}
}

// Start begins listening and serving HTTP requests.
func (s *Server) Start(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  s.cfg.ReadTimeout,
		WriteTimeout: s.cfg.WriteTimeout,
	}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	return srv.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Println("shutting down")
	return nil
}
`

func TestTruncateCodeAware_FitsInBudget(t *testing.T) {
	result := TruncateCodeAware(largeSample, 100000, false)
	if result != largeSample {
		t.Error("should return content unchanged when it fits")
	}
}

func TestTruncateCodeAware_HeadOnly(t *testing.T) {
	chunks := ChunkCode(largeSample)
	if len(chunks) < 3 {
		t.Skip("not enough chunks for test")
	}
	// Half the content — forces truncation while leaving room for
	// suffix + at least the header chunk.
	budget := len(largeSample) / 2
	result := TruncateCodeAware(largeSample, budget, false)

	if result == "" {
		t.Fatalf("expected non-empty result (budget=%d, content=%d, chunks=%d)",
			budget, len(largeSample), len(chunks))
	}
	if !strings.Contains(result, chunks[0].Content) {
		t.Error("result should contain first chunk")
	}
	if !strings.Contains(result, "omitted") {
		t.Error("result should contain omission marker")
	}
	if !strings.Contains(result, "block boundaries") {
		t.Error("result should contain truncation notice")
	}
}

func TestTruncateCodeAware_HeadTail(t *testing.T) {
	chunks := ChunkCode(largeSample)
	if len(chunks) < 4 {
		t.Skip("not enough chunks for head+tail test")
	}
	// Budget for header + 1 block from head and 1 block from tail.
	budget := chunks[0].Size + chunks[1].Size + chunks[len(chunks)-1].Size + 300
	result := TruncateCodeAware(largeSample, budget, true)

	if result == "" {
		t.Fatal("expected non-empty result")
	}
	// Should have both head and tail content.
	if !strings.Contains(result, "package server") {
		t.Error("should contain header")
	}
	// Last chunk should be present.
	lastChunk := chunks[len(chunks)-1]
	if !strings.Contains(result, lastChunk.Name) {
		t.Errorf("should contain last chunk name %q", lastChunk.Name)
	}
}

func TestTruncateCodeAware_SingleBlock(t *testing.T) {
	content := "x = 1\ny = 2\nprint(x + y)\n"
	result := TruncateCodeAware(content, 10, false)
	if result != "" {
		t.Errorf("single block should return empty (fallthrough), got %q", result)
	}
}

func TestTruncateCodeAware_BudgetTooSmall(t *testing.T) {
	result := TruncateCodeAware(goSample, 50, false)
	if result != "" {
		t.Error("tiny budget should return empty (fallthrough)")
	}
}

// ─── ChunkSummary ───────────────────────────────────────────────────────────

func TestChunkSummary(t *testing.T) {
	chunks := ChunkCode(goSample)
	summary := ChunkSummary(chunks)

	if !strings.Contains(summary, "header") {
		t.Error("summary should mention header")
	}
	if !strings.Contains(summary, "function") {
		t.Error("summary should mention function")
	}
	if !strings.Contains(summary, "chars") {
		t.Error("summary should show char counts")
	}
}

func TestChunkSummary_Empty(t *testing.T) {
	if ChunkSummary(nil) != "(empty)" {
		t.Error("nil chunks should produce (empty)")
	}
}
