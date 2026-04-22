package agent

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// ─── Deferred tool loading ────────────────────────────────────────────────────
//
// Tools marked as deferrable are NOT sent inline in every API request.
// Instead, the model discovers them via a ToolSearch-like tool when needed.
//
// This is the single biggest context reduction opportunity — tool schemas
// can burn 20-50% of the tool definition budget on tools the model never
// uses in a given turn.
//
// Ported from src/utils/toolSearch.ts.

const (
	// DefaultAutoToolSearchPercentage is the threshold: when deferred tool
	// definitions would exceed this percentage of the context window's tool
	// budget, they are deferred. Below this threshold, all tools are inlined.
	DefaultAutoToolSearchPercentage = 10

	// ToolSearchToolName is the name of the built-in tool_search tool.
	ToolSearchToolName = "tool_search"
)

// DeferredToolEntry stores a deferred tool's one-line summary and full definition.
type DeferredToolEntry struct {
	// Name is the tool's canonical name.
	Name string
	// Summary is a one-line description used for search matching.
	Summary string
	// Definition is the full tool definition, served when the tool is discovered.
	Definition ToolDefinition
	// Origin tracks where the tool came from (MCP, plugin, etc.).
	Origin ToolOrigin
}

// DeferredToolSet holds tools that are eligible for deferral. It stores their
// full definitions and provides search functionality for the tool_search tool.
type DeferredToolSet struct {
	mu      sync.RWMutex
	entries map[string]DeferredToolEntry
}

// NewDeferredToolSet creates an empty set.
func NewDeferredToolSet() *DeferredToolSet {
	return &DeferredToolSet{entries: make(map[string]DeferredToolEntry)}
}

// Add registers a tool as deferred.
func (s *DeferredToolSet) Add(entry DeferredToolEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[entry.Name] = entry
}

// Remove unregisters a deferred tool.
func (s *DeferredToolSet) Remove(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, name)
}

// Get returns a deferred tool entry by name.
func (s *DeferredToolSet) Get(name string) (DeferredToolEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[name]
	return e, ok
}

// Count returns the number of deferred tools.
func (s *DeferredToolSet) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// Search finds deferred tools matching the query. Uses case-insensitive
// substring matching on name and summary. Returns up to maxResults entries,
// ranked by relevance (name matches first, then summary matches).
func (s *DeferredToolSet) Search(query string, maxResults int) []DeferredToolEntry {
	if maxResults <= 0 {
		maxResults = 5
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Handle "select:name" syntax for direct selection.
	if strings.HasPrefix(query, "select:") {
		names := strings.Split(strings.TrimPrefix(query, "select:"), ",")
		var results []DeferredToolEntry
		for _, name := range names {
			name = strings.TrimSpace(name)
			if e, ok := s.entries[name]; ok {
				results = append(results, e)
			}
		}
		return results
	}

	type scored struct {
		entry DeferredToolEntry
		score int // higher = better match
	}
	var matches []scored

	// Split query into words for matching.
	queryWords := strings.Fields(query)

	for _, entry := range s.entries {
		nameLower := strings.ToLower(entry.Name)
		summaryLower := strings.ToLower(entry.Summary)

		score := 0
		for _, word := range queryWords {
			if strings.Contains(nameLower, word) {
				score += 10 // name match is strongest signal
			}
			if strings.Contains(summaryLower, word) {
				score += 5 // summary match
			}
		}
		if score > 0 {
			matches = append(matches, scored{entry: entry, score: score})
		}
	}

	// Sort by score descending, then name ascending for stability.
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].entry.Name < matches[j].entry.Name
	})

	if len(matches) > maxResults {
		matches = matches[:maxResults]
	}

	results := make([]DeferredToolEntry, len(matches))
	for i, m := range matches {
		results[i] = m.entry
	}
	return results
}

// ListSummaries returns one-line summaries of all deferred tools, sorted by name.
// Used to build the tool_search tool's system prompt description.
func (s *DeferredToolSet) ListSummaries() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0, len(s.entries))
	for name := range s.entries {
		names = append(names, name)
	}
	sort.Strings(names)

	summaries := make([]string, len(names))
	for i, name := range names {
		summaries[i] = name + " — " + s.entries[name].Summary
	}
	return summaries
}

// Definitions returns the full tool definitions for all deferred tools.
func (s *DeferredToolSet) Definitions() []ToolDefinition {
	s.mu.RLock()
	defer s.mu.RUnlock()

	defs := make([]ToolDefinition, 0, len(s.entries))
	for _, entry := range s.entries {
		defs = append(defs, entry.Definition)
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}

// ─── Partitioning logic ───────────────────────────────────────────────────────

// IsDeferrableTool returns true if a tool descriptor should be deferred based
// on its origin. MCP and plugin tools are deferrable by default.
func IsDeferrableTool(desc ToolDescriptor) bool {
	switch desc.Origin.Kind {
	case ToolOriginKindMCP, ToolOriginKindPlugin:
		return true
	default:
		return false
	}
}

// PartitionToolsResult holds the result of partitioning tools into inline and deferred sets.
type PartitionToolsResult struct {
	// Inline tools are sent with every API request.
	Inline []ToolDefinition
	// Deferred tools are held in the DeferredToolSet for on-demand discovery.
	Deferred *DeferredToolSet
	// InlineChars is the estimated total chars for inline tool definitions.
	InlineChars int
	// DeferredChars is the estimated total chars saved by deferring.
	DeferredChars int
}

// PartitionTools splits tool descriptors into inline and deferred sets.
// The autoThresholdPct parameter controls when deferral activates: deferred
// tool definitions must exceed this percentage of the tool budget for deferral
// to kick in. If they don't exceed the threshold, all tools are inlined.
//
// When maxInlineCount > 0 and the inline set exceeds that count, the largest
// non-critical inline tools are force-deferred regardless of their origin.
// This prevents small-context models from being overwhelmed by tool schemas.
//
// Critical tools (by name) are never deferred.
func PartitionTools(
	descriptors []ToolDescriptor,
	toolBudgetChars int,
	autoThresholdPct int,
	criticalToolNames []string,
	maxInlineCount ...int,
) PartitionToolsResult {
	if autoThresholdPct <= 0 {
		autoThresholdPct = DefaultAutoToolSearchPercentage
	}

	maxInline := 0
	if len(maxInlineCount) > 0 && maxInlineCount[0] > 0 {
		maxInline = maxInlineCount[0]
	}

	criticalSet := make(map[string]bool, len(criticalToolNames))
	for _, name := range criticalToolNames {
		criticalSet[name] = true
	}

	// Classify each descriptor.
	var inlineDescs, deferrableDescs []ToolDescriptor
	for _, desc := range descriptors {
		if criticalSet[desc.Name] || !IsDeferrableTool(desc) {
			inlineDescs = append(inlineDescs, desc)
		} else {
			deferrableDescs = append(deferrableDescs, desc)
		}
	}

	// ── Force-deferral for small models ──────────────────────────────────
	// When the total tool count exceeds maxInline, force-defer the largest
	// non-critical inline tools. This handles the common case where most
	// tools are built-in (not MCP/plugin) and would otherwise all stay
	// inline, overwhelming small context windows.
	forcedDeferral := false
	totalCount := len(inlineDescs) + len(deferrableDescs)
	if maxInline > 0 && totalCount > maxInline {
		// Sort inline descs by size (largest first) to defer the biggest.
		type sized struct {
			desc  ToolDescriptor
			chars int
		}
		var criticalInline, regularInline []sized
		for _, desc := range inlineDescs {
			s := sized{desc: desc, chars: EstimateToolDefinitionChars(desc.Definition())}
			if criticalSet[desc.Name] {
				criticalInline = append(criticalInline, s)
			} else {
				regularInline = append(regularInline, s)
			}
		}
		// Sort regular inline by size ascending — keep smallest inline,
		// defer the largest (they cost the most context).
		sort.Slice(regularInline, func(i, j int) bool {
			return regularInline[i].chars < regularInline[j].chars
		})

		// How many inline slots remain after critical tools?
		slotsForRegular := maxInline - len(criticalInline)
		if slotsForRegular < 0 {
			slotsForRegular = 0
		}

		// Force-defer excess regular inline tools.
		if len(regularInline) > slotsForRegular {
			forcedDeferred := regularInline[slotsForRegular:]
			regularInline = regularInline[:slotsForRegular]
			for _, s := range forcedDeferred {
				deferrableDescs = append(deferrableDescs, s.desc)
			}
			forcedDeferral = true
		}

		// Rebuild inlineDescs from critical + surviving regular.
		inlineDescs = make([]ToolDescriptor, 0, len(criticalInline)+len(regularInline))
		for _, s := range criticalInline {
			inlineDescs = append(inlineDescs, s.desc)
		}
		for _, s := range regularInline {
			inlineDescs = append(inlineDescs, s.desc)
		}
	}

	// Estimate chars for deferrable tools.
	deferrableChars := 0
	for _, desc := range deferrableDescs {
		deferrableChars += EstimateToolDefinitionChars(desc.Definition())
	}

	// Check if deferrable tools exceed the threshold.
	// Skip threshold check when force-deferral moved tools — those must stay deferred.
	threshold := toolBudgetChars * autoThresholdPct / 100
	if !forcedDeferral && ((deferrableChars < threshold && len(deferrableDescs) > 0) || len(deferrableDescs) == 0) {
		// Below threshold or no deferrable tools — inline everything.
		allDefs := make([]ToolDefinition, 0, len(inlineDescs)+len(deferrableDescs))
		totalChars := 0
		for _, desc := range inlineDescs {
			def := desc.Definition()
			allDefs = append(allDefs, def)
			totalChars += EstimateToolDefinitionChars(def)
		}
		for _, desc := range deferrableDescs {
			def := desc.Definition()
			allDefs = append(allDefs, def)
			totalChars += EstimateToolDefinitionChars(def)
		}
		return PartitionToolsResult{
			Inline:      allDefs,
			Deferred:    NewDeferredToolSet(),
			InlineChars: totalChars,
		}
	}

	// Deferral is active — build the deferred set.
	deferred := NewDeferredToolSet()
	for _, desc := range deferrableDescs {
		def := desc.Definition()
		deferred.Add(DeferredToolEntry{
			Name:       desc.Name,
			Summary:    truncateStr(desc.Description, 80),
			Definition: def,
			Origin:     desc.Origin,
		})
	}

	// Build inline definitions.
	inlineDefs := make([]ToolDefinition, 0, len(inlineDescs))
	inlineChars := 0
	for _, desc := range inlineDescs {
		def := desc.Definition()
		inlineDefs = append(inlineDefs, def)
		inlineChars += EstimateToolDefinitionChars(def)
	}

	return PartitionToolsResult{
		Inline:        inlineDefs,
		Deferred:      deferred,
		InlineChars:   inlineChars,
		DeferredChars: deferrableChars,
	}
}

// ─── Executor wrapper ─────────────────────────────────────────────────────────

// deferredToolExecutorWrapper intercepts tool_search calls locally and
// delegates everything else to the base executor. This lets the agentic loop
// handle tool discovery without modifying the existing executor chain.
type deferredToolExecutorWrapper struct {
	base       ToolExecutor
	searchFunc ToolFunc
}

func (w *deferredToolExecutorWrapper) Execute(ctx context.Context, call ToolCall) (string, error) {
	if call.Name == ToolSearchToolName {
		return w.searchFunc(ctx, call.Args)
	}
	return w.base.Execute(ctx, call)
}

// Definitions delegates to the base executor so tool-list introspection
// still works (e.g. for allowlist filtering).
func (w *deferredToolExecutorWrapper) Definitions() []ToolDefinition {
	if provider, ok := w.base.(interface{ Definitions() []ToolDefinition }); ok {
		return provider.Definitions()
	}
	return nil
}

// ProviderDescriptors delegates to the base executor.
func (w *deferredToolExecutorWrapper) ProviderDescriptors() []ToolDescriptor {
	if provider, ok := w.base.(interface{ ProviderDescriptors() []ToolDescriptor }); ok {
		return provider.ProviderDescriptors()
	}
	return nil
}

// EffectiveTraits delegates to the base executor for trait resolution.
func (w *deferredToolExecutorWrapper) EffectiveTraits(call ToolCall) (ToolTraits, bool) {
	if call.Name == ToolSearchToolName {
		// tool_search is concurrency-safe and lightweight.
		return ToolTraits{ConcurrencySafe: true}, true
	}
	if resolver, ok := w.base.(interface {
		EffectiveTraits(ToolCall) (ToolTraits, bool)
	}); ok {
		return resolver.EffectiveTraits(call)
	}
	return ToolTraits{}, false
}
