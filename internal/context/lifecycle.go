package context

import (
	stdctx "context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ContextProjectionMode describes how assembled context should be projected into
// the runtime model call.
type ContextProjectionMode string

const (
	ContextProjectionPerTurn         ContextProjectionMode = "per_turn"
	ContextProjectionThreadBootstrap ContextProjectionMode = "thread_bootstrap"
)

// ContextProjection reports whether an assembly is volatile per-turn context or
// stable bootstrap material for providers that keep persistent backend threads.
type ContextProjection struct {
	Mode        ContextProjectionMode `json:"mode"`
	Epoch       string                `json:"epoch,omitempty"`
	Stable      bool                  `json:"stable"`
	Description string                `json:"description,omitempty"`
}

// PromptCacheInfo captures basic prompt-cache stability telemetry for assembled
// context. Provider token counters can be recorded elsewhere; this exposes the
// context engine's cacheability hints and break detection.
type PromptCacheInfo struct {
	Retention     string   `json:"retention"` // none, short, long
	LastHitRate   float64  `json:"last_hit_rate,omitempty"`
	CacheBroken   bool     `json:"cache_broken,omitempty"`
	BreakReason   string   `json:"break_reason,omitempty"`
	BreakReasons  []string `json:"break_reasons,omitempty"`
	StaticTokens  int      `json:"static_tokens,omitempty"`
	DynamicTokens int      `json:"dynamic_tokens,omitempty"`
}

// AfterTurnParams is passed to context engines after a successful turn so they
// can persist canonical state, trigger extraction, or compact proactively.
type AfterTurnParams struct {
	Messages       []Message `json:"messages,omitempty"`
	PrePromptCount int       `json:"pre_prompt_count,omitempty"`
	TokenBudget    int       `json:"token_budget,omitempty"`
	CurrentTokens  int       `json:"current_tokens,omitempty"`
	IsHeartbeat    bool      `json:"is_heartbeat,omitempty"`
}

// AfterTurner is implemented by engines that support post-turn maintenance.
type AfterTurner interface {
	AfterTurn(ctx stdctx.Context, sessionID string, params AfterTurnParams) error
}

// SubagentSpawnParams describes how a child session should inherit context.
type SubagentSpawnParams struct {
	ParentSessionID string `json:"parent_session_id"`
	ChildSessionID  string `json:"child_session_id"`
	ContextMode     string `json:"context_mode,omitempty"` // fork (default) or isolated
}

// SubagentPreparation is returned after a child context has been prepared.
type SubagentPreparation struct {
	ParentSessionID   string       `json:"parent_session_id"`
	ChildSessionID    string       `json:"child_session_id"`
	ContextMode       string       `json:"context_mode"`
	InheritedMessages int          `json:"inherited_messages"`
	Rollback          func() error `json:"-"`
}

// SubagentLifecycle is implemented by engines that can fork or isolate child
// agent context state.
type SubagentLifecycle interface {
	PrepareSubagentSpawn(ctx stdctx.Context, params SubagentSpawnParams) (*SubagentPreparation, error)
	OnSubagentEnded(ctx stdctx.Context, childSessionID string, reason string) error
}

// TranscriptRewrite describes one transcript entry correction or redaction.
type TranscriptRewrite struct {
	EntryID string         `json:"entry_id"`
	Role    string         `json:"role,omitempty"`
	Text    string         `json:"text,omitempty"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// TranscriptRewriter is the storage hook used by Maintain to rewrite persisted
// transcript entries when a caller supplies one.
type TranscriptRewriter interface {
	RewriteTranscriptEntries(ctx stdctx.Context, sessionID string, rewrites []TranscriptRewrite) (int, error)
}

// MaintainParams carries background cleanup and transcript rewrite requests.
type MaintainParams struct {
	SessionID                string              `json:"session_id,omitempty"`
	RewriteTranscriptEntries []TranscriptRewrite `json:"rewrite_transcript_entries,omitempty"`
	TranscriptRewriter       TranscriptRewriter  `json:"-"`
	CleanupToolResults       bool                `json:"cleanup_tool_results,omitempty"`
}

// MaintainResult summarizes background context maintenance.
type MaintainResult struct {
	RewrittenEntries int  `json:"rewritten_entries,omitempty"`
	CleanedMessages  int  `json:"cleaned_messages,omitempty"`
	Changed          bool `json:"changed"`
}

// Maintainer is implemented by engines that can run background context cleanup.
type Maintainer interface {
	Maintain(ctx stdctx.Context, sessionID string, params MaintainParams) (MaintainResult, error)
}

// RunAfterTurn invokes an engine's AfterTurn hook when available.
func RunAfterTurn(ctx stdctx.Context, engine Engine, sessionID string, params AfterTurnParams) error {
	if hook, ok := engine.(AfterTurner); ok {
		return hook.AfterTurn(ctx, sessionID, params)
	}
	return nil
}

// PrepareSubagentSpawn invokes an engine's subagent fork hook when available.
func PrepareSubagentSpawn(ctx stdctx.Context, engine Engine, params SubagentSpawnParams) (*SubagentPreparation, error) {
	if hook, ok := engine.(SubagentLifecycle); ok {
		return hook.PrepareSubagentSpawn(ctx, params)
	}
	return &SubagentPreparation{
		ParentSessionID: params.ParentSessionID,
		ChildSessionID:  params.ChildSessionID,
		ContextMode:     normalizeContextMode(params.ContextMode),
	}, nil
}

// OnSubagentEnded invokes an engine's child cleanup hook when available.
func OnSubagentEnded(ctx stdctx.Context, engine Engine, childSessionID, reason string) error {
	if hook, ok := engine.(SubagentLifecycle); ok {
		return hook.OnSubagentEnded(ctx, childSessionID, reason)
	}
	return nil
}

// RunMaintain invokes an engine's maintenance hook when available.
func RunMaintain(ctx stdctx.Context, engine Engine, sessionID string, params MaintainParams) (MaintainResult, error) {
	if hook, ok := engine.(Maintainer); ok {
		return hook.Maintain(ctx, sessionID, params)
	}
	return MaintainResult{}, nil
}

func (e *WindowedEngine) AfterTurn(ctx stdctx.Context, sessionID string, params AfterTurnParams) error {
	if e == nil || params.IsHeartbeat {
		return nil
	}
	if len(params.Messages) > 0 {
		e.mu.Lock()
		msgs := copyMessages(params.Messages)
		if len(msgs) > e.maxMsgs {
			msgs = msgs[len(msgs)-e.maxMsgs:]
		}
		e.sessions[sessionID] = msgs
		e.mu.Unlock()
	}
	if shouldProactivelyCompact(params) {
		_, err := e.Compact(ctx, sessionID)
		return err
	}
	return nil
}

func (e *WindowedEngine) PrepareSubagentSpawn(_ stdctx.Context, params SubagentSpawnParams) (*SubagentPreparation, error) {
	if e == nil {
		return nil, fmt.Errorf("windowed engine is nil")
	}
	mode := normalizeContextMode(params.ContextMode)
	if strings.TrimSpace(params.ParentSessionID) == "" || strings.TrimSpace(params.ChildSessionID) == "" {
		return nil, fmt.Errorf("parent and child session IDs are required")
	}
	e.mu.Lock()
	prev, hadPrev := e.sessions[params.ChildSessionID]
	prevSummary, hadPrevSummary := e.summaries[params.ChildSessionID]
	prevCache, hadPrevCache := e.promptCacheLast[params.ChildSessionID]
	inherited := 0
	if mode == "isolated" {
		delete(e.sessions, params.ChildSessionID)
		delete(e.summaries, params.ChildSessionID)
		delete(e.promptCacheLast, params.ChildSessionID)
	} else {
		parent := copyMessages(e.sessions[params.ParentSessionID])
		e.sessions[params.ChildSessionID] = parent
		inherited = len(parent)
		if summary := e.summaries[params.ParentSessionID]; summary != "" {
			e.summaries[params.ChildSessionID] = summary
		} else {
			delete(e.summaries, params.ChildSessionID)
		}
		delete(e.promptCacheLast, params.ChildSessionID)
	}
	e.mu.Unlock()
	return &SubagentPreparation{
		ParentSessionID:   params.ParentSessionID,
		ChildSessionID:    params.ChildSessionID,
		ContextMode:       mode,
		InheritedMessages: inherited,
		Rollback: func() error {
			e.mu.Lock()
			defer e.mu.Unlock()
			if hadPrev {
				e.sessions[params.ChildSessionID] = copyMessages(prev)
			} else {
				delete(e.sessions, params.ChildSessionID)
			}
			if hadPrevSummary {
				e.summaries[params.ChildSessionID] = prevSummary
			} else {
				delete(e.summaries, params.ChildSessionID)
			}
			if hadPrevCache {
				e.promptCacheLast[params.ChildSessionID] = prevCache
			} else {
				delete(e.promptCacheLast, params.ChildSessionID)
			}
			return nil
		},
	}, nil
}

func (e *WindowedEngine) OnSubagentEnded(_ stdctx.Context, childSessionID string, reason string) error {
	if e == nil || strings.TrimSpace(childSessionID) == "" {
		return nil
	}
	if shouldDiscardChildContext(reason) {
		e.mu.Lock()
		delete(e.sessions, childSessionID)
		delete(e.summaries, childSessionID)
		delete(e.promptCacheLast, childSessionID)
		e.mu.Unlock()
	}
	return nil
}

func (e *WindowedEngine) Maintain(ctx stdctx.Context, sessionID string, params MaintainParams) (MaintainResult, error) {
	if e == nil {
		return MaintainResult{}, nil
	}
	result := MaintainResult{}
	if params.TranscriptRewriter != nil && len(params.RewriteTranscriptEntries) > 0 {
		count, err := params.TranscriptRewriter.RewriteTranscriptEntries(ctx, sessionID, params.RewriteTranscriptEntries)
		if err != nil {
			return result, err
		}
		result.RewrittenEntries = count
	}
	if len(params.RewriteTranscriptEntries) > 0 {
		rewritten := e.rewriteMessages(sessionID, params.RewriteTranscriptEntries)
		if rewritten > result.RewrittenEntries {
			result.RewrittenEntries = rewritten
		}
	}
	if result.RewrittenEntries > 0 {
		e.invalidateSessionSummary(sessionID)
	}
	result.Changed = result.RewrittenEntries > 0 || result.CleanedMessages > 0
	return result, nil
}

func (e *SmallWindowEngine) AfterTurn(_ stdctx.Context, sessionID string, params AfterTurnParams) error {
	if e == nil || params.IsHeartbeat {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	sess := e.getOrCreateSession(sessionID)
	if len(params.Messages) > 0 {
		sess.messages = copyMessages(params.Messages)
	}
	if shouldProactivelyCompact(params) || estimateSessionChars(sess.messages) > e.budget.HistoryMaxChars {
		before := len(sess.messages)
		msgs := copyMessages(sess.messages)
		msgs = e.clearOldToolResults(msgs)
		msgs = e.trimToWindow(msgs)
		sess.messages = msgs
		_ = before
	}
	return nil
}

func (e *SmallWindowEngine) PrepareSubagentSpawn(_ stdctx.Context, params SubagentSpawnParams) (*SubagentPreparation, error) {
	if e == nil {
		return nil, fmt.Errorf("small-window engine is nil")
	}
	mode := normalizeContextMode(params.ContextMode)
	if strings.TrimSpace(params.ParentSessionID) == "" || strings.TrimSpace(params.ChildSessionID) == "" {
		return nil, fmt.Errorf("parent and child session IDs are required")
	}
	e.mu.Lock()
	prev, hadPrev := e.sessions[params.ChildSessionID]
	var prevCopy swSession
	if hadPrev && prev != nil {
		prevCopy = swSession{messages: copyMessages(prev.messages), summary: prev.summary, promptCacheLast: prev.promptCacheLast}
	}
	inherited := 0
	if mode == "isolated" {
		delete(e.sessions, params.ChildSessionID)
	} else {
		parent := e.getOrCreateSession(params.ParentSessionID)
		e.sessions[params.ChildSessionID] = &swSession{messages: copyMessages(parent.messages), summary: parent.summary}
		inherited = len(parent.messages)
	}
	e.mu.Unlock()
	return &SubagentPreparation{
		ParentSessionID:   params.ParentSessionID,
		ChildSessionID:    params.ChildSessionID,
		ContextMode:       mode,
		InheritedMessages: inherited,
		Rollback: func() error {
			e.mu.Lock()
			defer e.mu.Unlock()
			if hadPrev {
				restored := prevCopy
				restored.messages = copyMessages(prevCopy.messages)
				e.sessions[params.ChildSessionID] = &restored
			} else {
				delete(e.sessions, params.ChildSessionID)
			}
			return nil
		},
	}, nil
}

func (e *SmallWindowEngine) OnSubagentEnded(_ stdctx.Context, childSessionID string, reason string) error {
	if e == nil || strings.TrimSpace(childSessionID) == "" {
		return nil
	}
	if shouldDiscardChildContext(reason) {
		e.mu.Lock()
		delete(e.sessions, childSessionID)
		e.mu.Unlock()
	}
	return nil
}

func (e *SmallWindowEngine) Maintain(ctx stdctx.Context, sessionID string, params MaintainParams) (MaintainResult, error) {
	if e == nil {
		return MaintainResult{}, nil
	}
	result := MaintainResult{}
	if params.TranscriptRewriter != nil && len(params.RewriteTranscriptEntries) > 0 {
		count, err := params.TranscriptRewriter.RewriteTranscriptEntries(ctx, sessionID, params.RewriteTranscriptEntries)
		if err != nil {
			return result, err
		}
		result.RewrittenEntries = count
	}
	if len(params.RewriteTranscriptEntries) > 0 {
		rewritten := e.rewriteMessages(sessionID, params.RewriteTranscriptEntries)
		if rewritten > result.RewrittenEntries {
			result.RewrittenEntries = rewritten
		}
	}
	if result.RewrittenEntries > 0 {
		e.invalidateSessionSummary(sessionID)
	}
	if params.CleanupToolResults {
		e.mu.Lock()
		sess := e.getOrCreateSession(sessionID)
		before := countClearedToolResults(sess.messages)
		sess.messages = e.clearOldToolResults(sess.messages)
		after := countClearedToolResults(sess.messages)
		e.mu.Unlock()
		if after > before {
			result.CleanedMessages = after - before
		}
	}
	result.Changed = result.RewrittenEntries > 0 || result.CleanedMessages > 0
	return result, nil
}

func (e *WindowedEngine) rewriteMessages(sessionID string, rewrites []TranscriptRewrite) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	msgs := e.sessions[sessionID]
	changed := 0
	for i := range msgs {
		for _, rw := range rewrites {
			if rw.EntryID == "" || msgs[i].ID != rw.EntryID {
				continue
			}
			if rw.Role != "" {
				msgs[i].Role = rw.Role
			}
			msgs[i].Content = rw.Text
			changed++
		}
	}
	e.sessions[sessionID] = msgs
	return changed
}

func (e *WindowedEngine) invalidateSessionSummary(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.summaries, sessionID)
	delete(e.promptCacheLast, sessionID)
}

func (e *SmallWindowEngine) rewriteMessages(sessionID string, rewrites []TranscriptRewrite) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	sess := e.getOrCreateSession(sessionID)
	changed := 0
	for i := range sess.messages {
		for _, rw := range rewrites {
			if rw.EntryID == "" || sess.messages[i].ID != rw.EntryID {
				continue
			}
			if rw.Role != "" {
				sess.messages[i].Role = rw.Role
			}
			sess.messages[i].Content = rw.Text
			changed++
		}
	}
	return changed
}

func (e *SmallWindowEngine) invalidateSessionSummary(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	sess := e.getOrCreateSession(sessionID)
	sess.summary = ""
	sess.promptCacheLast = ""
}

func (e *WindowedEngine) annotateAssembleResult(sessionID string, result *AssembleResult) {
	e.annotateAssembleResultWithParts(sessionID, result, promptCacheParts{Summary: result.SystemPromptAddition})
}

func (e *WindowedEngine) annotateAssembleResultWithParts(sessionID string, result *AssembleResult, parts promptCacheParts) {
	if result == nil || e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.promptCacheLast == nil {
		e.promptCacheLast = map[string]string{}
	}
	prev := e.promptCacheLast[sessionID]
	info := buildPromptCacheInfo(prev, result.SystemPromptAddition, result.EstimatedTokens, parts)
	e.promptCacheLast[sessionID] = parts.fingerprint(result.SystemPromptAddition)
	result.PromptCache = &info
	result.ContextProjection = buildContextProjection(result.SystemPromptAddition, len(result.Messages), result.EstimatedTokens)
}

func (e *SmallWindowEngine) annotateAssembleResultLocked(sess *swSession, result *AssembleResult) {
	if result == nil || sess == nil {
		return
	}
	parts := promptCacheParts{Summary: result.SystemPromptAddition}
	info := buildPromptCacheInfo(sess.promptCacheLast, result.SystemPromptAddition, result.EstimatedTokens, parts)
	sess.promptCacheLast = parts.fingerprint(result.SystemPromptAddition)
	result.PromptCache = &info
	result.ContextProjection = buildContextProjection(result.SystemPromptAddition, len(result.Messages), result.EstimatedTokens)
}

type promptCacheParts struct {
	Summary                 string
	Recall                  string
	DynamicContextPlacement string
	ProviderProfile         string
}

const promptCacheFingerprintVersion = "prompt-cache-v2"

func (p promptCacheParts) fingerprint(addition string) string {
	if p.Summary == "" && p.Recall == "" && p.DynamicContextPlacement == "" && p.ProviderProfile == "" {
		p.Summary = addition
	}
	return strings.Join([]string{
		promptCacheFingerprintVersion,
		"summary=" + strconv.Quote(p.Summary),
		"recall=" + strconv.Quote(p.Recall),
		"dynamic_context_placement=" + strconv.Quote(p.DynamicContextPlacement),
		"provider_profile=" + strconv.Quote(p.ProviderProfile),
	}, "\n")
}

func parsePromptCacheFingerprint(raw string) (promptCacheParts, bool) {
	if !strings.HasPrefix(raw, promptCacheFingerprintVersion+"\n") {
		return promptCacheParts{}, false
	}
	parts := promptCacheParts{}
	for _, line := range strings.Split(raw, "\n")[1:] {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			unquoted = value
		}
		switch key {
		case "summary":
			parts.Summary = unquoted
		case "recall":
			parts.Recall = unquoted
		case "dynamic_context_placement":
			parts.DynamicContextPlacement = unquoted
		case "provider_profile":
			parts.ProviderProfile = unquoted
		}
	}
	return parts, true
}

func buildPromptCacheInfo(prev, addition string, estimatedTokens int, parts promptCacheParts) PromptCacheInfo {
	dynamicTokens := (len(addition) + 3) / 4
	info := PromptCacheInfo{
		Retention:     "long",
		LastHitRate:   1,
		StaticTokens:  estimatedTokens - dynamicTokens,
		DynamicTokens: dynamicTokens,
	}
	if info.StaticTokens < 0 {
		info.StaticTokens = 0
	}
	if addition != "" {
		info.Retention = "short"
	}
	if addition == "" && estimatedTokens == 0 {
		info.Retention = "none"
	}
	if prev == "" {
		return info
	}
	currentFingerprint := parts.fingerprint(addition)
	if prev == currentFingerprint {
		return info
	}
	reasons := promptCacheBreakReasons(prev, parts, addition)
	if len(reasons) == 0 {
		return info
	}
	info.CacheBroken = true
	info.BreakReasons = reasons
	info.BreakReason = strings.Join(reasons, ",")
	info.LastHitRate = 0
	return info
}

func promptCacheBreakReasons(prev string, current promptCacheParts, addition string) []string {
	previous, ok := parsePromptCacheFingerprint(prev)
	if !ok {
		if prev != current.fingerprint(addition) && prev != addition {
			return []string{"system_prompt_addition_changed"}
		}
		return nil
	}
	reasons := make([]string, 0, 4)
	if previous.Summary != current.Summary {
		reasons = append(reasons, "summary_changed")
	}
	if previous.Recall != current.Recall {
		reasons = append(reasons, "recall_changed")
	}
	if previous.DynamicContextPlacement != current.DynamicContextPlacement {
		reasons = append(reasons, "dynamic_context_placement_changed")
	}
	if previous.ProviderProfile != current.ProviderProfile {
		reasons = append(reasons, "provider_profile_changed")
	}
	return reasons
}

func normalizePromptCacheSummary(text string) string {
	return normalizePromptCacheText(text)
}

func normalizePromptCacheRecall(text string) string {
	text = normalizePromptCacheText(text)
	if text == "" {
		return ""
	}
	blocks := strings.Split(text, "\n\n")
	for i, block := range blocks {
		lines := strings.Split(block, "\n")
		if len(lines) < 2 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "#") {
			continue
		}
		bullets := lines[1:]
		allBullets := true
		for _, line := range bullets {
			trimmed := strings.TrimSpace(line)
			if !(strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ")) {
				allBullets = false
				break
			}
		}
		if allBullets {
			sort.Strings(bullets)
			blocks[i] = strings.Join(append(lines[:1], bullets...), "\n")
		}
	}
	return strings.Join(blocks, "\n\n")
}

func normalizePromptCacheText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if !blank && len(out) > 0 {
				out = append(out, "")
			}
			blank = true
			continue
		}
		out = append(out, strings.Join(strings.Fields(trimmed), " "))
		blank = false
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

func buildContextProjection(addition string, messageCount, estimatedTokens int) *ContextProjection {
	const (
		threadBootstrapMessageThreshold = 40
		threadBootstrapTokenThreshold   = 16_000
	)
	if messageCount >= threadBootstrapMessageThreshold || estimatedTokens >= threadBootstrapTokenThreshold {
		desc := fmt.Sprintf("long session context (%d messages, ~%d tokens) should be bootstrapped into persistent backend threads", messageCount, estimatedTokens)
		if strings.TrimSpace(addition) != "" {
			desc += "; dynamic memory or summary additions remain attached to the bootstrap epoch"
		}
		return &ContextProjection{
			Mode:        ContextProjectionThreadBootstrap,
			Epoch:       fmt.Sprintf("messages:%d:tokens:%d", messageCount, estimatedTokens),
			Stable:      true,
			Description: desc,
		}
	}
	projection := &ContextProjection{Mode: ContextProjectionPerTurn, Stable: false, Description: "transcript context is projected every turn"}
	if strings.TrimSpace(addition) != "" {
		projection.Description = "dynamic memory or summary context included"
	}
	return projection
}

func shouldProactivelyCompact(params AfterTurnParams) bool {
	if params.TokenBudget <= 0 {
		return false
	}
	current := params.CurrentTokens
	if current <= 0 {
		for _, msg := range params.Messages {
			current += estimateMessageTokens(msg)
		}
	}
	return current > 0 && current*100 >= params.TokenBudget*85
}

func normalizeContextMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "isolated" {
		return "isolated"
	}
	return "fork"
}

func shouldDiscardChildContext(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "discard", "discarded", "cancel", "cancelled", "failed":
		return true
	default:
		return false
	}
}

func copyMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]Message, len(messages))
	copy(out, messages)
	for i := range out {
		if len(messages[i].ToolCalls) > 0 {
			out[i].ToolCalls = append([]ToolCallRef(nil), messages[i].ToolCalls...)
		}
	}
	return out
}

func countClearedToolResults(messages []Message) int {
	count := 0
	for _, msg := range messages {
		if msg.Role == "tool" && msg.Content == swClearedMarker {
			count++
		}
	}
	return count
}

var (
	_ AfterTurner       = (*WindowedEngine)(nil)
	_ SubagentLifecycle = (*WindowedEngine)(nil)
	_ Maintainer        = (*WindowedEngine)(nil)
	_ AfterTurner       = (*SmallWindowEngine)(nil)
	_ SubagentLifecycle = (*SmallWindowEngine)(nil)
	_ Maintainer        = (*SmallWindowEngine)(nil)
)
