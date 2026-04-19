// Package toolbuiltin/system_test_runner provides a structured test runner tool.
//   - test_run → auto-detects project type and runs tests with structured output
//
// Supported frameworks:
//   - Go:     go test -json
//   - Python: pytest --tb=short -q (parsed)
//   - Node:   jest --json / vitest run --reporter=json
//   - Rust:   cargo test (parsed)
package toolbuiltin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"metiq/internal/agent"
)

// ─── test_run (polyglot) ──────────────────────────────────────────────────────

type testRunResult struct {
	Framework  string           `json:"framework"`
	Passed     int              `json:"passed"`
	Failed     int              `json:"failed"`
	Skipped    int              `json:"skipped"`
	DurationMs int64            `json:"duration_ms"`
	Failures   []testFailure    `json:"failures,omitempty"`
	Packages   []testPkgResult  `json:"packages,omitempty"`
}

type testFailure struct {
	Package string `json:"package"`
	Test    string `json:"test"`
	Output  string `json:"output"`
}

type testPkgResult struct {
	Package    string  `json:"package"`
	Passed     int     `json:"passed"`
	Failed     int     `json:"failed"`
	Skipped    int     `json:"skipped"`
	ElapsedSec float64 `json:"elapsed_sec,omitempty"`
	Status     string  `json:"status"` // pass, fail, skip
}

func TestRunTool(ctx context.Context, args map[string]any) (string, error) {
	dir := agent.ArgString(args, "directory")
	framework := agent.ArgString(args, "framework")
	runFilter := agent.ArgString(args, "run")

	timeout := 120 * time.Second
	if t := agent.ArgInt(args, "timeout_seconds", 0); t > 0 && t <= 600 {
		timeout = time.Duration(t) * time.Second
	}

	// Auto-detect framework if not specified.
	if framework == "" {
		framework = detectTestFramework(dir)
	}
	if framework == "" {
		return "", fmt.Errorf("test_run: could not detect test framework in directory (no go.mod, package.json, Cargo.toml, pyproject.toml, setup.py, or requirements.txt found). Set 'framework' explicitly.")
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch framework {
	case "go":
		return runGoTests(execCtx, dir, args, runFilter)
	case "pytest":
		return runPytestTests(execCtx, dir, runFilter)
	case "jest":
		return runJestTests(execCtx, dir, runFilter)
	case "vitest":
		return runVitestTests(execCtx, dir, runFilter)
	case "cargo":
		return runCargoTests(execCtx, dir, runFilter)
	default:
		return "", fmt.Errorf("test_run: unsupported framework %q (supported: go, pytest, jest, vitest, cargo)", framework)
	}
}

// ─── framework detection ──────────────────────────────────────────────────────

func detectTestFramework(dir string) string {
	if dir == "" {
		dir = "."
	}
	// Check for marker files in priority order.
	checks := []struct {
		file      string
		framework string
	}{
		{"go.mod", "go"},
		{"Cargo.toml", "cargo"},
		{"vitest.config.ts", "vitest"},
		{"vitest.config.js", "vitest"},
		{"vitest.config.mts", "vitest"},
		{"jest.config.ts", "jest"},
		{"jest.config.js", "jest"},
		{"jest.config.mjs", "jest"},
		{"jest.config.cjs", "jest"},
		{"pyproject.toml", "pytest"},
		{"setup.py", "pytest"},
		{"setup.cfg", "pytest"},
		{"requirements.txt", "pytest"},
		{"Pipfile", "pytest"},
	}
	for _, c := range checks {
		if _, err := os.Stat(filepath.Join(dir, c.file)); err == nil {
			return c.framework
		}
	}
	// Check package.json for test script to differentiate jest/vitest/other.
	pkgJSON := filepath.Join(dir, "package.json")
	if raw, err := os.ReadFile(pkgJSON); err == nil {
		content := string(raw)
		if strings.Contains(content, "vitest") {
			return "vitest"
		}
		if strings.Contains(content, "jest") {
			return "jest"
		}
		// Has package.json but no clear framework — default to jest.
		return "jest"
	}
	return ""
}

// ─── Go test runner ───────────────────────────────────────────────────────────

// goTestEvent represents a single line of `go test -json` output.
type goTestEvent struct {
	Time    string  `json:"Time"`
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

func runGoTests(ctx context.Context, dir string, args map[string]any, runFilter string) (string, error) {
	pkg := agent.ArgString(args, "package")
	if pkg == "" {
		pkg = "./..."
	}

	cmdArgs := []string{"test", "-json", "-count=1"}
	if runFilter != "" {
		cmdArgs = append(cmdArgs, "-run", runFilter)
	}
	cmdArgs = append(cmdArgs, pkg)

	cmd := exec.CommandContext(ctx, "go", cmdArgs...)
	if dir != "" {
		cmd.Dir = dir
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	if runErr != nil && stdoutBuf.Len() == 0 {
		stderr := strings.TrimSpace(stderrBuf.String())
		if stderr != "" {
			return "", fmt.Errorf("test_run[go]: %s", stderr)
		}
		return "", fmt.Errorf("test_run[go]: %v", runErr)
	}

	result := testRunResult{Framework: "go", DurationMs: elapsed.Milliseconds()}
	pkgMap := make(map[string]*testPkgResult)
	testOutputs := make(map[string][]string)

	scanner := bufio.NewScanner(&stdoutBuf)
	for scanner.Scan() {
		var ev goTestEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}

		if ev.Package != "" {
			if _, ok := pkgMap[ev.Package]; !ok {
				pkgMap[ev.Package] = &testPkgResult{Package: ev.Package, Status: "pass"}
			}
		}

		key := ev.Package + "/" + ev.Test
		pkgKey := ev.Package + "/"

		switch ev.Action {
		case "output":
			if ev.Test != "" {
				testOutputs[key] = append(testOutputs[key], ev.Output)
			} else {
				testOutputs[pkgKey] = append(testOutputs[pkgKey], ev.Output)
			}
		case "pass":
			if ev.Test != "" {
				result.Passed++
				if p, ok := pkgMap[ev.Package]; ok {
					p.Passed++
				}
			} else if ev.Package != "" {
				if p, ok := pkgMap[ev.Package]; ok {
					p.ElapsedSec = ev.Elapsed
					p.Status = "pass"
				}
			}
		case "fail":
			if ev.Test != "" {
				result.Failed++
				if p, ok := pkgMap[ev.Package]; ok {
					p.Failed++
				}
				output := strings.Join(testOutputs[key], "")
				if len(output) > 2000 {
					output = output[:2000] + "\n... (truncated)"
				}
				result.Failures = append(result.Failures, testFailure{
					Package: ev.Package,
					Test:    ev.Test,
					Output:  strings.TrimRight(output, "\n"),
				})
			} else if ev.Package != "" {
				if p, ok := pkgMap[ev.Package]; ok {
					p.ElapsedSec = ev.Elapsed
					p.Status = "fail"
				}
				pkgOutput := strings.Join(testOutputs[pkgKey], "")
				if pkgOutput != "" {
					if len(pkgOutput) > 2000 {
						pkgOutput = pkgOutput[:2000] + "\n... (truncated)"
					}
					result.Failed++
					result.Failures = append(result.Failures, testFailure{
						Package: ev.Package,
						Test:    "(package)",
						Output:  strings.TrimRight(pkgOutput, "\n"),
					})
				}
			}
		case "skip":
			if ev.Test != "" {
				result.Skipped++
				if p, ok := pkgMap[ev.Package]; ok {
					p.Skipped++
				}
			} else if ev.Package != "" {
				if p, ok := pkgMap[ev.Package]; ok {
					p.ElapsedSec = ev.Elapsed
					p.Status = "skip"
				}
			}
		}
	}

	seen := make(map[string]bool)
	for _, p := range pkgMap {
		if !seen[p.Package] {
			seen[p.Package] = true
			result.Packages = append(result.Packages, *p)
		}
	}

	raw, _ := json.Marshal(result)
	return string(raw), nil
}

// ─── pytest runner ────────────────────────────────────────────────────────────

func runPytestTests(ctx context.Context, dir, runFilter string) (string, error) {
	// Determine pytest binary: prefer "pytest", fall back to "python -m pytest".
	pytestBin, err := exec.LookPath("pytest")
	usePythonM := err != nil

	args := []string{"--tb=short", "-q", "--no-header"}
	if runFilter != "" {
		args = append(args, "-k", runFilter)
	}

	var cmd *exec.Cmd
	if usePythonM {
		pythonBin := findPython()
		allArgs := append([]string{"-m", "pytest"}, args...)
		cmd = exec.CommandContext(ctx, pythonBin, allArgs...)
	} else {
		cmd = exec.CommandContext(ctx, pytestBin, args...)
	}
	if dir != "" {
		cmd.Dir = dir
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	cmd.Run() // pytest exits non-zero on failures; we parse output regardless
	elapsed := time.Since(start)

	output := stdoutBuf.String()
	if output == "" {
		output = stderrBuf.String()
	}

	return parsePytestOutput(output, elapsed)
}

func findPython() string {
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return "python3"
}

// parsePytestOutput parses pytest -q --tb=short output into structured results.
var pytestSummaryRe = regexp.MustCompile(`(\d+) passed`)
var pytestFailedRe = regexp.MustCompile(`(\d+) failed`)
var pytestSkippedRe = regexp.MustCompile(`(\d+) skipped`)
var pytestErrorRe = regexp.MustCompile(`(\d+) error`)
var pytestFailureHeaderRe = regexp.MustCompile(`^FAILED (.+?)(?:::(.+?))?(?:\s*-\s*(.+))?$`)

func parsePytestOutput(output string, elapsed time.Duration) (string, error) {
	result := testRunResult{
		Framework:  "pytest",
		DurationMs: elapsed.Milliseconds(),
	}

	lines := strings.Split(output, "\n")

	// Parse failure blocks.
	var currentFailure *testFailure
	var failureOutput []string
	inFailure := false

	for _, line := range lines {
		// Detect FAILED lines.
		if m := pytestFailureHeaderRe.FindStringSubmatch(line); m != nil {
			// Save previous failure.
			if currentFailure != nil {
				currentFailure.Output = strings.TrimRight(strings.Join(failureOutput, "\n"), "\n")
				if len(currentFailure.Output) > 2000 {
					currentFailure.Output = currentFailure.Output[:2000] + "\n... (truncated)"
				}
				result.Failures = append(result.Failures, *currentFailure)
			}
			currentFailure = &testFailure{
				Package: m[1],
				Test:    m[2],
			}
			failureOutput = nil
			inFailure = true
			continue
		}

		// Detect summary line (e.g. "3 passed, 1 failed in 0.05s").
		if strings.Contains(line, " passed") || strings.Contains(line, " failed") || strings.Contains(line, " error") {
			if m := pytestSummaryRe.FindStringSubmatch(line); m != nil {
				n, _ := strconv.Atoi(m[1])
				result.Passed = n
			}
			if m := pytestFailedRe.FindStringSubmatch(line); m != nil {
				n, _ := strconv.Atoi(m[1])
				result.Failed = n
			}
			if m := pytestSkippedRe.FindStringSubmatch(line); m != nil {
				n, _ := strconv.Atoi(m[1])
				result.Skipped = n
			}
			if m := pytestErrorRe.FindStringSubmatch(line); m != nil {
				n, _ := strconv.Atoi(m[1])
				result.Failed += n
			}
			inFailure = false
		}

		if inFailure {
			failureOutput = append(failureOutput, line)
		}
	}

	// Save last failure.
	if currentFailure != nil {
		currentFailure.Output = strings.TrimRight(strings.Join(failureOutput, "\n"), "\n")
		if len(currentFailure.Output) > 2000 {
			currentFailure.Output = currentFailure.Output[:2000] + "\n... (truncated)"
		}
		result.Failures = append(result.Failures, *currentFailure)
	}

	// If we didn't parse summary counts but have output, try to count from dots/F/s.
	if result.Passed == 0 && result.Failed == 0 && result.Skipped == 0 && len(lines) > 0 {
		for _, line := range lines {
			for _, c := range line {
				switch c {
				case '.':
					result.Passed++
				case 'F', 'E':
					result.Failed++
				case 's':
					result.Skipped++
				}
			}
			// Stop at first non-progress line.
			if len(line) > 0 && !strings.ContainsAny(line, ".FEsxX") {
				break
			}
		}
	}

	raw, _ := json.Marshal(result)
	return string(raw), nil
}

// ─── Jest runner ──────────────────────────────────────────────────────────────

func runJestTests(ctx context.Context, dir, runFilter string) (string, error) {
	// Try npx jest --json.
	args := []string{"jest", "--json", "--no-coverage"}
	if runFilter != "" {
		args = append(args, "-t", runFilter)
	}

	cmd := exec.CommandContext(ctx, "npx", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "CI=true") // prevents interactive mode

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	cmd.Run() // jest exits non-zero on failures
	elapsed := time.Since(start)

	return parseJestJSON(stdoutBuf.String(), stderrBuf.String(), elapsed)
}

// jestResult is the root JSON structure from jest --json.
type jestResult struct {
	Success    bool             `json:"success"`
	NumPassed  int              `json:"numPassedTests"`
	NumFailed  int              `json:"numFailedTests"`
	NumPending int              `json:"numPendingTests"`
	TestResults []jestSuiteResult `json:"testResults"`
}

type jestSuiteResult struct {
	Name        string            `json:"name"`
	Status      string            `json:"status"` // passed, failed
	AssertionResults []jestTestResult `json:"assertionResults"`
	// Jest also uses "testResults" in some versions.
	TestResults []jestTestResult `json:"testResults,omitempty"`
}

type jestTestResult struct {
	FullName string   `json:"fullName"`
	Title    string   `json:"title"`
	Status   string   `json:"status"` // passed, failed, pending
	FailureMessages []string `json:"failureMessages"`
}

func parseJestJSON(stdout, stderr string, elapsed time.Duration) (string, error) {
	result := testRunResult{
		Framework:  "jest",
		DurationMs: elapsed.Milliseconds(),
	}

	// Jest may output non-JSON before the JSON blob. Find the JSON start.
	jsonStart := strings.Index(stdout, "{")
	if jsonStart < 0 {
		// No JSON output — fall back to stderr parsing.
		result.Failed = 1
		output := stdout + "\n" + stderr
		if len(output) > 2000 {
			output = output[:2000] + "\n... (truncated)"
		}
		result.Failures = append(result.Failures, testFailure{
			Package: "(jest)",
			Test:    "(output)",
			Output:  strings.TrimSpace(output),
		})
		raw, _ := json.Marshal(result)
		return string(raw), nil
	}

	var jest jestResult
	if err := json.Unmarshal([]byte(stdout[jsonStart:]), &jest); err != nil {
		// Failed to parse — return raw output.
		result.Failed = 1
		result.Failures = append(result.Failures, testFailure{
			Package: "(jest)",
			Test:    "(parse error)",
			Output:  truncateStr(stdout, 2000),
		})
		raw, _ := json.Marshal(result)
		return string(raw), nil
	}

	result.Passed = jest.NumPassed
	result.Failed = jest.NumFailed
	result.Skipped = jest.NumPending

	for _, suite := range jest.TestResults {
		tests := suite.AssertionResults
		if len(tests) == 0 {
			tests = suite.TestResults
		}
		for _, t := range tests {
			if t.Status == "failed" && len(t.FailureMessages) > 0 {
				output := strings.Join(t.FailureMessages, "\n")
				if len(output) > 2000 {
					output = output[:2000] + "\n... (truncated)"
				}
				result.Failures = append(result.Failures, testFailure{
					Package: filepath.Base(suite.Name),
					Test:    t.FullName,
					Output:  output,
				})
			}
		}
	}

	raw, _ := json.Marshal(result)
	return string(raw), nil
}

// ─── Vitest runner ────────────────────────────────────────────────────────────

func runVitestTests(ctx context.Context, dir, runFilter string) (string, error) {
	args := []string{"vitest", "run", "--reporter=json"}
	if runFilter != "" {
		args = append(args, "-t", runFilter)
	}

	cmd := exec.CommandContext(ctx, "npx", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "CI=true")

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	cmd.Run()
	elapsed := time.Since(start)

	// Vitest JSON output is similar to Jest format.
	return parseVitestJSON(stdoutBuf.String(), stderrBuf.String(), elapsed)
}

// vitestResult matches vitest --reporter=json output.
type vitestResult struct {
	NumPassedTests  int                `json:"numPassedTests"`
	NumFailedTests  int                `json:"numFailedTests"`
	NumPendingTests int                `json:"numPendingTests"`
	TestResults     []vitestSuiteResult `json:"testResults"`
}

type vitestSuiteResult struct {
	Name           string              `json:"name"`
	AssertionResults []vitestTestResult `json:"assertionResults"`
}

type vitestTestResult struct {
	FullName        string   `json:"fullName"`
	Title           string   `json:"title"`
	Status          string   `json:"status"`
	FailureMessages []string `json:"failureMessages"`
}

func parseVitestJSON(stdout, stderr string, elapsed time.Duration) (string, error) {
	result := testRunResult{
		Framework:  "vitest",
		DurationMs: elapsed.Milliseconds(),
	}

	jsonStart := strings.Index(stdout, "{")
	if jsonStart < 0 {
		result.Failed = 1
		output := stdout + "\n" + stderr
		result.Failures = append(result.Failures, testFailure{
			Package: "(vitest)",
			Test:    "(output)",
			Output:  truncateStr(strings.TrimSpace(output), 2000),
		})
		raw, _ := json.Marshal(result)
		return string(raw), nil
	}

	var vt vitestResult
	if err := json.Unmarshal([]byte(stdout[jsonStart:]), &vt); err != nil {
		result.Failed = 1
		result.Failures = append(result.Failures, testFailure{
			Package: "(vitest)",
			Test:    "(parse error)",
			Output:  truncateStr(stdout, 2000),
		})
		raw, _ := json.Marshal(result)
		return string(raw), nil
	}

	result.Passed = vt.NumPassedTests
	result.Failed = vt.NumFailedTests
	result.Skipped = vt.NumPendingTests

	for _, suite := range vt.TestResults {
		for _, t := range suite.AssertionResults {
			if t.Status == "failed" && len(t.FailureMessages) > 0 {
				output := strings.Join(t.FailureMessages, "\n")
				result.Failures = append(result.Failures, testFailure{
					Package: filepath.Base(suite.Name),
					Test:    t.FullName,
					Output:  truncateStr(output, 2000),
				})
			}
		}
	}

	raw, _ := json.Marshal(result)
	return string(raw), nil
}

// ─── Cargo test runner ────────────────────────────────────────────────────────

// Cargo test output patterns:
//   test result: ok. 5 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out
//   test module::test_name ... ok
//   test module::test_name ... FAILED

var cargoTestLineRe = regexp.MustCompile(`^test (.+?) \.\.\. (ok|FAILED|ignored)$`)
var cargoSummaryRe = regexp.MustCompile(`test result: \w+\. (\d+) passed; (\d+) failed; (\d+) ignored`)
var cargoFailureStartRe = regexp.MustCompile(`^---- (.+?) stdout ----$`)

func runCargoTests(ctx context.Context, dir, runFilter string) (string, error) {
	args := []string{"test", "--", "--color=never"}
	if runFilter != "" {
		args = append(args, runFilter)
	}

	cmd := exec.CommandContext(ctx, "cargo", args...)
	if dir != "" {
		cmd.Dir = dir
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	cmd.Run()
	elapsed := time.Since(start)

	// Cargo outputs to both stdout and stderr.
	output := stdoutBuf.String() + "\n" + stderrBuf.String()
	return parseCargoOutput(output, elapsed)
}

func parseCargoOutput(output string, elapsed time.Duration) (string, error) {
	result := testRunResult{
		Framework:  "cargo",
		DurationMs: elapsed.Milliseconds(),
	}

	lines := strings.Split(output, "\n")

	// Collect failure output blocks.
	failureOutputs := make(map[string][]string)
	var currentFailureName string
	inFailureBlock := false

	for _, line := range lines {
		// Check for individual test results.
		if m := cargoTestLineRe.FindStringSubmatch(line); m != nil {
			switch m[2] {
			case "ok":
				result.Passed++
			case "FAILED":
				result.Failed++
			case "ignored":
				result.Skipped++
			}
		}

		// Check for summary line.
		if m := cargoSummaryRe.FindStringSubmatch(line); m != nil {
			p, _ := strconv.Atoi(m[1])
			f, _ := strconv.Atoi(m[2])
			i, _ := strconv.Atoi(m[3])
			// Use summary counts as authoritative.
			result.Passed = p
			result.Failed = f
			result.Skipped = i
		}

		// Detect failure output blocks.
		if m := cargoFailureStartRe.FindStringSubmatch(line); m != nil {
			currentFailureName = m[1]
			inFailureBlock = true
			continue
		}
		if inFailureBlock {
			if strings.HasPrefix(line, "---- ") || strings.HasPrefix(line, "failures:") {
				inFailureBlock = false
				currentFailureName = ""
				continue
			}
			failureOutputs[currentFailureName] = append(failureOutputs[currentFailureName], line)
		}
	}

	// Build failure entries.
	for name, outputLines := range failureOutputs {
		output := strings.TrimRight(strings.Join(outputLines, "\n"), "\n")
		if len(output) > 2000 {
			output = output[:2000] + "\n... (truncated)"
		}
		parts := strings.SplitN(name, "::", 2)
		pkg := ""
		testName := name
		if len(parts) == 2 {
			pkg = parts[0]
			testName = parts[1]
		}
		result.Failures = append(result.Failures, testFailure{
			Package: pkg,
			Test:    testName,
			Output:  output,
		})
	}

	raw, _ := json.Marshal(result)
	return string(raw), nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n... (truncated)"
}

// ─── tool definition ─────────────────────────────────────────────────────────

var TestRunDef = agent.ToolDefinition{
	Name:        "test_run",
	Description: "Run tests and return structured results: passed/failed/skipped counts and detailed failure output. Auto-detects the project's test framework from marker files (go.mod → Go, package.json → Jest/Vitest, Cargo.toml → Rust, pyproject.toml → pytest). Override with the 'framework' parameter. Supports: go, pytest, jest, vitest, cargo.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"directory": {
				Type:        "string",
				Description: "Working directory for the test command. Should be the project root.",
			},
			"framework": {
				Type:        "string",
				Description: "Test framework to use. Auto-detected if omitted. Options: go, pytest, jest, vitest, cargo.",
				Enum:        []string{"go", "pytest", "jest", "vitest", "cargo"},
			},
			"package": {
				Type:        "string",
				Description: "Go-specific: package pattern to test, e.g. \"./...\" (default). Ignored for other frameworks.",
			},
			"run": {
				Type:        "string",
				Description: "Filter for test names: regex for Go/Rust, -k expression for pytest, -t pattern for Jest/Vitest.",
			},
			"timeout_seconds": {
				Type:        "integer",
				Description: "Maximum execution time in seconds (1–600). Defaults to 120.",
			},
		},
	},
}
