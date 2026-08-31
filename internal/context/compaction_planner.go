package context

import (
	stdctx "context"
	"errors"
	"fmt"
	"math"
	"strings"
)

// SummaryGenerator creates a summary from a sanitized, model-visible projection.
// Implementations must not retain the supplied messages after returning.
type SummaryGenerator interface {
	Summarize(ctx stdctx.Context, messages []Message, previousSummary string) (string, error)
}

// SummaryGeneratorFunc adapts a function to SummaryGenerator.
type SummaryGeneratorFunc func(stdctx.Context, []Message, string) (string, error)

func (f SummaryGeneratorFunc) Summarize(ctx stdctx.Context, messages []Message, previousSummary string) (string, error) {
	return f(ctx, messages, previousSummary)
}

// ErrCompactionOverflow classifies a summary request that did not fit the
// provider context window. Only this error triggers bounded smaller-chunk retry.
var ErrCompactionOverflow = errors.New("compaction summary context overflow")

// CompactionPlanningResult records the bounded inputs and decisions used for a
// generated summary.
type CompactionPlanningResult struct {
	Summary             string
	ContextWindowTokens int
	AvailableTokens     int
	MaxChunkTokens      int
	ChunkRatio          float64
	Chunks              int
	Stages              int
	OverflowRetries     int
	OversizedMessages   int
}

func resolveSessionMemoryCompactConfig(config SessionMemoryCompactConfig) SessionMemoryCompactConfig {
	defaults := DefaultSessionMemoryCompactConfig
	if config.ContextWindowTokens <= 0 {
		config.ContextWindowTokens = defaults.ContextWindowTokens
	}
	if config.OutputReserveTokens <= 0 {
		config.OutputReserveTokens = defaults.OutputReserveTokens
	}
	if config.SummarizationOverheadTokens <= 0 {
		config.SummarizationOverheadTokens = defaults.SummarizationOverheadTokens
	}
	if config.SafetyMargin < 1 {
		config.SafetyMargin = defaults.SafetyMargin
	}
	if config.RecentContextRatio <= 0 || config.RecentContextRatio >= 1 {
		config.RecentContextRatio = defaults.RecentContextRatio
	}
	if config.BaseChunkRatio <= 0 || config.BaseChunkRatio >= 1 {
		config.BaseChunkRatio = defaults.BaseChunkRatio
	}
	if config.MinChunkRatio <= 0 || config.MinChunkRatio > config.BaseChunkRatio {
		config.MinChunkRatio = defaults.MinChunkRatio
	}
	if config.MaxSummaryStages <= 0 {
		config.MaxSummaryStages = defaults.MaxSummaryStages
	}
	if config.MaxOverflowRetries <= 0 {
		config.MaxOverflowRetries = defaults.MaxOverflowRetries
	}
	if config.MinTextBlockMessages <= 0 {
		config.MinTextBlockMessages = defaults.MinTextBlockMessages
	}

	available := config.ContextWindowTokens - config.OutputReserveTokens - config.SummarizationOverheadTokens
	if available < 1 {
		available = 1
	}
	if config.MaxTokens <= 0 {
		config.MaxTokens = maxInt(1, int(float64(available)*config.RecentContextRatio))
	}
	if config.MinTokens <= 0 {
		config.MinTokens = maxInt(1, config.MaxTokens/2)
	}
	if config.MinTokens > config.MaxTokens {
		config.MinTokens = config.MaxTokens
	}
	return config
}

// SanitizeCompactionMessages strips system/runtime-only fields and tool
// arguments from the model-facing summary projection. Tool result content is
// retained because it is part of the visible conversation.
func SanitizeCompactionMessages(messages []Message) []Message {
	safe := make([]Message, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case "user", "assistant", "tool":
		default:
			continue
		}
		projected := Message{
			Role:       msg.Role,
			Content:    msg.Content,
			ToolCallID: msg.ToolCallID,
			ID:         msg.ID,
		}
		if len(msg.ToolCalls) > 0 {
			projected.ToolCalls = make([]ToolCallRef, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				projected.ToolCalls = append(projected.ToolCalls, ToolCallRef{ID: call.ID, Name: call.Name})
			}
		}
		safe = append(safe, projected)
	}
	return safe
}

// ComputeAdaptiveChunkRatio reduces chunk size when average messages consume a
// large share of the model window.
func ComputeAdaptiveChunkRatio(messages []Message, contextWindow int, config SessionMemoryCompactConfig) float64 {
	config = resolveSessionMemoryCompactConfig(config)
	if contextWindow <= 0 {
		contextWindow = config.ContextWindowTokens
	}
	if len(messages) == 0 {
		return config.BaseChunkRatio
	}
	avgRatio := (float64(estimateMessagesTokens(SanitizeCompactionMessages(messages))) / float64(len(messages))) * config.SafetyMargin / float64(contextWindow)
	if avgRatio <= 0.1 {
		return config.BaseChunkRatio
	}
	reduction := math.Min(avgRatio*2, config.BaseChunkRatio-config.MinChunkRatio)
	return math.Max(config.MinChunkRatio, config.BaseChunkRatio-reduction)
}

type compactionMessageGroup struct {
	messages []Message
	tokens   int
}

func compactionMessageGroups(messages []Message) []compactionMessageGroup {
	if len(messages) == 0 {
		return nil
	}
	groups := make([]compactionMessageGroup, 0, len(messages))
	current := compactionMessageGroup{}
	pending := map[string]struct{}{}
	flush := func() {
		if len(current.messages) > 0 {
			groups = append(groups, current)
			current = compactionMessageGroup{}
		}
	}
	for _, msg := range messages {
		current.messages = append(current.messages, msg)
		current.tokens += estimateMessageTokens(msg)
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			pending = map[string]struct{}{}
			for _, call := range msg.ToolCalls {
				if call.ID != "" {
					pending[call.ID] = struct{}{}
				}
			}
		} else if msg.Role == "tool" && msg.ToolCallID != "" {
			delete(pending, msg.ToolCallID)
		}
		if len(pending) == 0 {
			flush()
		}
	}
	flush()
	return groups
}

// BuildSummaryChunks splits sanitized messages without splitting an active
// assistant tool-call/result batch.
func BuildSummaryChunks(messages []Message, maxChunkTokens int, config SessionMemoryCompactConfig) [][]Message {
	config = resolveSessionMemoryCompactConfig(config)
	if maxChunkTokens < 1 {
		maxChunkTokens = 1
	}
	effectiveMax := maxInt(1, int(float64(maxChunkTokens)/config.SafetyMargin))
	groups := compactionMessageGroups(SanitizeCompactionMessages(messages))
	chunks := make([][]Message, 0)
	var current []Message
	currentTokens := 0
	for _, group := range groups {
		if len(current) > 0 && currentTokens+group.tokens > effectiveMax {
			chunks = append(chunks, current)
			current = nil
			currentTokens = 0
		}
		current = append(current, group.messages...)
		currentTokens += group.tokens
	}
	if len(current) > 0 {
		chunks = append(chunks, current)
	}
	return chunks
}

// BuildOversizedFallback replaces indivisible groups over half a context window
// with deterministic notes so summary generation can still make progress.
func BuildOversizedFallback(messages []Message, contextWindow int, config SessionMemoryCompactConfig) ([]Message, []string) {
	config = resolveSessionMemoryCompactConfig(config)
	if contextWindow <= 0 {
		contextWindow = config.ContextWindowTokens
	}
	threshold := float64(contextWindow) * 0.5
	groups := compactionMessageGroups(SanitizeCompactionMessages(messages))
	small := make([]Message, 0, len(messages))
	notes := make([]string, 0)
	for _, group := range groups {
		if float64(group.tokens)*config.SafetyMargin <= threshold {
			small = append(small, group.messages...)
			continue
		}
		role := group.messages[0].Role
		id := group.messages[0].ID
		if id == "" {
			id = "unknown"
		}
		notes = append(notes, fmt.Sprintf("[Large %s message group id=%s (~%dK tokens) omitted from summary]", role, id, int(math.Round(float64(group.tokens)/1000))))
	}
	return small, notes
}

// GenerateCompactionSummary runs sanitized adaptive chunks, then recursively
// summarizes the partial summaries. Context-overflow retries are bounded and
// reduce only the failing chunk budget.
func GenerateCompactionSummary(ctx stdctx.Context, messages []Message, previousSummary string, config SessionMemoryCompactConfig) (CompactionPlanningResult, error) {
	config = resolveSessionMemoryCompactConfig(config)
	if config.SummaryGenerator == nil {
		return CompactionPlanningResult{}, errors.New("compaction summary generator is required")
	}
	available := config.ContextWindowTokens - config.OutputReserveTokens - config.SummarizationOverheadTokens
	available = maxInt(1, available)
	ratio := ComputeAdaptiveChunkRatio(messages, config.ContextWindowTokens, config)
	maxChunk := maxInt(1, int(float64(available)*ratio))
	small, notes := BuildOversizedFallback(messages, config.ContextWindowTokens, config)
	if len(notes) > 0 {
		small = append(small, Message{Role: "user", Content: strings.Join(notes, "\n"), ID: "compaction-oversized-notes"})
	}

	result := CompactionPlanningResult{
		ContextWindowTokens: config.ContextWindowTokens,
		AvailableTokens:     available,
		MaxChunkTokens:      maxChunk,
		ChunkRatio:          ratio,
		OversizedMessages:   len(notes),
	}
	current := small
	prior := strings.TrimSpace(previousSummary)
	for stage := 1; stage <= config.MaxSummaryStages; stage++ {
		chunks := BuildSummaryChunks(current, maxChunk, config)
		if len(chunks) == 0 {
			result.Summary = prior
			result.Stages = stage - 1
			return result, nil
		}
		result.Chunks += len(chunks)
		partials := make([]string, 0, len(chunks))
		for i, chunk := range chunks {
			seed := ""
			if i == 0 {
				seed = prior
			}
			summary, retries, err := summarizeWithOverflowRecovery(ctx, config.SummaryGenerator, chunk, seed, maxChunk, config)
			result.OverflowRetries += retries
			if err != nil {
				return result, err
			}
			partials = append(partials, strings.TrimSpace(summary))
		}
		result.Stages = stage
		if len(partials) == 1 {
			result.Summary = boundCompactionSummary(mergeSessionSummaries(prior, partials[0]), available)
			return result, nil
		}
		current = make([]Message, 0, len(partials))
		for i, partial := range partials {
			current = append(current, Message{Role: "user", ID: fmt.Sprintf("compaction-stage-%d-part-%d", stage, i), Content: partial})
		}
		prior = ""
	}
	return result, fmt.Errorf("compaction summary exceeded %d stages: %w", config.MaxSummaryStages, ErrCompactionOverflow)
}

func summarizeWithOverflowRecovery(ctx stdctx.Context, generator SummaryGenerator, chunk []Message, previous string, maxChunk int, config SessionMemoryCompactConfig) (string, int, error) {
	return summarizeChunkRecursive(ctx, generator, chunk, previous, maxChunk, config, 0)
}

func summarizeChunkRecursive(ctx stdctx.Context, generator SummaryGenerator, chunk []Message, previous string, maxChunk int, config SessionMemoryCompactConfig, depth int) (string, int, error) {
	summary, err := generator.Summarize(ctx, chunk, previous)
	if err == nil {
		return summary, depth, nil
	}
	if !errors.Is(err, ErrCompactionOverflow) || depth >= config.MaxOverflowRetries {
		return "", depth, err
	}
	chunks := BuildSummaryChunks(chunk, maxInt(1, maxChunk>>(depth+1)), config)
	if len(chunks) <= 1 {
		return "", depth + 1, err
	}
	partials := make([]string, 0, len(chunks))
	totalRetries := 1
	for i, smaller := range chunks {
		seed := ""
		if i == 0 {
			seed = previous
		}
		partial, retries, subErr := summarizeChunkRecursive(ctx, generator, smaller, seed, maxChunk, config, depth+1)
		totalRetries += retries
		if subErr != nil {
			return "", totalRetries, subErr
		}
		partials = append(partials, strings.TrimSpace(partial))
	}
	return strings.Join(partials, "\n\n"), totalRetries, nil
}

func boundCompactionSummary(summary string, maxTokens int) string {
	summary = strings.TrimSpace(summary)
	if maxTokens < 1 || estimateTextTokens(summary) <= maxTokens {
		return summary
	}
	const marker = "\n[Earlier summary truncated to compaction budget]"
	maxChars := maxTokens*4 - len(marker)
	if maxChars < 1 {
		return strings.TrimSpace(TruncateUTF8(marker, maxTokens*4))
	}
	return strings.TrimSpace(TruncateUTF8(summary, maxChars)) + marker
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
