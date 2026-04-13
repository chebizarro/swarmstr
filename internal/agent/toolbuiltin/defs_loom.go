package toolbuiltin

import "metiq/internal/agent"

// ── Loom compute marketplace ──────────────────────────────────────────────────

// LoomWorkerListDef is the ToolDefinition for loom_worker_list.
var LoomWorkerListDef = agent.ToolDefinition{
	Name:        "loom_worker_list",
	Description: "Discover available Loom compute workers on Nostr (kind 10100). Returns worker pubkeys, capabilities, pricing, and supported commands. Use this before loom_job_submit to find a suitable worker.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"limit": {
				Type:        "number",
				Description: "Maximum number of workers to return. Default: 20.",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to query. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
	},
}

// LoomJobSubmitDef is the ToolDefinition for loom_job_submit.
var LoomJobSubmitDef = agent.ToolDefinition{
	Name:        "loom_job_submit",
	Description: "Submit a compute job to a Loom worker via Nostr (kind 5100). The worker executes the command and uploads stdout/stderr to Blossom. Payment is a Cashu token locked to the worker's pubkey — obtain one via nuts_mint_quote first. Use loom_job_status or loom_job_result to track progress.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"worker_pubkey": {
				Type:        "string",
				Description: "Hex pubkey of the Loom worker to send the job to.",
			},
			"command": {
				Type:        "string",
				Description: "The executable command to run (e.g. 'python3', 'bash', 'ffmpeg').",
			},
			"payment": {
				Type:        "string",
				Description: "Cashu token locked to the worker's pubkey. Determines execution timeout: amount / price_per_second.",
			},
			"args": {
				Type:        "string",
				Description: "JSON array string of command arguments, e.g. '[\"script.py\", \"--verbose\"]'. Also accepts space-separated string.",
			},
			"stdin": {
				Type:        "string",
				Description: "Optional data to pass to the command via stdin.",
			},
			"env": {
				Type:        "string",
				Description: "JSON object string of environment variables, e.g. '{\"FOO\":\"bar\"}'.",
			},
			"secrets": {
				Type:        "string",
				Description: "JSON object string of pre-encrypted secret values to pass to the worker.",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to publish the job to. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"worker_pubkey", "command", "payment"},
	},
}

// LoomJobStatusDef is the ToolDefinition for loom_job_status.
var LoomJobStatusDef = agent.ToolDefinition{
	Name:        "loom_job_status",
	Description: "Get the latest status of a submitted Loom compute job (kind 30100). Returns stage (queued/running/done/error), progress notes, and timing.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"job_id": {
				Type:        "string",
				Description: "Job ID returned by loom_job_submit.",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to query. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"job_id"},
	},
}

// LoomJobResultDef is the ToolDefinition for loom_job_result.
var LoomJobResultDef = agent.ToolDefinition{
	Name:        "loom_job_result",
	Description: "Wait for and retrieve the final result of a Loom compute job (kind 5101). Returns exit code, stdout_url and stderr_url pointing to Blossom-hosted output files. Use blossom_download to fetch the actual content.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"job_id": {
				Type:        "string",
				Description: "Job ID returned by loom_job_submit.",
			},
			"timeout_seconds": {
				Type:        "number",
				Description: "How long to wait for the result before giving up. Default: 300 (5 minutes).",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to query. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"job_id"},
	},
}

// LoomJobCancelDef is the ToolDefinition for loom_job_cancel.
var LoomJobCancelDef = agent.ToolDefinition{
	Name:        "loom_job_cancel",
	Description: "Cancel a running or queued Loom compute job (kind 5102). The worker will return a partial Cashu refund for unused execution time.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"job_id": {
				Type:        "string",
				Description: "Job ID to cancel.",
			},
			"worker_pubkey": {
				Type:        "string",
				Description: "Hex pubkey of the worker handling the job.",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"job_id", "worker_pubkey"},
	},
}
