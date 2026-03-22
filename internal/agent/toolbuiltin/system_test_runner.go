// Package toolbuiltin/system_test_runner provides a structured test runner tool.
//   - test_run → runs `go test -json` and returns parsed results
package toolbuiltin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"metiq/internal/agent"
)

// ─── test_run ─────────────────────────────────────────────────────────────────

type testRunResult struct {
	Passed     int              `json:"passed"`
	Failed     int              `json:"failed"`
	Skipped    int              `json:"skipped"`
	DurationMs int64            `json:"duration_ms"`
	Failures   []testFailure    `json:"failures,omitempty"`
	Packages   []testPkgResult  `json:"packages"`
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

// goTestEvent represents a single line of `go test -json` output.
type goTestEvent struct {
	Time    string  `json:"Time"`
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

func TestRunTool(ctx context.Context, args map[string]any) (string, error) {
	dir := agent.ArgString(args, "directory")
	pkg := agent.ArgString(args, "package")
	runFilter := agent.ArgString(args, "run")

	if pkg == "" {
		pkg = "./..."
	}

	timeout := 120 * time.Second
	if t := agent.ArgInt(args, "timeout_seconds", 0); t > 0 && t <= 600 {
		timeout = time.Duration(t) * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmdArgs := []string{"test", "-json", "-count=1"}
	if runFilter != "" {
		cmdArgs = append(cmdArgs, "-run", runFilter)
	}
	cmdArgs = append(cmdArgs, pkg)

	cmd := exec.CommandContext(execCtx, "go", cmdArgs...)
	if dir != "" {
		cmd.Dir = dir
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	// If we couldn't even start the process or it's a non-test error,
	// check if there's any JSON output. If not, return the raw error.
	if runErr != nil && stdoutBuf.Len() == 0 {
		stderr := strings.TrimSpace(stderrBuf.String())
		if stderr != "" {
			return "", fmt.Errorf("test_run: %s", stderr)
		}
		return "", fmt.Errorf("test_run: %v", runErr)
	}

	// Parse the JSON event stream.
	result := testRunResult{DurationMs: elapsed.Milliseconds()}
	pkgMap := make(map[string]*testPkgResult)
	// testOutputs collects output lines per "package/test" key for failure reports.
	testOutputs := make(map[string][]string)

	scanner := bufio.NewScanner(&stdoutBuf)
	for scanner.Scan() {
		var ev goTestEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue // skip non-JSON lines (e.g. build output)
		}

		// Ensure package entry exists.
		if ev.Package != "" {
			if _, ok := pkgMap[ev.Package]; !ok {
				pkgMap[ev.Package] = &testPkgResult{Package: ev.Package, Status: "pass"}
			}
		}

		key := ev.Package + "/" + ev.Test

		// Package-scoped output key (test=="").
		pkgKey := ev.Package + "/"

		switch ev.Action {
		case "output":
			if ev.Test != "" {
				testOutputs[key] = append(testOutputs[key], ev.Output)
			} else {
				// Package-level output (build errors, setup failures).
				testOutputs[pkgKey] = append(testOutputs[pkgKey], ev.Output)
			}

		case "pass":
			if ev.Test != "" {
				result.Passed++
				if pkg, ok := pkgMap[ev.Package]; ok {
					pkg.Passed++
				}
			} else if ev.Package != "" {
				// Package-level pass.
				if pkg, ok := pkgMap[ev.Package]; ok {
					pkg.ElapsedSec = ev.Elapsed
					pkg.Status = "pass"
				}
			}

		case "fail":
			if ev.Test != "" {
				result.Failed++
				if pkg, ok := pkgMap[ev.Package]; ok {
					pkg.Failed++
				}
				// Collect failure output.
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
				// Package-level failure (build error, setup failure, etc.).
				if pkg, ok := pkgMap[ev.Package]; ok {
					pkg.ElapsedSec = ev.Elapsed
					pkg.Status = "fail"
				}
				// If no individual test failures recorded, emit a package-level failure.
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
				if pkg, ok := pkgMap[ev.Package]; ok {
					pkg.Skipped++
				}
			} else if ev.Package != "" {
				if pkg, ok := pkgMap[ev.Package]; ok {
					pkg.ElapsedSec = ev.Elapsed
					pkg.Status = "skip"
				}
			}
		}
	}

	// Build packages list from pkgMap.
	seen := make(map[string]bool)
	for _, pkg := range pkgMap {
		if !seen[pkg.Package] {
			seen[pkg.Package] = true
			result.Packages = append(result.Packages, *pkg)
		}
	}

	raw, _ := json.Marshal(result)
	return string(raw), nil
}

var TestRunDef = agent.ToolDefinition{
	Name:        "test_run",
	Description: "Run Go tests with `go test -json` and return structured results: total passed/failed/skipped counts, per-package breakdowns, and detailed failure output with test name and captured output. Use to verify code changes, iterate on failing tests, or validate a full test suite.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"directory": {
				Type:        "string",
				Description: "Working directory for the test command. Should be the module root.",
			},
			"package": {
				Type:        "string",
				Description: "Go package pattern to test, e.g. \"./...\" (default), \"./internal/agent/...\", or a specific package path.",
			},
			"run": {
				Type:        "string",
				Description: "Regex filter for test names, passed as -run flag. E.g. \"TestAuth\" to run only matching tests.",
			},
			"timeout_seconds": {
				Type:        "integer",
				Description: "Maximum execution time in seconds (1–600). Defaults to 120.",
			},
		},
	},
}
