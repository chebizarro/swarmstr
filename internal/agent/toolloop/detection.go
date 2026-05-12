// Package toolloop implements tool-call loop detection for agentic LLM tool
// loops.  It is a Go port of OpenClaw's tool-loop-detection.ts, providing
// tool-call detectors (generic repeat, known-poll no-progress, ping-pong), a
// global circuit breaker, and per-turn assistant-text decision thrashing checks.
//
// Usage:
//
//	state := toolloop.NewState()
//	result := toolloop.Detect(state, toolName, params, nil)
//	if result.Stuck && result.Level == toolloop.Critical {
//	    // block the tool call
//	}
//	toolloop.RecordCall(state, toolName, params, toolCallID, nil)
//	// after execution:
//	toolloop.RecordOutcome(state, toolName, params, toolCallID, resultStr, errStr, nil)
package toolloop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

// ─── Types ────────────────────────────────────────────────────────────────────

// DetectorKind identifies which detector flagged the loop.
type DetectorKind string

const (
	GenericRepeat        DetectorKind = "generic_repeat"
	KnownPollNoProgress  DetectorKind = "known_poll_no_progress"
	GlobalCircuitBreaker DetectorKind = "global_circuit_breaker"
	PingPong             DetectorKind = "ping_pong"
	TextDecisionThrash   DetectorKind = "text_decision_thrash"
)

// Level indicates severity: warning allows the call but injects a message,
// critical blocks it entirely.
type Level string

const (
	Warning  Level = "warning"
	Critical Level = "critical"
)

// Result is the output of Detect.
type Result struct {
	Stuck          bool
	Level          Level
	Detector       DetectorKind
	Count          int
	Message        string
	PairedToolName string
	WarningKey     string
}

// Config controls thresholds and which detectors are active.
type Config struct {
	Enabled                       bool
	HistorySize                   int
	WarningThreshold              int
	CriticalThreshold             int
	GlobalCircuitBreakerThreshold int
	Detectors                     DetectorsConfig
	TextThrash                    TextThrashConfig
}

// DetectorsConfig toggles individual detectors.
type DetectorsConfig struct {
	GenericRepeat       bool
	KnownPollNoProgress bool
	PingPong            bool
}

// TextThrashConfig controls response-text decision-thrashing detection.
type TextThrashConfig struct {
	Enabled           bool
	WarningThreshold  int
	CriticalThreshold int
	PrefixWindowChars int
}

// DefaultConfig returns the default configuration (enabled, with all detectors on).
func DefaultConfig() Config {
	return Config{
		Enabled:                       true,
		HistorySize:                   defaultHistorySize,
		WarningThreshold:              defaultWarningThreshold,
		CriticalThreshold:             defaultCriticalThreshold,
		GlobalCircuitBreakerThreshold: defaultGlobalCircuitBreakerThreshold,
		Detectors: DetectorsConfig{
			GenericRepeat:       true,
			KnownPollNoProgress: true,
			PingPong:            true,
		},
		TextThrash: defaultTextThrashConfig(),
	}
}

// ─── Constants ────────────────────────────────────────────────────────────────

const (
	defaultHistorySize                   = 30
	defaultWarningThreshold              = 10
	defaultCriticalThreshold             = 20
	defaultGlobalCircuitBreakerThreshold = 30
	defaultTextThrashWarningThreshold    = 2
	defaultTextThrashCriticalThreshold   = 3
	defaultTextThrashPrefixWindowChars   = 80
)

// ─── Call record ──────────────────────────────────────────────────────────────

// CallRecord is a single entry in the sliding-window tool call history.
type CallRecord struct {
	ToolName   string
	ArgsHash   string
	ToolCallID string
	ResultHash string
	Timestamp  int64
}

// ─── State ────────────────────────────────────────────────────────────────────

// State holds per-session tool call history for loop detection.
// It is safe for concurrent use.
type State struct {
	mu      sync.Mutex
	history []CallRecord
}

// TextThrashState holds per-turn state for response-text decision-thrashing
// detection. It is intentionally not stored in Registry because the detector
// only reasons about repeated assistant responses within one agentic turn.
type TextThrashState struct {
	ConsecutiveCount  int
	LastToolPlanKey   string
	LastPatternFamily string
}

// NewState creates an empty loop detection state.
func NewState() *State {
	return &State{}
}

// NewTextThrashState creates empty per-turn text-thrash detector state.
func NewTextThrashState() *TextThrashState {
	return &TextThrashState{}
}

// Reset clears the history (e.g. on /new or session reset).
func (s *State) Reset() {
	s.mu.Lock()
	s.history = nil
	s.mu.Unlock()
}

// ─── Hashing ──────────────────────────────────────────────────────────────────

// HashToolCall produces a deterministic hash of toolName + params.
func HashToolCall(toolName string, params map[string]any) string {
	return toolName + ":" + digestStable(params)
}

func digestStable(v any) string {
	s := stableStringify(v)
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func stableStringify(v any) string {
	if v == nil {
		return "null"
	}
	switch val := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := "{"
		for i, k := range keys {
			if i > 0 {
				out += ","
			}
			kb, _ := json.Marshal(k)
			out += string(kb) + ":" + stableStringify(val[k])
		}
		return out + "}"
	case []any:
		out := "["
		for i, item := range val {
			if i > 0 {
				out += ","
			}
			out += stableStringify(item)
		}
		return out + "]"
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

// hashOutcome produces a hash of the tool result/error for no-progress detection.
func hashOutcome(result, errStr string) string {
	if errStr != "" {
		return "error:" + digestStable(errStr)
	}
	if result == "" {
		return ""
	}
	return digestStable(result)
}

// ─── Config resolution ────────────────────────────────────────────────────────

func defaultTextThrashConfig() TextThrashConfig {
	return TextThrashConfig{
		Enabled:           true,
		WarningThreshold:  defaultTextThrashWarningThreshold,
		CriticalThreshold: defaultTextThrashCriticalThreshold,
		PrefixWindowChars: defaultTextThrashPrefixWindowChars,
	}
}

func resolveTextThrashConfig(cfg TextThrashConfig) TextThrashConfig {
	if cfg == (TextThrashConfig{}) {
		return defaultTextThrashConfig()
	}
	if cfg.WarningThreshold <= 0 {
		cfg.WarningThreshold = defaultTextThrashWarningThreshold
	}
	if cfg.CriticalThreshold <= 0 {
		cfg.CriticalThreshold = defaultTextThrashCriticalThreshold
	}
	if cfg.CriticalThreshold <= cfg.WarningThreshold {
		cfg.CriticalThreshold = cfg.WarningThreshold + 1
	}
	if cfg.PrefixWindowChars <= 0 {
		cfg.PrefixWindowChars = defaultTextThrashPrefixWindowChars
	}
	return cfg
}

func resolveConfig(cfg *Config) Config {
	if cfg == nil {
		return DefaultConfig()
	}
	c := *cfg
	if c.HistorySize <= 0 {
		c.HistorySize = defaultHistorySize
	}
	if c.WarningThreshold <= 0 {
		c.WarningThreshold = defaultWarningThreshold
	}
	if c.CriticalThreshold <= 0 {
		c.CriticalThreshold = defaultCriticalThreshold
	}
	if c.GlobalCircuitBreakerThreshold <= 0 {
		c.GlobalCircuitBreakerThreshold = defaultGlobalCircuitBreakerThreshold
	}
	if c.CriticalThreshold <= c.WarningThreshold {
		c.CriticalThreshold = c.WarningThreshold + 1
	}
	if c.GlobalCircuitBreakerThreshold <= c.CriticalThreshold {
		c.GlobalCircuitBreakerThreshold = c.CriticalThreshold + 1
	}
	c.TextThrash = resolveTextThrashConfig(c.TextThrash)
	return c
}

// ─── Detectors ────────────────────────────────────────────────────────────────

// getNoProgressStreak counts consecutive identical-result calls (from the tail)
// for a given tool+args combination.
func getNoProgressStreak(history []CallRecord, toolName, argsHash string) (count int, latestResultHash string) {
	for i := len(history) - 1; i >= 0; i-- {
		rec := history[i]
		if rec.ToolName != toolName || rec.ArgsHash != argsHash {
			continue
		}
		if rec.ResultHash == "" {
			continue
		}
		if latestResultHash == "" {
			latestResultHash = rec.ResultHash
			count = 1
			continue
		}
		if rec.ResultHash != latestResultHash {
			break
		}
		count++
	}
	return
}

// getPingPongStreak detects alternating A-B-A-B call patterns.
func getPingPongStreak(history []CallRecord, currentHash string) (count int, pairedToolName string, pairedHash string, noProgressEvidence bool) {
	if len(history) == 0 {
		return 0, "", "", false
	}
	last := history[len(history)-1]

	// Find the "other" call signature (first one that differs from last).
	var otherHash, otherToolName string
	for i := len(history) - 2; i >= 0; i-- {
		if history[i].ArgsHash != last.ArgsHash {
			otherHash = history[i].ArgsHash
			otherToolName = history[i].ToolName
			break
		}
	}
	if otherHash == "" {
		return 0, "", "", false
	}

	// Count alternating tail.
	altCount := 0
	for i := len(history) - 1; i >= 0; i-- {
		var expected string
		if altCount%2 == 0 {
			expected = last.ArgsHash
		} else {
			expected = otherHash
		}
		if history[i].ArgsHash != expected {
			break
		}
		altCount++
	}
	if altCount < 2 {
		return 0, "", "", false
	}

	// Current call should continue the alternation.
	if currentHash != otherHash {
		return 0, "", "", false
	}

	// Check no-progress evidence: all results within each side are identical.
	tailStart := len(history) - altCount
	if tailStart < 0 {
		tailStart = 0
	}
	var firstHashA, firstHashB string
	noProgressEvidence = true
	for i := tailStart; i < len(history); i++ {
		rec := history[i]
		if rec.ResultHash == "" {
			noProgressEvidence = false
			break
		}
		if rec.ArgsHash == last.ArgsHash {
			if firstHashA == "" {
				firstHashA = rec.ResultHash
			} else if firstHashA != rec.ResultHash {
				noProgressEvidence = false
				break
			}
		} else if rec.ArgsHash == otherHash {
			if firstHashB == "" {
				firstHashB = rec.ResultHash
			} else if firstHashB != rec.ResultHash {
				noProgressEvidence = false
				break
			}
		} else {
			noProgressEvidence = false
			break
		}
	}
	if firstHashA == "" || firstHashB == "" {
		noProgressEvidence = false
	}

	return altCount + 1, otherToolName, last.ArgsHash, noProgressEvidence
}

func canonicalPairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

func isKnownPollToolCall(toolName string, params map[string]any) bool {
	if toolName == "command_status" {
		return true
	}
	if toolName != "process" || params == nil {
		return false
	}
	action, _ := params["action"].(string)
	return action == "poll" || action == "log"
}

func resetTextThrashState(state *TextThrashState) {
	if state == nil {
		return
	}
	state.ConsecutiveCount = 0
	state.LastToolPlanKey = ""
	state.LastPatternFamily = ""
}

func compactHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:12]
}

func normalizeTextThrashPrefix(text string, windowChars int) string {
	text = strings.TrimSpace(strings.ToLower(text))
	text = strings.TrimLeft(text, " \t\r\n>\\\"'`*_~-:;,.!?()[]{}")
	text = strings.Join(strings.Fields(text), " ")
	if windowChars <= 0 {
		windowChars = defaultTextThrashPrefixWindowChars
	}
	runes := []rune(text)
	if len(runes) > windowChars {
		text = string(runes[:windowChars])
	}
	return text
}

func hasLeadingTextThrashMarker(prefix, marker string) bool {
	if !strings.HasPrefix(prefix, marker) {
		return false
	}
	if len(prefix) == len(marker) {
		return true
	}
	next := prefix[len(marker)]
	return next == ' ' || strings.ContainsRune(",.:;!?)]}-", rune(next))
}

func recognizeTextThrashPattern(prefix string) (family string, ok bool) {
	markers := []string{
		"actually",
		"wait",
		"hold on",
		"on second thought",
		"let me try again",
		"let me correct",
		"i should instead",
	}
	for _, marker := range markers {
		if hasLeadingTextThrashMarker(prefix, marker) {
			return "self_correction", true
		}
	}
	return "", false
}

// ObserveTextThrash updates per-turn text-thrash state for one assistant
// response and reports warning/critical severity when a self-correction preamble
// repeats with an unchanged ordered tool plan. Callers should pass an empty
// toolPlanKey when there are no planned tool calls, which resets the detector.
func ObserveTextThrash(state *TextThrashState, assistantText string, toolPlanKey string, cfg *Config) Result {
	if state == nil {
		return Result{}
	}
	rc := resolveConfig(cfg)
	if !rc.Enabled || !rc.TextThrash.Enabled {
		resetTextThrashState(state)
		return Result{}
	}

	toolPlanKey = strings.TrimSpace(toolPlanKey)
	prefix := normalizeTextThrashPrefix(assistantText, rc.TextThrash.PrefixWindowChars)
	patternFamily, ok := recognizeTextThrashPattern(prefix)
	if toolPlanKey == "" || prefix == "" || !ok {
		resetTextThrashState(state)
		return Result{}
	}

	if state.LastToolPlanKey == toolPlanKey && state.LastPatternFamily == patternFamily {
		state.ConsecutiveCount++
	} else {
		state.ConsecutiveCount = 1
		state.LastToolPlanKey = toolPlanKey
		state.LastPatternFamily = patternFamily
	}

	if state.ConsecutiveCount < rc.TextThrash.WarningThreshold {
		return Result{}
	}

	level := Warning
	messagePrefix := "WARNING"
	messageSuffix := "Continue only if the tool plan is still useful; otherwise stop retrying and report the issue."
	if state.ConsecutiveCount >= rc.TextThrash.CriticalThreshold {
		level = Critical
		messagePrefix = "CRITICAL"
		messageSuffix = "Execution blocked to prevent repeated decision thrashing without tool-call progress."
	}

	return Result{
		Stuck:      true,
		Level:      level,
		Detector:   TextDecisionThrash,
		Count:      state.ConsecutiveCount,
		Message:    fmt.Sprintf("%s: Repeated assistant self-correction text with an unchanged tool plan %d times. %s", messagePrefix, state.ConsecutiveCount, messageSuffix),
		WarningKey: fmt.Sprintf("text_thrash:%s:%s", patternFamily, compactHash(toolPlanKey)),
	}
}

// ─── Main detection function ──────────────────────────────────────────────────

// Detect checks whether the given tool call appears to be part of a loop.
// This should be called BEFORE executing the tool.
func Detect(state *State, toolName string, params map[string]any, cfg *Config) Result {
	rc := resolveConfig(cfg)
	if !rc.Enabled {
		return Result{}
	}

	state.mu.Lock()
	history := make([]CallRecord, len(state.history))
	copy(history, state.history)
	state.mu.Unlock()

	currentHash := HashToolCall(toolName, params)
	noProgressCount, latestResultHash := getNoProgressStreak(history, toolName, currentHash)
	knownPollTool := isKnownPollToolCall(toolName, params)

	// 1. Global circuit breaker.
	if noProgressCount >= rc.GlobalCircuitBreakerThreshold {
		log.Printf("toolloop: global circuit breaker triggered: %s repeated %d times with no progress", toolName, noProgressCount)
		return Result{
			Stuck:      true,
			Level:      Critical,
			Detector:   GlobalCircuitBreaker,
			Count:      noProgressCount,
			Message:    fmt.Sprintf("CRITICAL: %s has repeated identical no-progress outcomes %d times. Session execution blocked by global circuit breaker to prevent runaway loops.", toolName, noProgressCount),
			WarningKey: fmt.Sprintf("global:%s:%s:%s", toolName, currentHash, latestResultHash),
		}
	}

	// 2. Known-poll no-progress (critical).
	if knownPollTool && rc.Detectors.KnownPollNoProgress && noProgressCount >= rc.CriticalThreshold {
		log.Printf("toolloop: critical polling loop detected: %s repeated %d times", toolName, noProgressCount)
		return Result{
			Stuck:      true,
			Level:      Critical,
			Detector:   KnownPollNoProgress,
			Count:      noProgressCount,
			Message:    fmt.Sprintf("CRITICAL: Called %s with identical arguments and no progress %d times. This appears to be a stuck polling loop. Session execution blocked to prevent resource waste.", toolName, noProgressCount),
			WarningKey: fmt.Sprintf("poll:%s:%s:%s", toolName, currentHash, latestResultHash),
		}
	}

	// 3. Known-poll no-progress (warning).
	if knownPollTool && rc.Detectors.KnownPollNoProgress && noProgressCount >= rc.WarningThreshold {
		log.Printf("toolloop: polling loop warning: %s repeated %d times", toolName, noProgressCount)
		return Result{
			Stuck:      true,
			Level:      Warning,
			Detector:   KnownPollNoProgress,
			Count:      noProgressCount,
			Message:    fmt.Sprintf("WARNING: You have called %s %d times with identical arguments and no progress. Stop polling and either (1) increase wait time between checks, or (2) report the task as failed if the process is stuck.", toolName, noProgressCount),
			WarningKey: fmt.Sprintf("poll:%s:%s:%s", toolName, currentHash, latestResultHash),
		}
	}

	// 4. Ping-pong (critical).
	ppCount, ppPairedName, ppPairedHash, ppNoProgress := getPingPongStreak(history, currentHash)
	ppWarningKey := fmt.Sprintf("pingpong:%s:%s", toolName, currentHash)
	if ppPairedHash != "" {
		ppWarningKey = "pingpong:" + canonicalPairKey(currentHash, ppPairedHash)
	}

	if rc.Detectors.PingPong && ppCount >= rc.CriticalThreshold && ppNoProgress {
		log.Printf("toolloop: critical ping-pong loop detected: alternating calls count=%d currentTool=%s", ppCount, toolName)
		return Result{
			Stuck:          true,
			Level:          Critical,
			Detector:       PingPong,
			Count:          ppCount,
			Message:        fmt.Sprintf("CRITICAL: You are alternating between repeated tool-call patterns (%d consecutive calls) with no progress. This appears to be a stuck ping-pong loop. Session execution blocked to prevent resource waste.", ppCount),
			PairedToolName: ppPairedName,
			WarningKey:     ppWarningKey,
		}
	}

	// 5. Ping-pong (warning).
	if rc.Detectors.PingPong && ppCount >= rc.WarningThreshold {
		log.Printf("toolloop: ping-pong loop warning: alternating calls count=%d currentTool=%s", ppCount, toolName)
		return Result{
			Stuck:          true,
			Level:          Warning,
			Detector:       PingPong,
			Count:          ppCount,
			Message:        fmt.Sprintf("WARNING: You are alternating between repeated tool-call patterns (%d consecutive calls). This looks like a ping-pong loop; stop retrying and report the task as failed.", ppCount),
			PairedToolName: ppPairedName,
			WarningKey:     ppWarningKey,
		}
	}

	// 6. Generic repeat (warning only, non-poll tools).
	recentCount := 0
	for _, h := range history {
		if h.ToolName == toolName && h.ArgsHash == currentHash {
			recentCount++
		}
	}
	if !knownPollTool && rc.Detectors.GenericRepeat && recentCount >= rc.WarningThreshold {
		log.Printf("toolloop: loop warning: %s called %d times with identical arguments", toolName, recentCount)
		return Result{
			Stuck:      true,
			Level:      Warning,
			Detector:   GenericRepeat,
			Count:      recentCount,
			Message:    fmt.Sprintf("WARNING: You have called %s %d times with identical arguments. If this is not making progress, stop retrying and report the task as failed.", toolName, recentCount),
			WarningKey: fmt.Sprintf("generic:%s:%s", toolName, currentHash),
		}
	}

	return Result{}
}

// ─── Recording ────────────────────────────────────────────────────────────────

// RecordCall adds a tool call to the session's sliding-window history.
// Call this AFTER Detect, BEFORE executing the tool.
func RecordCall(state *State, toolName string, params map[string]any, toolCallID string, cfg *Config) {
	rc := resolveConfig(cfg)
	state.mu.Lock()
	defer state.mu.Unlock()

	state.history = append(state.history, CallRecord{
		ToolName:   toolName,
		ArgsHash:   HashToolCall(toolName, params),
		ToolCallID: toolCallID,
		Timestamp:  time.Now().UnixMilli(),
	})
	if len(state.history) > rc.HistorySize {
		state.history = state.history[len(state.history)-rc.HistorySize:]
	}
}

// RecordOutcome attaches the result hash to a previously recorded call so
// no-progress detection can identify identical outcomes.
func RecordOutcome(state *State, toolName string, params map[string]any, toolCallID, result, errStr string, cfg *Config) {
	rh := hashOutcome(result, errStr)
	if rh == "" {
		return
	}

	rc := resolveConfig(cfg)
	argsHash := HashToolCall(toolName, params)

	state.mu.Lock()
	defer state.mu.Unlock()

	matched := false
	for i := len(state.history) - 1; i >= 0; i-- {
		rec := &state.history[i]
		if toolCallID != "" && rec.ToolCallID != toolCallID {
			continue
		}
		if rec.ToolName != toolName || rec.ArgsHash != argsHash {
			continue
		}
		if rec.ResultHash != "" {
			continue
		}
		rec.ResultHash = rh
		matched = true
		break
	}

	if !matched {
		state.history = append(state.history, CallRecord{
			ToolName:   toolName,
			ArgsHash:   argsHash,
			ToolCallID: toolCallID,
			ResultHash: rh,
			Timestamp:  time.Now().UnixMilli(),
		})
	}

	if len(state.history) > rc.HistorySize {
		state.history = state.history[len(state.history)-rc.HistorySize:]
	}
}

// ─── Session registry ─────────────────────────────────────────────────────────

// Registry maps session IDs to loop detection states.
// It is safe for concurrent use.
type Registry struct {
	mu     sync.Mutex
	states map[string]*State
}

// NewRegistry creates an empty session registry.
func NewRegistry() *Registry {
	return &Registry{states: make(map[string]*State)}
}

// Get returns the State for a session, creating one if needed.
func (r *Registry) Get(sessionID string) *State {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.states[sessionID]
	if !ok {
		s = NewState()
		r.states[sessionID] = s
	}
	return s
}

// Remove deletes a session's state (e.g. on /new).
func (r *Registry) Remove(sessionID string) {
	r.mu.Lock()
	delete(r.states, sessionID)
	r.mu.Unlock()
}

// Stats returns tool call statistics for a session.
func Stats(state *State) (totalCalls, uniquePatterns int, mostFrequentTool string, mostFrequentCount int) {
	state.mu.Lock()
	defer state.mu.Unlock()

	type entry struct {
		toolName string
		count    int
	}
	patterns := make(map[string]*entry)
	for _, rec := range state.history {
		e, ok := patterns[rec.ArgsHash]
		if ok {
			e.count++
		} else {
			patterns[rec.ArgsHash] = &entry{toolName: rec.ToolName, count: 1}
		}
	}

	for _, e := range patterns {
		if e.count > mostFrequentCount {
			mostFrequentTool = e.toolName
			mostFrequentCount = e.count
		}
	}
	return len(state.history), len(patterns), mostFrequentTool, mostFrequentCount
}
