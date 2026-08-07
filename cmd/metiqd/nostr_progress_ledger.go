package main

import (
	"context"
	"log"
	"time"

	"metiq/internal/gateway/channels"
	"metiq/internal/store/state"
	taskspkg "metiq/internal/tasks"
)

// nostr_progress_ledger.go wires the R5 Progress Ledger moderator loop
// (reference: openclaw-nostr ocn-d3u): when this instance is a room's
// designated moderator (progressLedger + progressLedgerModerator knobs), a
// background ticker drives channels.ProgressLedgerScheduler, which reviews the
// recorded room-event window + current fleet-task state deterministically and
// posts at most one compact summary per interval — silence by default.

// nostrProgressLedgerTick is the driver cadence; the scheduler itself enforces
// the room's review interval (floor 60s), so a sub-interval tick only bounds
// wake-up latency.
const nostrProgressLedgerTick = 30 * time.Second

// nostrProgressLedgerPostTimeout bounds one summary publish.
const nostrProgressLedgerPostTimeout = 30 * time.Second

// nostrProgressLedgerOpenStatuses are the task states reviewed as "current":
// non-terminal kind-30900 heads (duplicate claims on finished work are noise).
var nostrProgressLedgerOpenStatuses = []state.TaskStatus{
	state.TaskStatusPending,
	state.TaskStatusPlanned,
	state.TaskStatusReady,
	state.TaskStatusInProgress,
	state.TaskStatusBlocked,
	state.TaskStatusAwaitingApproval,
	state.TaskStatusVerifying,
}

// nostrProgressLedgerLoopOptions parameterize one room's moderator loop.
type nostrProgressLedgerLoopOptions struct {
	roomKey    string
	selfPubkey string
	scheduler  *channels.ProgressLedgerScheduler
	recorder   *channels.ProgressLedgerRecorder
	guard      *channels.BotLoopProtection
	taskLedger *taskspkg.Ledger
	// policy resolves the room's CURRENT policy each tick (config changes
	// apply live); ok=false stops the loop's work until re-enabled.
	policy func() (channels.NostrRoomPolicy, bool)
	// post publishes the compact summary to the room.
	post func(ctx context.Context, text string) error
}

// startNostrProgressLedgerLoop runs the per-room moderator review until ctx
// ends. Caller has already verified this instance is the room's moderator at
// startup; the policy is still re-resolved every tick so demotion/disable
// takes effect without a restart.
func startNostrProgressLedgerLoop(ctx context.Context, opts nostrProgressLedgerLoopOptions) {
	if opts.scheduler == nil || opts.policy == nil || opts.post == nil || opts.roomKey == "" {
		return
	}
	go func() {
		ticker := time.NewTicker(nostrProgressLedgerTick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			policy, ok := opts.policy()
			if !ok {
				continue
			}
			result := opts.scheduler.Run(channels.ProgressLedgerRunParams{
				RoomKey:    opts.roomKey,
				Policy:     policy,
				SelfPubkey: opts.selfPubkey,
				Collect:    func() channels.ProgressLedgerInput { return collectNostrProgressLedgerInput(ctx, opts) },
				Post: func(summary string) error {
					postCtx, cancel := context.WithTimeout(ctx, nostrProgressLedgerPostTimeout)
					defer cancel()
					return opts.post(postCtx, summary)
				},
			})
			switch {
			case result.PostErr != nil:
				log.Printf("progress ledger post failed room=%s err=%v", opts.roomKey, result.PostErr)
			case result.Posted:
				log.Printf("progress ledger posted room=%s summary=%q", opts.roomKey, result.Summary)
			case result.Ran && result.Findings.Actionable() && result.Throttled:
				log.Printf("progress ledger throttled room=%s (actionable findings withheld)", opts.roomKey)
			}
		}
	}()
}

// collectNostrProgressLedgerInput gathers the typed review facts: the recorded
// room-event window, the current open fleet-task state from the ledger the
// FleetTaskBridge synchronizes, and the pair-guard snapshot. Open commitments
// join here once a daemon-wide commitments.Store is wired (map it with
// channels.ProgressLedgerCommitmentsFromStore).
func collectNostrProgressLedgerInput(ctx context.Context, opts nostrProgressLedgerLoopOptions) channels.ProgressLedgerInput {
	in := channels.ProgressLedgerInput{}
	if opts.recorder != nil {
		in.Events = opts.recorder.Events(opts.roomKey)
	}
	if opts.guard != nil && opts.guard.Guard() != nil {
		in.PairGuard = opts.guard.Guard().Snapshot()
	}
	if opts.taskLedger != nil {
		entries, err := opts.taskLedger.ListTasks(ctx, taskspkg.ListTasksOptions{Status: nostrProgressLedgerOpenStatuses})
		if err != nil {
			log.Printf("progress ledger task listing failed room=%s err=%v", opts.roomKey, err)
		}
		for _, entry := range entries {
			if entry == nil {
				continue
			}
			task := channels.ProgressLedgerTask{
				TaskID: entry.Task.TaskID,
				Status: string(entry.Task.Status),
				Title:  entry.Task.Title,
			}
			if entry.Task.AssignedAgent != "" {
				task.Claimants = []string{entry.Task.AssignedAgent}
			}
			in.Tasks = append(in.Tasks, task)
		}
	}
	return in
}
