package toolbuiltin

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ─── buildSandboxCmd ────────────────────────────────────────────────────────

func TestBuildSandboxCmd_Interpreted(t *testing.T) {
	lang := sandboxLangs["python"]
	cmd := buildSandboxCmd(lang, "")
	if cmd != "python3 script.py" {
		t.Errorf("cmd = %q, want %q", cmd, "python3 script.py")
	}
}

func TestBuildSandboxCmd_Compiled(t *testing.T) {
	lang := sandboxLangs["c"]
	cmd := buildSandboxCmd(lang, "")
	want := "gcc -o main main.c -lm 2>&1 && ./main"
	if cmd != want {
		t.Errorf("cmd = %q, want %q", cmd, want)
	}
}

func TestBuildSandboxCmd_WithArgs(t *testing.T) {
	lang := sandboxLangs["python"]
	cmd := buildSandboxCmd(lang, "--verbose input.txt")
	if !strings.HasSuffix(cmd, " --verbose input.txt") {
		t.Errorf("cmd = %q, missing args suffix", cmd)
	}
}

func TestBuildSandboxCmd_CompiledWithArgs(t *testing.T) {
	lang := sandboxLangs["rust"]
	cmd := buildSandboxCmd(lang, "-n 42")
	if !strings.Contains(cmd, "rustc main.rs") || !strings.HasSuffix(cmd, " -n 42") {
		t.Errorf("cmd = %q", cmd)
	}
}

// ─── filterNsjailLogs ───────────────────────────────────────────────────────

func TestFilterNsjailLogs(t *testing.T) {
	input := "[I] Mode: STANDALONE_ONCE\n[W] Warning message\nhello world\nfoo\n[E] Error line\n"
	got := filterNsjailLogs(input)
	if got != "hello world\nfoo" {
		t.Errorf("filtered = %q, want %q", got, "hello world\nfoo")
	}
}

func TestFilterNsjailLogs_AllNsjail(t *testing.T) {
	input := "[I] line1\n[W] line2\n[D] line3\n"
	got := filterNsjailLogs(input)
	if got != "" {
		t.Errorf("filtered = %q, want empty", got)
	}
}

func TestFilterNsjailLogs_NoneNsjail(t *testing.T) {
	input := "pure program output\nsecond line"
	got := filterNsjailLogs(input)
	if got != input {
		t.Errorf("filtered = %q, want %q", got, input)
	}
}

// ─── truncateSBOutput ───────────────────────────────────────────────────────

func TestTruncateSBOutput_Short(t *testing.T) {
	s := "hello"
	got := truncateSBOutput(s)
	if got != s {
		t.Errorf("truncated = %q", got)
	}
}

func TestTruncateSBOutput_Exact(t *testing.T) {
	s := strings.Repeat("a", maxSBOutput)
	got := truncateSBOutput(s)
	if got != s {
		t.Errorf("should not truncate at exact limit")
	}
}

func TestTruncateSBOutput_Long(t *testing.T) {
	s := strings.Repeat("a", maxSBOutput+100)
	got := truncateSBOutput(s)
	if !strings.HasSuffix(got, "... (output truncated at 64 KiB)") {
		t.Errorf("missing truncation suffix")
	}
	if len(got) != maxSBOutput+len("\n... (output truncated at 64 KiB)") {
		t.Errorf("truncated length = %d", len(got))
	}
}

// ─── detectSandboxBackend ───────────────────────────────────────────────────

func TestDetectSandboxBackend(t *testing.T) {
	backend := detectSandboxBackend()
	switch backend {
	case "nsjail", "docker", "limits":
		// valid
	default:
		t.Errorf("invalid backend: %q", backend)
	}
}

// ─── sandboxLangs completeness ──────────────────────────────────────────────

func TestSandboxLangsComplete(t *testing.T) {
	expected := []string{"go", "python", "javascript", "rust", "c", "cpp", "bash"}
	for _, lang := range expected {
		spec, ok := sandboxLangs[lang]
		if !ok {
			t.Errorf("missing language spec: %q", lang)
			continue
		}
		if spec.ext == "" || spec.file == "" || spec.image == "" || spec.run == "" {
			t.Errorf("language %q has empty fields: ext=%q file=%q image=%q run=%q",
				lang, spec.ext, spec.file, spec.image, spec.run)
		}
	}
}

// ─── sandboxEnv ─────────────────────────────────────────────────────────────

func TestSandboxEnv(t *testing.T) {
	env := sandboxEnv("/tmp/sandbox-123")

	// Check key vars are set.
	found := map[string]bool{"PATH": false, "HOME": false, "TMPDIR": false, "GOPATH": false}
	for _, e := range env {
		for key := range found {
			if strings.HasPrefix(e, key+"=") {
				found[key] = true
			}
		}
	}
	for key, ok := range found {
		if !ok {
			t.Errorf("missing env var %q", key)
		}
	}

	// HOME should be the sandbox dir.
	for _, e := range env {
		if strings.HasPrefix(e, "HOME=") {
			if e != "HOME=/tmp/sandbox-123" {
				t.Errorf("HOME = %q", e)
			}
		}
	}
}

// ─── Integration: runSandboxLimits ──────────────────────────────────────────

func TestRunSandboxLimits_Python(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "script.py"), []byte(`print("Hello, Sandbox!")`), 0644)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stdout, stderr, exitCode, err := runSandboxLimits(ctx, "python3 script.py", tmpDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if exitCode != 0 {
		t.Errorf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	if strings.TrimSpace(stdout) != "Hello, Sandbox!" {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestRunSandboxLimits_Stdin(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "script.py"), []byte(`import sys; print(sys.stdin.read().upper())`), 0644)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stdout, _, exitCode, err := runSandboxLimits(ctx, "python3 script.py", tmpDir, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if exitCode != 0 {
		t.Errorf("exit code = %d", exitCode)
	}
	if strings.TrimSpace(stdout) != "HELLO" {
		t.Errorf("stdout = %q, want HELLO", stdout)
	}
}

func TestRunSandboxLimits_ExitCode(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "script.sh"), []byte("exit 42"), 0644)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, exitCode, err := runSandboxLimits(ctx, "bash script.sh", tmpDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if exitCode != 42 {
		t.Errorf("exit code = %d, want 42", exitCode)
	}
}

func TestRunSandboxLimits_Timeout(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "script.sh"), []byte("sleep 60"), 0644)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	start := time.Now()
	_, _, _, _ = runSandboxLimits(ctx, "bash script.sh", tmpDir, "")
	elapsed := time.Since(start)

	// Should have been killed within ~2 seconds.
	if elapsed > 5*time.Second {
		t.Errorf("took %v, timeout did not work", elapsed)
	}
}

func TestRunSandboxLimits_Stderr(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	tmpDir := t.TempDir()
	code := `import sys; print("out"); print("err", file=sys.stderr)`
	os.WriteFile(filepath.Join(tmpDir, "script.py"), []byte(code), 0644)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stdout, stderr, exitCode, err := runSandboxLimits(ctx, "python3 script.py", tmpDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if exitCode != 0 {
		t.Errorf("exit code = %d", exitCode)
	}
	if strings.TrimSpace(stdout) != "out" {
		t.Errorf("stdout = %q", stdout)
	}
	if strings.TrimSpace(stderr) != "err" {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRunSandboxLimits_CompileC(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}

	tmpDir := t.TempDir()
	code := "#include <stdio.h>\nint main() { printf(\"Hello from C\\n\"); return 0; }\n"
	os.WriteFile(filepath.Join(tmpDir, "main.c"), []byte(code), 0644)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := buildSandboxCmd(sandboxLangs["c"], "")
	stdout, stderr, exitCode, err := runSandboxLimits(ctx, cmd, tmpDir, "")
	if err != nil {
		t.Fatalf("err=%v stderr=%q", err, stderr)
	}
	if exitCode != 0 {
		t.Errorf("exit code = %d, stderr = %q", exitCode, stderr)
	}
	if strings.TrimSpace(stdout) != "Hello from C" {
		t.Errorf("stdout = %q", stdout)
	}
}

// ─── Full tool integration ──────────────────────────────────────────────────

func TestSandboxExecTool_MissingCode(t *testing.T) {
	tool := SandboxExecTool()
	_, err := tool(context.Background(), map[string]any{
		"language": "python",
	})
	if err == nil || !strings.Contains(err.Error(), "'code' is required") {
		t.Errorf("expected code required error, got %v", err)
	}
}

func TestSandboxExecTool_MissingLanguage(t *testing.T) {
	tool := SandboxExecTool()
	_, err := tool(context.Background(), map[string]any{
		"code": "print(1)",
	})
	if err == nil || !strings.Contains(err.Error(), "'language' is required") {
		t.Errorf("expected language required error, got %v", err)
	}
}

func TestSandboxExecTool_UnsupportedLanguage(t *testing.T) {
	tool := SandboxExecTool()
	_, err := tool(context.Background(), map[string]any{
		"code":     "print(1)",
		"language": "haskell",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported language") {
		t.Errorf("expected unsupported language error, got %v", err)
	}
}

func TestSandboxExecTool_PythonHelloWorld(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	tool := SandboxExecTool()
	out, err := tool(context.Background(), map[string]any{
		"code":     `print("Hello from sandbox!")`,
		"language": "python",
		"sandbox":  "limits", // force limits backend for testing
		"timeout":  10,
	})
	if err != nil {
		t.Fatal(err)
	}

	var result sandboxResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, stderr = %q", result.ExitCode, result.Stderr)
	}
	if strings.TrimSpace(result.Stdout) != "Hello from sandbox!" {
		t.Errorf("stdout = %q", result.Stdout)
	}
	if result.Language != "python" {
		t.Errorf("language = %q", result.Language)
	}
	if result.Sandbox != "limits" {
		t.Errorf("sandbox = %q", result.Sandbox)
	}
	if result.DurationMS <= 0 {
		t.Errorf("duration_ms = %d", result.DurationMS)
	}
}

func TestSandboxExecTool_BashWithStdin(t *testing.T) {
	tool := SandboxExecTool()
	out, err := tool(context.Background(), map[string]any{
		"code":     `read name; echo "Hello, $name!"`,
		"language": "bash",
		"stdin":    "World",
		"sandbox":  "limits",
		"timeout":  5,
	})
	if err != nil {
		t.Fatal(err)
	}

	var result sandboxResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.Stdout) != "Hello, World!" {
		t.Errorf("stdout = %q", result.Stdout)
	}
}
