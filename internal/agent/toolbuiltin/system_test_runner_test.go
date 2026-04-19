package toolbuiltin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ─── framework detection ──────────────────────────────────────────────────────

func TestDetectTestFramework_Go(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\ngo 1.21\n"), 0644)
	if got := detectTestFramework(dir); got != "go" {
		t.Errorf("expected go, got %q", got)
	}
}

func TestDetectTestFramework_Cargo(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"test\"\n"), 0644)
	if got := detectTestFramework(dir); got != "cargo" {
		t.Errorf("expected cargo, got %q", got)
	}
}

func TestDetectTestFramework_Pytest(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname = \"test\"\n"), 0644)
	if got := detectTestFramework(dir); got != "pytest" {
		t.Errorf("expected pytest, got %q", got)
	}
}

func TestDetectTestFramework_Vitest(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "vitest.config.ts"), []byte("export default {}\n"), 0644)
	if got := detectTestFramework(dir); got != "vitest" {
		t.Errorf("expected vitest, got %q", got)
	}
}

func TestDetectTestFramework_Jest(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "jest.config.js"), []byte("module.exports = {}\n"), 0644)
	if got := detectTestFramework(dir); got != "jest" {
		t.Errorf("expected jest, got %q", got)
	}
}

func TestDetectTestFramework_PackageJSON_Jest(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"devDependencies":{"jest":"^29"}}`), 0644)
	if got := detectTestFramework(dir); got != "jest" {
		t.Errorf("expected jest, got %q", got)
	}
}

func TestDetectTestFramework_PackageJSON_Vitest(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"devDependencies":{"vitest":"^1"}}`), 0644)
	if got := detectTestFramework(dir); got != "vitest" {
		t.Errorf("expected vitest, got %q", got)
	}
}

func TestDetectTestFramework_GoTakesPriority(t *testing.T) {
	// If both go.mod and package.json exist, Go should win.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\ngo 1.21\n"), 0644)
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"devDependencies":{"jest":"^29"}}`), 0644)
	if got := detectTestFramework(dir); got != "go" {
		t.Errorf("expected go (priority), got %q", got)
	}
}

func TestDetectTestFramework_Unknown(t *testing.T) {
	dir := t.TempDir()
	if got := detectTestFramework(dir); got != "" {
		t.Errorf("expected empty for unknown project, got %q", got)
	}
}

// ─── pytest parser ────────────────────────────────────────────────────────────

func TestParsePytestOutput_AllPass(t *testing.T) {
	output := `...
3 passed in 0.05s
`
	raw, err := parsePytestOutput(output, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result testRunResult
	json.Unmarshal([]byte(raw), &result)

	if result.Framework != "pytest" {
		t.Errorf("expected framework=pytest, got %s", result.Framework)
	}
	if result.Passed != 3 {
		t.Errorf("expected 3 passed, got %d", result.Passed)
	}
	if result.Failed != 0 {
		t.Errorf("expected 0 failed, got %d", result.Failed)
	}
}

func TestParsePytestOutput_WithFailures(t *testing.T) {
	output := `FAILED test_math.py::test_add - assert 3 == 4
FAILED test_math.py::test_sub - assert 5 == 6
2 failed, 3 passed, 1 skipped in 0.12s
`
	raw, err := parsePytestOutput(output, 120*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result testRunResult
	json.Unmarshal([]byte(raw), &result)

	if result.Passed != 3 {
		t.Errorf("expected 3 passed, got %d", result.Passed)
	}
	if result.Failed != 2 {
		t.Errorf("expected 2 failed, got %d", result.Failed)
	}
	if result.Skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", result.Skipped)
	}
	if len(result.Failures) != 2 {
		t.Errorf("expected 2 failure entries, got %d", len(result.Failures))
	}
}

func TestParsePytestOutput_OnlyDots(t *testing.T) {
	// Some pytest versions only output progress dots.
	output := "....F.s.\n"
	raw, err := parsePytestOutput(output, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result testRunResult
	json.Unmarshal([]byte(raw), &result)

	if result.Passed != 6 {
		t.Errorf("expected 6 passed (dots), got %d", result.Passed)
	}
	if result.Failed != 1 {
		t.Errorf("expected 1 failed (F), got %d", result.Failed)
	}
	if result.Skipped != 1 {
		t.Errorf("expected 1 skipped (s), got %d", result.Skipped)
	}
}

// ─── Jest JSON parser ─────────────────────────────────────────────────────────

func TestParseJestJSON_Success(t *testing.T) {
	jestOut := `{"success":true,"numPassedTests":5,"numFailedTests":0,"numPendingTests":1,"testResults":[]}`
	raw, err := parseJestJSON(jestOut, "", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result testRunResult
	json.Unmarshal([]byte(raw), &result)

	if result.Framework != "jest" {
		t.Errorf("expected framework=jest, got %s", result.Framework)
	}
	if result.Passed != 5 {
		t.Errorf("expected 5 passed, got %d", result.Passed)
	}
	if result.Skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", result.Skipped)
	}
}

func TestParseJestJSON_WithFailures(t *testing.T) {
	jestOut := `{
		"success": false,
		"numPassedTests": 3,
		"numFailedTests": 1,
		"numPendingTests": 0,
		"testResults": [{
			"name": "/app/src/math.test.ts",
			"status": "failed",
			"assertionResults": [{
				"fullName": "math > should add numbers",
				"title": "should add numbers",
				"status": "failed",
				"failureMessages": ["Expected 4 but received 3"]
			}]
		}]
	}`
	raw, err := parseJestJSON(jestOut, "", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result testRunResult
	json.Unmarshal([]byte(raw), &result)

	if result.Failed != 1 {
		t.Errorf("expected 1 failed, got %d", result.Failed)
	}
	if len(result.Failures) != 1 {
		t.Fatalf("expected 1 failure entry, got %d", len(result.Failures))
	}
	if result.Failures[0].Test != "math > should add numbers" {
		t.Errorf("unexpected test name: %s", result.Failures[0].Test)
	}
}

func TestParseJestJSON_NoJSON(t *testing.T) {
	raw, err := parseJestJSON("Error: jest not found", "command not found", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result testRunResult
	json.Unmarshal([]byte(raw), &result)

	if result.Failed != 1 {
		t.Errorf("expected 1 failed for non-JSON output, got %d", result.Failed)
	}
}

// ─── Cargo test parser ────────────────────────────────────────────────────────

func TestParseCargoOutput_AllPass(t *testing.T) {
	output := `running 3 tests
test utils::test_parse ... ok
test utils::test_format ... ok
test main::test_run ... ok

test result: ok. 3 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.01s
`
	raw, err := parseCargoOutput(output, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result testRunResult
	json.Unmarshal([]byte(raw), &result)

	if result.Framework != "cargo" {
		t.Errorf("expected framework=cargo, got %s", result.Framework)
	}
	if result.Passed != 3 {
		t.Errorf("expected 3 passed, got %d", result.Passed)
	}
	if result.Failed != 0 {
		t.Errorf("expected 0 failed, got %d", result.Failed)
	}
}

func TestParseCargoOutput_WithFailures(t *testing.T) {
	output := `running 3 tests
test utils::test_parse ... ok
test utils::test_format ... FAILED
test main::test_run ... ok

failures:

---- utils::test_format stdout ----
thread 'utils::test_format' panicked at 'assertion failed: expected 42, got 0'

failures:
    utils::test_format

test result: FAILED. 2 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.02s
`
	raw, err := parseCargoOutput(output, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result testRunResult
	json.Unmarshal([]byte(raw), &result)

	if result.Passed != 2 {
		t.Errorf("expected 2 passed, got %d", result.Passed)
	}
	if result.Failed != 1 {
		t.Errorf("expected 1 failed, got %d", result.Failed)
	}
	if len(result.Failures) != 1 {
		t.Fatalf("expected 1 failure entry, got %d", len(result.Failures))
	}
	if result.Failures[0].Test != "test_format" {
		t.Errorf("unexpected test name: %s", result.Failures[0].Test)
	}
	if result.Failures[0].Package != "utils" {
		t.Errorf("unexpected package: %s", result.Failures[0].Package)
	}
}

func TestParseCargoOutput_Ignored(t *testing.T) {
	output := `running 2 tests
test test_a ... ok
test test_b ... ignored

test result: ok. 1 passed; 0 failed; 1 ignored; 0 measured; 0 filtered out
`
	raw, err := parseCargoOutput(output, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result testRunResult
	json.Unmarshal([]byte(raw), &result)

	if result.Passed != 1 {
		t.Errorf("expected 1 passed, got %d", result.Passed)
	}
	if result.Skipped != 1 {
		t.Errorf("expected 1 skipped (ignored), got %d", result.Skipped)
	}
}

// ─── Vitest JSON parser ──────────────────────────────────────────────────────

func TestParseVitestJSON_Success(t *testing.T) {
	vitestOut := `{"numPassedTests":4,"numFailedTests":0,"numPendingTests":0,"testResults":[]}`
	raw, err := parseVitestJSON(vitestOut, "", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result testRunResult
	json.Unmarshal([]byte(raw), &result)

	if result.Framework != "vitest" {
		t.Errorf("expected framework=vitest, got %s", result.Framework)
	}
	if result.Passed != 4 {
		t.Errorf("expected 4 passed, got %d", result.Passed)
	}
}

// ─── truncateStr ──────────────────────────────────────────────────────────────

func TestTruncateStr(t *testing.T) {
	if got := truncateStr("short", 100); got != "short" {
		t.Errorf("should not truncate: %q", got)
	}
	long := "a very long string that exceeds the limit"
	got := truncateStr(long, 10)
	if len(got) > 40 { // 10 + suffix
		t.Errorf("should be truncated: %q", got)
	}
}
