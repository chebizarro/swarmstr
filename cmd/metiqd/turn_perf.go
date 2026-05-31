package main

import (
	"context"
	"time"

	"metiq/internal/agent"
	ctxengine "metiq/internal/context"
)

func ingestContextWithTurnSpan(ctx context.Context, engine ctxengine.Engine, sessionID string, msg ctxengine.Message, phase string) (ctxengine.IngestResult, error) {
	startedAt := time.Now()
	result, err := engine.Ingest(ctx, sessionID, msg)
	fields := map[string]any{
		"phase":    phase,
		"role":     msg.Role,
		"has_id":   msg.ID != "",
		"ingested": result.Ingested,
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	agent.EmitTurnSpan(ctx, "context_engine_ingest", time.Since(startedAt), fields)
	return result, err
}

func assembleContextWithTurnSpan(ctx context.Context, engine ctxengine.Engine, sessionID string, maxTokens int, phase string) (ctxengine.AssembleResult, error) {
	startedAt := time.Now()
	assembled, err := engine.Assemble(ctx, sessionID, maxTokens)
	fields := map[string]any{
		"phase":            phase,
		"max_tokens":       maxTokens,
		"messages_count":   len(assembled.Messages),
		"estimated_tokens": assembled.EstimatedTokens,
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	agent.EmitTurnSpan(ctx, "context_engine_assemble", time.Since(startedAt), fields)
	return assembled, err
}

func emitPostTurnPersistenceSpan(ctx context.Context, startedAt time.Time, outcome string) {
	if startedAt.IsZero() {
		return
	}
	agent.EmitTurnSpan(ctx, "post_turn_persistence", time.Since(startedAt), map[string]any{"outcome": outcome})
}
