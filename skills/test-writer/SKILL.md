---
name: test-writer
description: "Generate comprehensive test suites for functions, modules, or APIs. Use when: (1) user asks to write tests, (2) new code lacks test coverage, (3) adding edge case or regression tests, (4) user says 'test this'. NOT for: running existing tests (use verify), debugging test failures (use debug)."
when_to_use: "Use when the user asks to write, add, or generate tests for code."
user-invocable: true
disable-model-invocation: false
---

# Test Writer

Generate tests that catch real bugs, not tests that pass by definition.

## Goal
Produce a test suite that exercises the target code's actual behavior, edge cases, and error paths.

## Workflow

1. **Read the target code** and its dependencies. Understand what it does, not just its signature.
2. **Identify the testing framework** already used in the project. Match it.
3. **Design test cases** across these categories:
   - Happy path (expected inputs → expected outputs)
   - Edge cases (empty, nil, zero, max, unicode, concurrent)
   - Error paths (invalid input, missing deps, timeouts, permission denied)
   - Boundary conditions (off-by-one, overflow, empty collections)
4. **Write the tests** following the project's existing patterns.
5. **Run the tests** with `test_run` or `bash_exec` to verify they compile and pass.

## Language Patterns

### Go
- Table-driven tests with `t.Run` subtests
- `t.Helper()` for test helpers
- `t.TempDir()` for filesystem isolation
- `t.Parallel()` when tests are independent
- Name: `TestFunctionName_Scenario`

### Python
- `pytest` fixtures and parametrize
- `tmp_path` for filesystem isolation
- `pytest.raises` for error assertions
- Name: `test_function_name_scenario`

### TypeScript/JavaScript
- `describe`/`it` blocks (Jest/Vitest)
- `beforeEach`/`afterEach` for setup/teardown
- Mock with `vi.fn()` or `jest.fn()`
- Name: `it('should do X when Y')`

### Rust
- `#[test]` functions in `mod tests`
- `#[should_panic]` for expected panics
- `assert_eq!`, `assert_ne!`, `assert!`
- `tempfile` crate for filesystem isolation

## Guardrails
- Match the project's testing style — don't introduce new frameworks.
- Test behavior, not implementation. Tests should survive refactoring.
- Each test should test one thing and have a descriptive name.
- Prefer real values over mocks. Mock only external dependencies.
- Always run the tests before delivering them.
- If the code under test is untestable, note what refactoring would help.
