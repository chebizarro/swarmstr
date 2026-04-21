package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"metiq/internal/agent/toolloop"
)

// ─── Shared agentic tool loop ─────────────────────────────────────────────────
//
// RunAgenticLoop drives the tool→LLM→tool cycle for any ChatProvider.
// It replaces the duplicated loops that were previously embedded in each
// provider's Generate() method.

// AgenticLoopConfig configures the shared agentic tool loop.
type AgenticLoopConfig struct {
	// Provider makes single LLM calls.
	Provider ChatProvider
	// InitialMessages is the starting message list (system + history + user).
	InitialMessages []LLMMessage
	// Tools is the list of tool definitions for native function calling.
	Tools []ToolDefinition
	// Executor runs tool calls. If nil, the loop is skipped.
	Executor ToolExecutor
	// Options configures each LLM call (max_tokens, thinking, caching).
	Options ChatOptions
	// MaxIterations caps the number of tool→LLM round-trips.
	// Default: tier-appropriate (Micro=5, Small=10, Standard=30).
	// Falls back to 30 when ModelID is empty or unrecognized.
	MaxIterations int
	// ModelID is used to resolve tier-appropriate defaults when MaxIterations
	// is zero. Set from the provider's model string.
	ModelID string
	// ForceText if true, makes a final LLM call with Tools=nil when the loop
	// exhausts MaxIterations without producing text. This forces the model to
	// summarise its findings instead of returning an error.
	ForceText bool
	// LogPrefix is prepended to log messages (e.g. "anthropic", "openai").
	LogPrefix string
	// SessionID and TurnID correlate runtime tool lifecycle events back to the
	// enclosing metiq turn when ToolEventSink is set.
	SessionID string
	TurnID    string
	// ToolEventSink receives canonical start/progress/result/error tool signals.
	ToolEventSink ToolLifecycleSink
	// ContextWindowTokens bounds tool-result context guards between LLM calls.
	ContextWindowTokens int
	// Trace carries task/run/step correlation IDs for observability.
	Trace TraceContext
	// LastAssistantTime is the timestamp of the last assistant message in
	// the conversation. Used by the time-based microcompact trigger: when
	// the gap since this time exceeds the threshold, an aggressive
	// micro-compaction pass runs before the first LLM call.
	// Zero value means "unknown" — the time-based trigger is skipped.
	LastAssistantTime time.Time
	// TimeBasedMCConfig configures the time-gap microcompact trigger.
	// Nil or zero-value config uses DefaultTimeBasedMCConfig.
	TimeBasedMCConfig *TimeBasedMCConfig
	// DeferredTools holds tool definitions that are NOT sent inline with every
	// API request. When non-nil, the loop automatically registers a tool_search
	// built-in tool and dynamically adds discovered tools to subsequent LLM
	// calls. Callers should populate this via PartitionTools.
	DeferredTools *DeferredToolSet
}

// ToolExecResult holds the outcome of a single tool execution.
type ToolExecResult struct {
	ToolCallID  string
	Content     string
	LoopBlocked bool // true if loop detection returned CRITICAL
}

type toolTraitResolver interface {
	EffectiveTraits(ToolCall) (ToolTraits, bool)
}

type toolLoopResolver interface {
	PrepareLoopExecution(context.Context, ToolCall) (toolloop.Result, bool)
	RecordLoopOutcome(context.Context, ToolCall, string, string)
}

type toolCallBatch struct {
	isConcurrencySafe bool
	calls             []ToolCall
}

// RunAgenticLoop executes the agentic tool loop:
//  1. Call the LLM
//  2. If the response requests tool use, execute tools in parallel
//  3. Append tool results and call the LLM again
//  4. Repeat until the model produces text or MaxIterations is reached
//  5. Optionally force a text response when the loop is exhausted
func RunAgenticLoop(ctx context.Context, cfg AgenticLoopConfig) (*LLMResponse, error) {
	if cfg.MaxIterations <= 0 {
		profile := ResolveModelContext(cfg.ModelID)
		cfg.MaxIterations = profile.MaxAgenticIterations
		if cfg.MaxIterations <= 0 {
			cfg.MaxIterations = 30
		}
	}
	if cfg.LogPrefix == "" {
		cfg.LogPrefix = "agentic"
	}

	messages := cfg.InitialMessages

	// Time-based micro-compaction: when the gap since the last assistant
	// message exceeds the threshold, the prompt cache has expired and the
	// full prefix will be rewritten. Clear old tool results before the
	// first call to shrink what gets rewritten — the clearing is free.
	{
		tbConfig := DefaultTimeBasedMCConfig
		if cfg.TimeBasedMCConfig != nil {
			tbConfig = *cfg.TimeBasedMCConfig
		}
		if !cfg.LastAssistantTime.IsZero() {
			mcResult := TimeBasedMicrocompact(messages, tbConfig, cfg.LastAssistantTime)
			if mcResult.Cleared > 0 {
				messages = mcResult.Messages
				log.Printf("%s: time-based micro-compact cleared %d tool results (%d chars freed, gap=%s)",
					cfg.LogPrefix, mcResult.Cleared, mcResult.CharsBefore-mcResult.CharsAfter,
					time.Since(cfg.LastAssistantTime).Truncate(time.Minute))
			}
		}
	}

	// ── Deferred tool loading integration ──────────────────────────────────
	// When deferred tools are present, wrap the executor so the model can
	// discover them via tool_search. Discovered tools are dynamically added
	// to the tools list for subsequent LLM calls.
	activeTools := cfg.Tools
	if cfg.DeferredTools != nil && cfg.DeferredTools.Count() > 0 {
		// Add tool_search definition to the inline tools.
		toolSearchDef := ToolSearchDefinition(cfg.DeferredTools)
		activeTools = append([]ToolDefinition{toolSearchDef}, activeTools...)

		// Wrap the executor to handle tool_search calls locally.
		searchFunc := ToolSearchFunc(cfg.DeferredTools, func(discovered []ToolDefinition) {
			// Append newly-discovered tool definitions. Dedup by name.
			seen := make(map[string]bool, len(activeTools))
			for _, t := range activeTools {
				seen[t.Name] = true
			}
			for _, d := range discovered {
				if !seen[d.Name] {
					activeTools = append(activeTools, d)
					seen[d.Name] = true
					log.Printf("%s: deferred tool discovered: %s", cfg.LogPrefix, d.Name)
				}
			}
		})
		cfg.Executor = &deferredToolExecutorWrapper{
			base:       cfg.Executor,
			searchFunc: searchFunc,
		}
		log.Printf("%s: deferred tool loading active (%d deferred, %d inline)",
			cfg.LogPrefix, cfg.DeferredTools.Count(), len(activeTools))
	}

	// historyDelta accumulates the ordered assistant/tool messages produced
	// during this turn so callers can persist them for future context.
	var historyDelta []ConversationMessage

	// Initial LLM call.
	resp, err := cfg.Provider.Chat(ctx, GuardToolResultMessages(messages, cfg.ContextWindowTokens), activeTools, cfg.Options)
	if err != nil {
		return nil, err
	}

	totalUsage := resp.Usage

	// If no tool calls or no executor, return immediately.
	// For plain text responses, emit a single assistant message in the delta.
	if !resp.NeedsToolResults || len(resp.ToolCalls) == 0 || cfg.Executor == nil {
		if resp.Content != "" {
			resp.HistoryDelta = []ConversationMessage{{Role: "assistant", Content: resp.Content}}
		}
		resp.Outcome = TurnOutcomeCompleted
		resp.StopReason = TurnStopReasonModelText
		return resp, nil
	}

	// Save the preamble text for history (e.g. "Let me check...") before
	// clearing it from the user-visible reply.  The preamble is preserved in
	// HistoryDelta so future turns can see what the model said alongside its
	// tool calls, but it is NOT returned as the final reply text.
	toolPreamble := resp.Content
	resp.Content = ""
	calls := resp.ToolCalls

	loopBlocked := false

	for iter := 0; iter < cfg.MaxIterations; iter++ {
		// Log which tools are being called.
		toolNames := make([]string, len(calls))
		for i, c := range calls {
			toolNames[i] = c.Name
		}
		log.Printf("%s: agentic loop iter=%d/%d tools=%v", cfg.LogPrefix, iter+1, cfg.MaxIterations, toolNames)

		// Build the assistant tool-call ConversationMessage for history delta.
		// On the first iteration, include the saved preamble text; on
		// subsequent iterations use resp.Content (which is cleared to "").
		deltaContent := resp.Content
		if iter == 0 && toolPreamble != "" {
			deltaContent = toolPreamble
		}
		refs := make([]ToolCallRef, len(calls))
		for i, c := range calls {
			refs[i] = ToolCallToRef(c)
		}
		historyDelta = append(historyDelta, ConversationMessage{
			Role:      "assistant",
			Content:   deltaContent,
			ToolCalls: refs,
		})

		// Append the assistant's tool-use message.
		messages = append(messages, LLMMessage{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: calls,
		})

		// Execute tool calls using src-shaped batch partitioning: consecutive
		// concurrency-safe calls run together, others run serially.
		results := executeToolBatches(ctx, cfg.Executor, calls, cfg.SessionID, cfg.TurnID, cfg.ToolEventSink, cfg.Trace)

		// Append tool results and check for loop blocking.
		for _, r := range results {
			messages = append(messages, LLMMessage{
				Role:       "tool",
				Content:    r.Content,
				ToolCallID: r.ToolCallID,
			})
			historyDelta = append(historyDelta, ConversationMessage{
				Role:       "tool",
				Content:    r.Content,
				ToolCallID: r.ToolCallID,
			})
			if r.LoopBlocked {
				log.Printf("%s: loop detector blocked tool, breaking agentic loop", cfg.LogPrefix)
				loopBlocked = true
			}
		}

		if loopBlocked {
			break
		}

		// Apply micro-compaction before the next LLM call to free context
		// space consumed by old tool results. Runs universally for all model
		// sizes — the budget's CompactionThreshold scales with window size
		// so large models compact later while small models compact earlier.
		{
			windowTokens := cfg.ContextWindowTokens
			if windowTokens <= 0 {
				windowTokens = 200_000 // safe default
			}
			budget := ComputeContextBudget(ProfileFromContextWindowTokens(windowTokens))
			estChars := estimateMessageChars(messages)
			thresholdPct := int(budget.CompactionThreshold * 100)
			if estChars*100/max(1, budget.EffectiveChars) > thresholdPct {
				mcResult := MicroCompactMessages(messages, MicroCompactOptions{
					KeepRecent:  budget.MicroCompactKeepRecent,
					TargetChars: budget.HistoryMax,
				})
				if mcResult.Cleared > 0 {
					messages = mcResult.Messages
					log.Printf("%s: micro-compact cleared %d tool results (%d chars freed)",
						cfg.LogPrefix, mcResult.Cleared, mcResult.CharsBefore-mcResult.CharsAfter)
				}
			}
		}

		// Next LLM call — use activeTools which may have grown via tool_search.
		resp, err = cfg.Provider.Chat(ctx, GuardToolResultMessages(messages, cfg.ContextWindowTokens), activeTools, cfg.Options)
		if err != nil {
			log.Printf("%s: agentic loop LLM error iter=%d: %v", cfg.LogPrefix, iter+1, err)
			// Return partial history so callers can persist completed tool work.
			outcome := TurnOutcomeFailed
			stopReason := TurnStopReasonProviderError
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				outcome = TurnOutcomeAborted
				stopReason = TurnStopReasonCancelled
			}
			return nil, &TurnExecutionError{
				Cause: err,
				Partial: TurnResult{
					HistoryDelta: historyDelta,
					Outcome:      outcome,
					StopReason:   stopReason,
					Usage: TurnUsage{
						InputTokens:         totalUsage.InputTokens,
						OutputTokens:        totalUsage.OutputTokens,
						CacheReadTokens:     totalUsage.CacheReadTokens,
						CacheCreationTokens: totalUsage.CacheCreationTokens,
					},
				},
			}
		}

		totalUsage.InputTokens += resp.Usage.InputTokens
		totalUsage.OutputTokens += resp.Usage.OutputTokens
		totalUsage.CacheReadTokens += resp.Usage.CacheReadTokens
		totalUsage.CacheCreationTokens += resp.Usage.CacheCreationTokens
		calls = resp.ToolCalls

		// If the model produced text (no more tool calls), we're done.
		if !resp.NeedsToolResults || len(calls) == 0 {
			if resp.Content != "" {
				historyDelta = append(historyDelta, ConversationMessage{
					Role:    "assistant",
					Content: resp.Content,
				})
			}
			resp.Usage = totalUsage
			resp.HistoryDelta = historyDelta
			resp.Outcome = TurnOutcomeCompletedWithTools
			resp.StopReason = TurnStopReasonModelText
			return resp, nil
		}

		// Clear preamble text for next iteration.
		resp.Content = ""
	}

	// Loop exhausted or blocked — attempt force-summary.
	if cfg.ForceText && (resp == nil || resp.Content == "") {
		summaryResp, summaryDelta := forceSummary(ctx, cfg, messages, calls, totalUsage)
		if summaryResp != nil {
			summaryResp.HistoryDelta = append(historyDelta, summaryDelta...)
			if summaryResp.Content != "" {
				summaryResp.HistoryDelta = append(summaryResp.HistoryDelta, ConversationMessage{
					Role:    "assistant",
					Content: summaryResp.Content,
				})
			}
			summaryResp.Outcome = TurnOutcomeForcedSummary
			summaryResp.StopReason = TurnStopReasonForcedSummary
			return summaryResp, nil
		}
	}

	// If still no text, return a failure message.
	if resp == nil || resp.Content == "" {
		failContent := "I wasn't able to complete this — the tool calls kept looping without producing a result. Please try rephrasing or check that the external service is responding."
		historyDelta = append(historyDelta, ConversationMessage{
			Role:    "assistant",
			Content: failContent,
		})
		return &LLMResponse{
			Content:      failContent,
			Usage:        totalUsage,
			HistoryDelta: historyDelta,
			Outcome:      blockedOutcome(loopBlocked),
			StopReason:   blockedStopReason(loopBlocked),
		}, nil
	}

	if resp.Content != "" {
		historyDelta = append(historyDelta, ConversationMessage{
			Role:    "assistant",
			Content: resp.Content,
		})
	}
	resp.Usage = totalUsage
	resp.HistoryDelta = historyDelta
	resp.Outcome = TurnOutcomeCompletedWithTools
	resp.StopReason = TurnStopReasonModelText
	return resp, nil
}

// executeToolBatches partitions calls using canonical src-style scheduling:
// consecutive concurrency-safe calls are grouped into parallel batches, while
// all other calls run serially. Returned results preserve the original call order.
func executeToolBatches(ctx context.Context, executor ToolExecutor, calls []ToolCall, sessionID, turnID string, sink ToolLifecycleSink, trace TraceContext) []ToolExecResult {
	if sessionID == "" {
		sessionID = SessionIDFromContext(ctx)
	}
	batches := partitionToolCalls(executor, calls)
	results := make([]ToolExecResult, 0, len(calls))
	concurrencyLimit := getMaxToolUseConcurrency()
	for batchIndex, batch := range batches {
		emitSchedulerEvents(batch, batchIndex, len(batches), concurrencyLimit, sessionID, turnID, sink, trace)
		if batch.isConcurrencySafe {
			results = append(results, executeToolBatchParallel(ctx, executor, batch.calls, concurrencyLimit, sessionID, turnID, sink, trace)...)
			continue
		}
		results = append(results, executeToolBatchSerial(ctx, executor, batch.calls, sessionID, turnID, sink, trace)...)
	}
	return results
}

func emitSchedulerEvents(batch toolCallBatch, batchIndex, batchCount, concurrencyLimit int, sessionID, turnID string, sink ToolLifecycleSink, trace TraceContext) {
	mode := "serial"
	limit := 0
	if batch.isConcurrencySafe {
		mode = "parallel"
		limit = concurrencyLimit
	}
	for i, call := range batch.calls {
		emitToolLifecycleEvent(sink, ToolLifecycleEvent{
			Type:       ToolLifecycleEventProgress,
			TS:         time.Now().UnixMilli(),
			SessionID:  sessionID,
			TurnID:     turnID,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Trace:     trace,
			Data: ToolSchedulerDecision{
				Kind:             ToolDecisionKindScheduler,
				Mode:             mode,
				BatchIndex:       batchIndex,
				BatchCount:       batchCount,
				BatchSize:        len(batch.calls),
				BatchPosition:    i,
				ConcurrencySafe:  batch.isConcurrencySafe,
				ConcurrencyLimit: limit,
			},
		})
	}
}

func partitionToolCalls(executor ToolExecutor, calls []ToolCall) []toolCallBatch {
	if len(calls) == 0 {
		return nil
	}
	resolver, _ := executor.(toolTraitResolver)
	batches := make([]toolCallBatch, 0, len(calls))
	for _, call := range calls {
		isConcurrencySafe := false
		if resolver != nil {
			if traits, ok := resolver.EffectiveTraits(call); ok {
				isConcurrencySafe = traits.ConcurrencySafe
			}
		}
		if isConcurrencySafe && len(batches) > 0 && batches[len(batches)-1].isConcurrencySafe {
			batches[len(batches)-1].calls = append(batches[len(batches)-1].calls, call)
			continue
		}
		batches = append(batches, toolCallBatch{isConcurrencySafe: isConcurrencySafe, calls: []ToolCall{call}})
	}
	return batches
}

func executeToolBatchParallel(ctx context.Context, executor ToolExecutor, calls []ToolCall, concurrencyLimit int, sessionID, turnID string, sink ToolLifecycleSink, trace TraceContext) []ToolExecResult {
	results := make([]ToolExecResult, len(calls))
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrencyLimit)
	for i, call := range calls {
		results[i].ToolCallID = call.ID
		wg.Add(1)
		go func(idx int, c ToolCall) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[idx] = executeSingleToolCall(ctx, executor, c, sessionID, turnID, sink, trace)
		}(i, call)
	}
	wg.Wait()
	return results
}

func executeToolBatchSerial(ctx context.Context, executor ToolExecutor, calls []ToolCall, sessionID, turnID string, sink ToolLifecycleSink, trace TraceContext) []ToolExecResult {
	results := make([]ToolExecResult, len(calls))
	for i, call := range calls {
		results[i] = executeSingleToolCall(ctx, executor, call, sessionID, turnID, sink, trace)
	}
	return results
}

func executeSingleToolCall(ctx context.Context, executor ToolExecutor, call ToolCall, sessionID, turnID string, sink ToolLifecycleSink, trace TraceContext) ToolExecResult {
	result := ToolExecResult{ToolCallID: call.ID}
	if sessionID == "" {
		sessionID = SessionIDFromContext(ctx)
	}
	loopAware, _ := executor.(toolLoopResolver)
	var loopResult toolloop.Result
	var loopEnabled bool
	if loopAware != nil {
		loopResult, loopEnabled = loopAware.PrepareLoopExecution(ctx, call)
		if loopEnabled && loopResult.Stuck {
			decision := ToolLoopDecision{
				Kind:           ToolDecisionKindLoopDetection,
				Blocked:        loopResult.Level == toolloop.Critical,
				Level:          string(loopResult.Level),
				Detector:       string(loopResult.Detector),
				Count:          loopResult.Count,
				WarningKey:     loopResult.WarningKey,
				PairedToolName: loopResult.PairedToolName,
				Message:        loopResult.Message,
			}
			emitToolLifecycleEvent(sink, ToolLifecycleEvent{
				Type:       ToolLifecycleEventProgress,
				TS:         time.Now().UnixMilli(),
				SessionID:  sessionID,
				TurnID:     turnID,
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Trace:     trace,
				Data:       decision,
			})
			if loopResult.Level == toolloop.Critical {
				log.Printf("toolloop: BLOCKED tool=%s session=%s detector=%s count=%d", call.Name, sessionID, loopResult.Detector, loopResult.Count)
				result.Content = "error: " + loopResult.Message
				result.LoopBlocked = true
				emitToolLifecycleEvent(sink, ToolLifecycleEvent{
					Type:       ToolLifecycleEventError,
					TS:         time.Now().UnixMilli(),
					SessionID:  sessionID,
					TurnID:     turnID,
					ToolCallID: call.ID,
					ToolName:   call.Name,
					Trace:     trace,
					Error:      loopResult.Message,
					Data:       decision,
				})
				return result
			}
			log.Printf("toolloop: WARNING tool=%s session=%s detector=%s count=%d", call.Name, sessionID, loopResult.Detector, loopResult.Count)
		}
	}
	emitToolLifecycleEvent(sink, ToolLifecycleEvent{
		Type:       ToolLifecycleEventStart,
		TS:         time.Now().UnixMilli(),
		SessionID:  sessionID,
		TurnID:     turnID,
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Trace:     trace,
	})
	value, execErr := executor.Execute(ctx, call)
	if execErr != nil {
		errMsg := execErr.Error()
		result.Content = "error: " + errMsg
		log.Printf("tool %s error: %v", call.Name, execErr)
		emitToolLifecycleEvent(sink, ToolLifecycleEvent{
			Type:       ToolLifecycleEventError,
			TS:         time.Now().UnixMilli(),
			SessionID:  sessionID,
			TurnID:     turnID,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Trace:     trace,
			Error:      errMsg,
		})
		if loopAware != nil {
			loopAware.RecordLoopOutcome(ctx, call, value, errMsg)
		}
		if isCriticalToolError(execErr) {
			result.LoopBlocked = true
		}
		return result
	}
	if loopAware != nil {
		loopAware.RecordLoopOutcome(ctx, call, value, "")
	}
	if loopEnabled && loopResult.Stuck && loopResult.Level == toolloop.Warning {
		value = "[LOOP DETECTION] " + loopResult.Message + "\n\n" + value
	}
	result.Content = value
	emitToolLifecycleEvent(sink, ToolLifecycleEvent{
		Type:       ToolLifecycleEventResult,
		TS:         time.Now().UnixMilli(),
		SessionID:  sessionID,
		TurnID:     turnID,
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Trace:     trace,
		Result:     value,
	})
	return result
}

// forceSummary makes a final LLM call with Tools=nil, forcing the model to
// produce a text response. Any pending tool calls are executed first so the
// model has their results as context.
func forceSummary(ctx context.Context, cfg AgenticLoopConfig, messages []LLMMessage, pendingCalls []ToolCall, usage ProviderUsage) (*LLMResponse, []ConversationMessage) {
	log.Printf("%s: agentic loop exhausted, forcing summary", cfg.LogPrefix)

	var summaryDelta []ConversationMessage

	// Execute any remaining pending tool calls.
	if len(pendingCalls) > 0 && cfg.Executor != nil {
		refs := make([]ToolCallRef, len(pendingCalls))
		for i, c := range pendingCalls {
			refs[i] = ToolCallToRef(c)
		}
		summaryDelta = append(summaryDelta, ConversationMessage{Role: "assistant", ToolCalls: refs})
		// Append the assistant message with pending calls.
		messages = append(messages, LLMMessage{
			Role:      "assistant",
			ToolCalls: pendingCalls,
		})

		results := executeToolBatches(ctx, cfg.Executor, pendingCalls, cfg.SessionID, cfg.TurnID, cfg.ToolEventSink, cfg.Trace)
		for _, r := range results {
			messages = append(messages, LLMMessage{
				Role:       "tool",
				Content:    r.Content,
				ToolCallID: r.ToolCallID,
			})
			summaryDelta = append(summaryDelta, ConversationMessage{
				Role:       "tool",
				Content:    r.Content,
				ToolCallID: r.ToolCallID,
			})
		}
	}

	// Add summary prompt.
	messages = append(messages, LLMMessage{
		Role:    "user",
		Content: "You have used all available tool calls. Please summarise your findings and give a final answer to the user now. Do not attempt any more tool calls.",
	})

	// Call without tools so the model MUST produce text.
	opts := cfg.Options
	summaryResp, err := cfg.Provider.Chat(ctx, GuardToolResultMessages(messages, cfg.ContextWindowTokens), nil, opts)
	if err != nil {
		log.Printf("%s: force-summary error: %v", cfg.LogPrefix, err)
		return nil, nil
	}
	if summaryResp == nil || summaryResp.Content == "" {
		return nil, nil
	}

	summaryResp.Usage = ProviderUsage{
		InputTokens:         usage.InputTokens + summaryResp.Usage.InputTokens,
		OutputTokens:        usage.OutputTokens + summaryResp.Usage.OutputTokens,
		CacheReadTokens:     usage.CacheReadTokens + summaryResp.Usage.CacheReadTokens,
		CacheCreationTokens: usage.CacheCreationTokens + summaryResp.Usage.CacheCreationTokens,
	}
	return summaryResp, summaryDelta
}

func getMaxToolUseConcurrency() int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("CLAUDE_CODE_MAX_TOOL_USE_CONCURRENCY"))); err == nil && v > 0 {
		return v
	}
	return 10
}

func blockedOutcome(loopBlocked bool) TurnOutcome {
	if loopBlocked {
		return TurnOutcomeBlocked
	}
	return TurnOutcomeFailed
}

func blockedStopReason(loopBlocked bool) TurnStopReason {
	if loopBlocked {
		return TurnStopReasonLoopBlocked
	}
	return TurnStopReasonMaxIterations
}

func isCriticalToolError(err error) bool {
	for current := err; current != nil; current = errors.Unwrap(current) {
		if strings.HasPrefix(current.Error(), "CRITICAL:") {
			return true
		}
	}
	return false
}

// generateWithAgenticLoop is a helper that providers call from Generate().
// It builds messages from the Turn, makes the initial call, and runs the
// agentic loop if needed. This eliminates the duplicated loop code from
// each provider.
func generateWithAgenticLoop(ctx context.Context, provider ChatProvider, turn Turn, providerSystemPrompt, logPrefix string) (ProviderResult, error) {
	messages := buildLLMMessagesFromTurn(turn, providerSystemPrompt)
	opts := chatOptionsFromTurn(turn)

	// If no executor or no tools, just do a single call.
	if turn.Executor == nil || len(turn.Tools) == 0 {
		resp, err := provider.Chat(ctx, messages, turn.Tools, opts)
		if err != nil {
			return ProviderResult{}, err
		}
		return llmResponseToProviderResult(resp), nil
	}

	// Run the agentic loop.
	maxIter := turn.MaxAgenticIterations // 0 = use model-tier default in RunAgenticLoop
	resp, err := RunAgenticLoop(ctx, AgenticLoopConfig{
		Provider:            provider,
		InitialMessages:     messages,
		Tools:               turn.Tools,
		Executor:            turn.Executor,
		Options:             opts,
		MaxIterations:       maxIter,
		ForceText:           true,
		LogPrefix:           logPrefix,
		SessionID:           turn.SessionID,
		TurnID:              turn.TurnID,
		ToolEventSink:       turn.ToolEventSink,
		ContextWindowTokens: turn.ContextWindowTokens,
		Trace:               turn.Trace,
		LastAssistantTime:   turn.LastAssistantTime,
		DeferredTools:       turn.DeferredTools,
	})
	if err != nil {
		return ProviderResult{}, err
	}

	result := llmResponseToProviderResult(resp)
	if result.Text == "" && len(result.ToolCalls) == 0 {
		return ProviderResult{}, fmt.Errorf("%s returned no content", logPrefix)
	}

	// Log cache hit ratio when cache tokens are reported by the provider.
	if u := result.Usage; u.CacheReadTokens > 0 || u.CacheCreationTokens > 0 {
		total := u.InputTokens + u.CacheReadTokens + u.CacheCreationTokens
		var pct float64
		if total > 0 {
			pct = float64(u.CacheReadTokens) / float64(total) * 100
		}
		log.Printf("%s: cache tokens read=%d created=%d (%.0f%% hit rate of %d total input)",
			logPrefix, u.CacheReadTokens, u.CacheCreationTokens, pct, total)
	}

	return result, nil
}
