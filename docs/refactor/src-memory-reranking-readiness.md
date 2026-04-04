# Memory Reranking Readiness Gate

## Purpose

This document defines when metiq should, and should not, add `src`-style LLM memory reranking.

It exists to keep reranking as a deliberate follow-on after baseline retrieval quality is measured, not as an opportunistic addition that increases latency and model cost without proving value.

## Canonical `src` behavior

The canonical `src` implementation does two distinct things:

1. It scans available memory headers and builds a manifest of candidate memories.
2. It runs a side-query model call to select relevant filenames from that manifest.

Source anchors:
- `src/memdir/findRelevantMemories.ts:39-141`
- `src/memdir/memdir.ts:272-419`

Important properties of the canonical flow:
- reranking is **model-mediated**, not deterministic token scoring
- the model selects from a **pre-built candidate set**, it does not replace storage/indexing
- the selector is a **separate side query** with structured JSON output
- `src` logs recall-shape telemetry around selection

That is the behavior being deferred here.

## Current metiq baseline

metiq now has deterministic retrieval plus model-facing memory packaging, but **no LLM reranking path**.

Current retrieval and packaging anchors:
- deterministic global search: `swarmstr/internal/memory/index.go:104-146`
- deterministic session search: `swarmstr/internal/memory/index.go:225-271`
- model-facing static memory prompt: `swarmstr/cmd/metiqd/memory_prompt.go:20-86`
- model-facing dynamic recall packaging: `swarmstr/cmd/metiqd/memory_prompt.go:130-197`
- agent run turn assembly: `swarmstr/cmd/metiqd/memory_prompt.go:203-214`
- recall packaging coverage: `swarmstr/cmd/metiqd/memory_context_test.go:54-210`

Today metiq retrieval works like this:
- `Index.Search(...)` scores by token overlap, then breaks ties by recency.
- `Index.SearchSession(...)` prefers session-local matches, then backfills from recent session entries.
- `assembleMemoryRecallContext(...)` formats a bounded recall block with:
  - `From this session`
  - `Related from other sessions`
- the model sees retrieved recall as **dynamic per-turn context**, not as a second-model decision.

This is the baseline that must be measured first.

## Measurement status in metiq

metiq now records bounded deterministic recall samples in local session state so
the current baseline can be reviewed before any reranker bead is opened.

Implementation anchors:
- `swarmstr/cmd/metiqd/memory_prompt.go`
- `swarmstr/internal/store/state/session_store.go`

Session samples are stored in `sessions.json` as `recent_memory_recall` and are:
- bounded to a small recent ring
- redacted (`query_hash`, counts, selected IDs/paths only)
- limited to deterministic retrieval metadata, not raw prompt text

What this bead does **not** add:
- no second model call
- no reranking override
- no ranking change to the current deterministic path

This means metiq can now gather evidence about actual recall behavior while
keeping reranking fully deferred.

## Why reranking is deferred

Reranking is explicitly deferred because the current open question is not “can a model select memories?” — `src` already proves that it can. The open question is whether metiq’s current deterministic retrieval is materially failing in a way that reranking would actually fix.

Adding reranking before measurement would introduce all of the following immediately:
- one extra model call on memory-bearing turns
- extra latency before the main turn starts
- extra token cost on every reranked turn
- a harder-to-debug recall path, because ranking would no longer be explainable from deterministic token overlap and recency alone
- additional recursion/safety risk if the reranker is ever allowed to call tools or depend on mutable runtime state

## Required measurements before any implementation

Reranking must not be implemented until metiq can measure the quality of the current deterministic recall path.

At minimum, the following signals must be available for a review set of real turns:

### 1. Retrieval candidate quality
For each turn where memory recall runs, record:
- user query text or a stable redacted equivalent
- session-scope candidates returned by `SearchSession(...)`
- cross-session candidates returned by `Search(...)`
- final recall block actually injected into the turn
- memory IDs and topics for all returned items

This is needed to answer whether the current shortlist is already good enough.

### 2. Miss classification
For sampled failures, classify the miss into one of these buckets:
- **indexing miss**: the right memory was not retrievable at all
- **ranking miss**: the right memory was retrievable but lost to worse candidates
- **packaging miss**: the right memory was returned but presented poorly to the model
- **staleness miss**: retrieved memory existed but should not have been trusted
- **scope miss**: the right memory was excluded by `user` / `project` / `local` policy

Reranking is only justified for the **ranking miss** bucket.

### 3. Turn-level usefulness
For a reviewed sample of turns, determine whether recalled memory was:
- relevant and helpful
- irrelevant but harmless
- actively misleading
- missing when it should have been present

This review must use the exact recall block the model saw, not only backend search results.

### 4. Latency and cost baseline
Before reranking is considered, measure the baseline cost of the existing memory path:
- percentage of turns that trigger recall packaging
- p50/p95 latency added by deterministic retrieval and prompt assembly
- average recall block size
- average number of recalled items per turn

Without this baseline there is no way to judge whether an extra model call is acceptable.

## Go / no-go gate

Reranking may be considered only if **all** of the following are true:

1. **Baseline retrieval has been measured on real turns**
   - not synthetic examples only
   - not anecdotal “it seems noisy” reports

2. **Ranking misses are a demonstrated problem**
   - the failure review shows that a meaningful portion of harmful recall failures are ranking misses
   - not indexing misses
   - not stale-memory issues
   - not prompt-packaging issues

3. **Deterministic tuning has already been tried first**
   - query tokenization
   - result limits
   - session/global balancing
   - scope filtering
   - prompt formatting improvements

4. **The expected benefit is larger than the operational cost**
   - added model latency is acceptable for recall-bearing turns
   - added token cost is acceptable
   - the change is debuggable enough for production support

5. **The reranker can be implemented as a bounded side query**
   - no tool access
   - no writes
   - no recursive memory calls
   - strict timeout
   - structured output only

If those conditions are not met, reranking remains out of scope.

## Constraints on any future implementation

If a later bead implements reranking, it must follow these constraints:

### Keep deterministic retrieval as the first stage
Reranking must operate on a deterministic shortlist from the existing index.

It must **not** replace:
- `Index.Search(...)`
- `Index.SearchSession(...)`
- scope filtering

The deterministic path remains the source of candidate recall.

### Never run the reranker on the full memory corpus
The reranker must only inspect a bounded candidate set that was already retrieved deterministically.

That keeps:
- latency bounded
- prompt size bounded
- behavior explainable

### Keep the reranker tool-free
The reranker must not have access to:
- `memory_search`
- filesystem or network tools
- agent runtime tools of any kind

It must be a pure side-query classifier, matching the spirit of `src/memdir/findRelevantMemories.ts:39-141`.

### Preserve explainability
Any future reranking implementation must log enough information to compare:
- deterministic candidates
- reranked selections
- final injected recall

Without that comparison, production debugging will regress immediately.

### Do not let reranking mask stale-memory problems
Reranking cannot be treated as a substitute for:
- staleness checks
- scope correctness
- better memory authoring
- better prompt guidance

A better selector does not fix bad or stale memory content.

## Recommended evaluation sequence

1. Measure the current deterministic recall path.
2. Review failed and borderline turns.
3. Fix deterministic retrieval and packaging issues first.
4. Re-measure.
5. Only if ranking misses remain materially harmful, create a new implementation bead for side-query reranking.

## Explicit no-go cases

Do **not** implement reranking if any of the following is still true:
- retrieval failures are mostly missing/stale memories rather than ranking mistakes
- current recall behavior has not been sampled and reviewed
- latency budget for memory-bearing turns is unknown
- additional model spend is not budgeted
- the only evidence is anecdotal operator discomfort with current recall ordering

## Recommendation for follow-on bead shape

When this gate is eventually satisfied, the implementation bead should be framed as:
- add a bounded side-query reranker on top of deterministic memory retrieval
- compare baseline shortlist vs reranked shortlist in logs
- keep model-facing packaging unchanged unless measurement shows packaging is the actual problem

Until then, metiq should continue to rely on:
- deterministic indexed retrieval
- scoped filtering
- model-facing static memory guidance
- model-facing dynamic recall packaging
