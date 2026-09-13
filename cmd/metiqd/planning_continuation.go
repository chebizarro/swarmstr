package main

import (
	"log"

	"metiq/internal/agent"
)

// runTurnWithPlanningContinuation runs baseTurn through run. If enabled and the
// result is a planning-only response (promises without tool actions), it retries
// once with BuildPlanningOnlyContinuation. On continuation error the original
// result is returned as a fallback (no error propagated).
func runTurnWithPlanningContinuation(
	enabled bool,
	baseTurn agent.Turn,
	run func(agent.Turn) (agent.TurnResult, error),
) (result agent.TurnResult, used bool, err error) {
	result, err = run(baseTurn)
	if err != nil {
		return result, false, err
	}
	if !enabled {
		return result, false, nil
	}
	state := agent.BuildCommitmentStateFromTraces(result.ToolTraces)
	if !agent.ShouldRetryPlanningOnly(result.Text, state, 0, 1) {
		return result, false, nil
	}
	cont := agent.BuildPlanningOnlyContinuation(baseTurn, result.Text)
	contResult, contErr := run(cont)
	if contErr != nil {
		log.Printf("planning-only continuation failed; falling back to original session=%s err=%v", baseTurn.SessionID, contErr)
		return result, false, nil
	}
	return contResult, true, nil
}