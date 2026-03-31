package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
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
	// MaxIterations caps the number of tool→LLM round-trips. Default: 30.
	MaxIterations int
	// ForceText if true, makes a final LLM call with Tools=nil when the loop
	// exhausts MaxIterations without producing text. This forces the model to
	// summarise its findings instead of returning an error.
	ForceText bool
	// LogPrefix is prepended to log messages (e.g. "anthropic", "openai").
	LogPrefix string
}

// ToolExecResult holds the outcome of a single tool execution.
type ToolExecResult struct {
	ToolCallID  string
	Content     string
	LoopBlocked bool // true if loop detection returned CRITICAL
}

// RunAgenticLoop executes the agentic tool loop:
//  1. Call the LLM
//  2. If the response requests tool use, execute tools in parallel
//  3. Append tool results and call the LLM again
//  4. Repeat until the model produces text or MaxIterations is reached
//  5. Optionally force a text response when the loop is exhausted
func RunAgenticLoop(ctx context.Context, cfg AgenticLoopConfig) (*LLMResponse, error) {
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 30
	}
	if cfg.LogPrefix == "" {
		cfg.LogPrefix = "agentic"
	}

	messages := cfg.InitialMessages

	// historyDelta accumulates the ordered assistant/tool messages produced
	// during this turn so callers can persist them for future context.
	var historyDelta []ConversationMessage

	// Initial LLM call.
	resp, err := cfg.Provider.Chat(ctx, messages, cfg.Tools, cfg.Options)
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

		// Execute all tool calls in parallel.
		results := executeToolsParallel(ctx, cfg.Executor, calls)

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

		// Next LLM call.
		resp, err = cfg.Provider.Chat(ctx, messages, cfg.Tools, cfg.Options)
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
				},
			}
		}

		totalUsage.InputTokens += resp.Usage.InputTokens
		totalUsage.OutputTokens += resp.Usage.OutputTokens
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

// executeToolsParallel runs all tool calls concurrently and returns results
// in the same order as the input calls.
func executeToolsParallel(ctx context.Context, executor ToolExecutor, calls []ToolCall) []ToolExecResult {
	results := make([]ToolExecResult, len(calls))
	var wg sync.WaitGroup

	for i, call := range calls {
		results[i].ToolCallID = call.ID

		wg.Add(1)
		go func(idx int, c ToolCall) {
			defer wg.Done()

			result, execErr := executor.Execute(ctx, c)
			if execErr != nil {
				errMsg := execErr.Error()
				results[idx].Content = "error: " + errMsg
				log.Printf("tool %s error: %v", c.Name, execErr)
				// If the loop detector blocked the call (CRITICAL level),
				// signal the loop to stop immediately.
				if isCriticalToolError(execErr) {
					results[idx].LoopBlocked = true
				}
			} else {
				results[idx].Content = result
			}
		}(i, call)
	}

	wg.Wait()
	return results
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

		results := executeToolsParallel(ctx, cfg.Executor, pendingCalls)
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
	summaryResp, err := cfg.Provider.Chat(ctx, messages, nil, opts)
	if err != nil {
		log.Printf("%s: force-summary error: %v", cfg.LogPrefix, err)
		return nil, nil
	}
	if summaryResp == nil || summaryResp.Content == "" {
		return nil, nil
	}

	summaryResp.Usage = ProviderUsage{
		InputTokens:  usage.InputTokens + summaryResp.Usage.InputTokens,
		OutputTokens: usage.OutputTokens + summaryResp.Usage.OutputTokens,
	}
	return summaryResp, summaryDelta
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
	resp, err := RunAgenticLoop(ctx, AgenticLoopConfig{
		Provider:        provider,
		InitialMessages: messages,
		Tools:           turn.Tools,
		Executor:        turn.Executor,
		Options:         opts,
		MaxIterations:   30,
		ForceText:       true,
		LogPrefix:       logPrefix,
	})
	if err != nil {
		return ProviderResult{}, err
	}

	result := llmResponseToProviderResult(resp)
	if result.Text == "" && len(result.ToolCalls) == 0 {
		return ProviderResult{}, fmt.Errorf("%s returned no content", logPrefix)
	}
	return result, nil
}
