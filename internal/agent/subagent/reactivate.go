package subagent

import (
	"fmt"
	"strings"
	"time"
)

// ReactivateInput holds the parameters for reactivating a completed subagent
// session. It maps to openclaw's reactivateCompletedSubagentSession.
type ReactivateInput struct {
	// SessionKey is the child session key whose latest ended run should be
	// replaced with a fresh run.
	SessionKey string

	// RunID is the run ID for the new replacement run.
	RunID string

	// RunTimeoutSeconds overrides the timeout for the new run. When zero,
	// the original run's timeout is preserved.
	RunTimeoutSeconds int
}

// ReactivateResult describes the outcome of a reactivation attempt.
type ReactivateResult struct {
	// Reactivated is true when a fresh run was successfully created.
	Reactivated bool

	// PreviousRunID is the run ID that was replaced.
	PreviousRunID string

	// NewRunID is the run ID of the replacement run.
	NewRunID string
}

// ReactivateCompletedSession looks up the latest subagent run for the given
// child session key. If it has ended, it atomically replaces the ended run
// with a fresh one, preserving the session context (child session key,
// requester info, task, cleanup policy, etc.).
//
// This is the Go equivalent of openclaw's reactivateCompletedSubagentSession +
// replaceSubagentRunAfterSteer.
func (r *Registry) ReactivateCompletedSession(input ReactivateInput) (ReactivateResult, error) {
	sessionKey := strings.TrimSpace(input.SessionKey)
	nextRunID := strings.TrimSpace(input.RunID)
	if sessionKey == "" {
		return ReactivateResult{}, fmt.Errorf("session_key is required")
	}
	if nextRunID == "" {
		return ReactivateResult{}, fmt.Errorf("run_id is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Find the latest run for this child session key.
	var latest *SubagentRunRecord
	var latestKey string
	for k, rec := range r.runs {
		if rec.ChildSessionKey != sessionKey {
			continue
		}
		if latest == nil || rec.CreatedAt > latest.CreatedAt {
			latest = rec
			latestKey = k
		}
	}

	if latest == nil {
		return ReactivateResult{}, nil
	}

	// Only reactivate ended runs.
	if latest.EndedAt == 0 {
		return ReactivateResult{}, nil
	}

	previousRunID := latest.RunID

	// Remove the old run.
	if latestKey != nextRunID {
		delete(r.runs, latestKey)
	}

	// Build the replacement run, preserving context from the source.
	now := time.Now().UnixMilli()
	timeout := input.RunTimeoutSeconds
	if timeout == 0 {
		timeout = latest.RunTimeoutSeconds
	}

	next := &SubagentRunRecord{
		RunID:               nextRunID,
		ChildSessionKey:     latest.ChildSessionKey,
		RequesterSessionKey: latest.RequesterSessionKey,
		RequesterDisplayKey: latest.RequesterDisplayKey,
		Task:                latest.Task,
		Cleanup:             latest.Cleanup,
		Label:               latest.Label,
		RunTimeoutSeconds:   timeout,
		CreatedAt:           latest.CreatedAt,
		StartedAt:           now,
		EndedAt:             0,
		Outcome:             nil,
		SuppressAnnounce:    "",
	}

	r.runs[nextRunID] = next
	return ReactivateResult{
		Reactivated:   true,
		PreviousRunID: previousRunID,
		NewRunID:      nextRunID,
	}, nil
}
