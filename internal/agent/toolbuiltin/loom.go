// Package toolbuiltin – Loom decentralized compute marketplace tools.
//
// Registers: loom_worker_list, loom_job_submit, loom_job_status,
// loom_job_result, loom_job_cancel
//
// Loom enables the agent to discover workers and submit compute jobs via Nostr
// with Cashu ecash payments. Workers execute arbitrary commands and upload
// stdout/stderr to Blossom.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"

	"swarmstr/internal/agent"
	"swarmstr/internal/loom"
)

// LoomToolOpts configures Loom tools.
type LoomToolOpts struct {
	Keyer      nostr.Keyer
	Relays     []string
}

// RegisterLoomTools registers Loom compute marketplace tools.
func RegisterLoomTools(tools *agent.ToolRegistry, opts LoomToolOpts) {
	pool := nostr.NewPool(nostr.PoolOptions{PenaltyBox: true})

	resolveKeyer := func(ctx context.Context) (nostr.Keyer, error) {
		if opts.Keyer == nil {
			return nil, fmt.Errorf("no signing keyer configured")
		}
		return opts.Keyer, nil
	}

	// loom_worker_list: discover available compute workers (kind 10100).
	tools.Register("loom_worker_list", func(ctx context.Context, args map[string]any) (string, error) {
		limit := 20
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			relays = opts.Relays
		}

		workers, err := loom.ListWorkers(ctx, pool, relays, limit)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{
			"workers": workers,
			"count":   len(workers),
			"note":    "Workers accept Cashu tokens for payment. Timeout = payment_amount / price_per_second.",
		})
		return string(out), nil
	})

	// loom_job_submit: submit a compute job to a worker (kind 5100).
	// The caller must provide a Cashu token locked to the worker's pubkey.
	// The timeout is determined by: payment_amount / price_per_second.
	tools.Register("loom_job_submit", func(ctx context.Context, args map[string]any) (string, error) {
		workerPubKey, _ := args["worker_pubkey"].(string)
		command, _ := args["command"].(string)
		payment, _ := args["payment"].(string) // Cashu token locked to worker
		stdin, _ := args["stdin"].(string)
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			relays = opts.Relays
		}

		if workerPubKey == "" {
			return "", fmt.Errorf("loom_job_submit: worker_pubkey is required")
		}
		if command == "" {
			return "", fmt.Errorf("loom_job_submit: command is required")
		}
		if payment == "" {
			return "", fmt.Errorf("loom_job_submit: payment (Cashu token locked to worker pubkey) is required")
		}

		req := loom.JobRequest{
			WorkerPubKey: workerPubKey,
			Command:      command,
			Stdin:        stdin,
			Payment:      payment,
		}

		// Parse args array from JSON string.
		if argsStr, ok := args["args"].(string); ok && argsStr != "" {
			var cmdArgs []string
			if err := json.Unmarshal([]byte(argsStr), &cmdArgs); err != nil {
				// Treat as space-separated if not JSON.
				req.Args = strings.Fields(argsStr)
			} else {
				req.Args = cmdArgs
			}
		}

		// Parse env vars from JSON object string.
		if envStr, ok := args["env"].(string); ok && envStr != "" {
			var envMap map[string]string
			if err := json.Unmarshal([]byte(envStr), &envMap); err == nil {
				req.Env = envMap
			}
		}

		// Parse pre-encrypted secrets from JSON object string.
		if secretsStr, ok := args["secrets"].(string); ok && secretsStr != "" {
			var secretsMap map[string]string
			if err := json.Unmarshal([]byte(secretsStr), &secretsMap); err == nil {
				req.Secrets = secretsMap
			}
		}

		ks, err := resolveKeyer(ctx)
		if err != nil {
			return "", fmt.Errorf("loom_job_submit: %w", err)
		}

		jobID, err := loom.SubmitJob(ctx, pool, ks, relays, req)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{
			"ok":     true,
			"job_id": jobID,
			"note":   "Use loom_job_status to track progress; loom_job_result to get stdout/stderr URLs.",
		})
		return string(out), nil
	})

	// loom_job_status: get the latest status for a submitted job (kind 30100).
	tools.Register("loom_job_status", func(ctx context.Context, args map[string]any) (string, error) {
		jobID, _ := args["job_id"].(string)
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			relays = opts.Relays
		}

		if jobID == "" {
			return "", fmt.Errorf("loom_job_status: job_id is required")
		}

		status, err := loom.GetJobStatus(ctx, pool, relays, jobID)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(status)
		return string(out), nil
	})

	// loom_job_result: wait for and retrieve the final job result (kind 5101).
	// stdout_url and stderr_url point to Blossom-hosted output files.
	tools.Register("loom_job_result", func(ctx context.Context, args map[string]any) (string, error) {
		jobID, _ := args["job_id"].(string)
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			relays = opts.Relays
		}
		waitSecs := 300 // default 5 minutes
		if v, ok := args["timeout_seconds"].(float64); ok && v > 0 {
			waitSecs = int(v)
		}

		if jobID == "" {
			return "", fmt.Errorf("loom_job_result: job_id is required")
		}

		result, err := loom.WaitForResult(ctx, pool, relays, jobID, time.Duration(waitSecs)*time.Second)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(result)
		return string(out), nil
	})

	// loom_job_cancel: cancel a running or queued job (kind 5102).
	tools.Register("loom_job_cancel", func(ctx context.Context, args map[string]any) (string, error) {
		jobID, _ := args["job_id"].(string)
		workerPubKey, _ := args["worker_pubkey"].(string)
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			relays = opts.Relays
		}

		if jobID == "" {
			return "", fmt.Errorf("loom_job_cancel: job_id is required")
		}
		if workerPubKey == "" {
			return "", fmt.Errorf("loom_job_cancel: worker_pubkey is required")
		}

		ks, err := resolveKeyer(ctx)
		if err != nil {
			return "", fmt.Errorf("loom_job_cancel: %w", err)
		}

		evID, err := loom.CancelJob(ctx, pool, ks, relays, jobID, workerPubKey)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{
			"ok":              true,
			"cancellation_id": evID,
			"note":            "Worker will return partial change for unused payment upon cancellation.",
		})
		return string(out), nil
	})
}
