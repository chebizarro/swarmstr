---
name: multi-lang
description: "Language-specific idiom guides for Go, Python, TypeScript, Rust. Covers non-obvious patterns: project layout, error handling, testing idioms, build quirks, common pitfalls. Use when: (1) writing code in a specific language and need idiomatic patterns, (2) user asks 'what's the Go/Python/etc way to do X', (3) porting code between languages. NOT for: syntax questions (the model already knows syntax)."
when_to_use: "Use when the user asks for language-specific idiomatic patterns or best practices."
user-invocable: true
disable-model-invocation: true
---

# Multi-Language Idiom Guide

The non-obvious things that separate working code from idiomatic code.

## Go

### Project Layout
- `cmd/` for executables, `internal/` for private packages, `pkg/` only if you want external consumers
- One package per directory, package name = directory name
- `_test.go` files in the same package for white-box tests, `_test` package suffix for black-box

### Error Handling
- Return errors, don't panic. `if err != nil { return ..., fmt.Errorf("context: %w", err) }`
- Use `errors.Is` and `errors.As` for error checking, not string comparison
- Sentinel errors for expected conditions, wrapped errors for unexpected ones

### Testing
- Table-driven tests with `t.Run` for subtests
- `t.Helper()` on every test helper function
- `t.Parallel()` for independent tests
- `testdata/` directory for test fixtures

### Pitfalls
- Goroutine leaks: always ensure goroutines can exit (context, done channel)
- Nil interface vs nil pointer: `var err error = (*MyError)(nil)` is non-nil
- Slice append gotcha: append may or may not allocate a new backing array
- `defer` in loops: defers run at function exit, not loop iteration

## Python

### Project Layout
- `src/` layout with `pyproject.toml`
- `__init__.py` for packages, `__main__.py` for runnable modules
- `tests/` at project root, mirroring `src/` structure

### Error Handling
- Specific exceptions over broad `except Exception`
- Context managers (`with`) for resource cleanup
- `raise ... from err` to preserve exception chains

### Testing
- `pytest` over `unittest` — less boilerplate
- Fixtures for setup/teardown, `conftest.py` for shared fixtures
- `@pytest.mark.parametrize` for data-driven tests
- `tmp_path` fixture for filesystem isolation

### Pitfalls
- Mutable default arguments: `def f(x=[])` shares the list across calls
- Late binding closures: `lambda: i` captures variable, not value
- Import cycles: restructure or use local imports
- GIL: use `multiprocessing` or `asyncio` for CPU/IO parallelism

## TypeScript

### Project Layout
- `src/` for source, `dist/` for compiled output
- Barrel exports (`index.ts`) for clean public APIs
- `tsconfig.json` strict mode: `strict: true`

### Error Handling
- Use `Result<T, E>` pattern or throw typed errors
- `unknown` over `any` for caught exceptions
- Zod or similar for runtime type validation at boundaries

### Testing
- Vitest or Jest with TypeScript support
- `describe`/`it` blocks, `beforeEach` for setup
- Mock only external dependencies, not internal modules

### Pitfalls
- `==` vs `===`: always use strict equality
- `any` defeats the type system — use `unknown` + type guards
- Optional chaining (`?.`) returns `undefined`, not `null`
- `Promise` rejections without `.catch()` crash in Node.js

## Rust

### Project Layout
- `src/lib.rs` for libraries, `src/main.rs` for binaries
- `mod.rs` or `module_name.rs` for submodules
- `Cargo.toml` workspaces for multi-crate projects

### Error Handling
- `Result<T, E>` for recoverable errors, `panic!` only for bugs
- `thiserror` for library error types, `anyhow` for application errors
- `?` operator for error propagation

### Testing
- `#[cfg(test)] mod tests` in the same file
- `#[test]` annotation, `assert_eq!` / `assert!`
- Integration tests in `tests/` directory
- `#[should_panic]` for expected panics

### Pitfalls
- Borrow checker: prefer cloning to fighting the compiler, then optimize
- Lifetime elision: learn the three rules before adding explicit lifetimes
- `unwrap()` in production code is a bug waiting to happen
- `String` vs `&str`: own when you need to, borrow when you can
