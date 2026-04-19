---
name: perf-profile
description: "Performance profiling and analysis: identify hot paths, memory leaks, N+1 queries, benchmark before/after. Use when: (1) user reports slowness, (2) optimizing a critical path, (3) benchmarking changes, (4) diagnosing memory issues. NOT for: general debugging (use debug), code review (use code-review)."
when_to_use: "Use when the user asks about performance, profiling, benchmarks, or optimization."
user-invocable: true
disable-model-invocation: true
---

# Performance Profile

Measure first, optimize second. Never optimize without data.

## Goal
Identify the actual bottleneck and quantify the improvement.

## Workflow

1. **Establish baseline.** Run benchmarks or time the operation before changing anything.
2. **Profile.** Use language-appropriate tools to find the hot path.
3. **Identify the bottleneck.** Focus on the single biggest contributor.
4. **Optimize.** Make one change at a time.
5. **Measure again.** Compare against the baseline. If not faster, revert.

## Language Tools

### Go
- `go test -bench . -benchmem` for microbenchmarks
- `go tool pprof` for CPU and memory profiles
- `runtime/pprof` or `net/http/pprof` for production profiling
- `go test -trace trace.out` for goroutine analysis

### Python
- `cProfile` / `profile` for function-level profiling
- `py-spy` for sampling profiler (no code changes)
- `memory_profiler` for memory usage
- `timeit` for microbenchmarks

### Node.js/TypeScript
- `--prof` flag + `--prof-process` for V8 profiles
- Chrome DevTools for flamegraphs
- `clinic.js` for automatic profiling
- `console.time` / `performance.now` for manual timing

## Common Patterns to Check
- N+1 queries (database calls in loops)
- Unnecessary allocations in hot paths
- String concatenation in loops (use builders)
- Unbounded caches or growing data structures
- Synchronous I/O blocking event loops
- Missing connection pooling
- Repeated computation (memoize)

## Guardrails
- Profile in conditions that match production (same data scale, concurrency).
- Don't optimize code that runs once or rarely.
- Always measure before and after — gut feelings about performance are wrong.
- Prefer algorithmic improvements (O(n²) → O(n log n)) over micro-optimizations.
