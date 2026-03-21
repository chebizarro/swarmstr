// defs_registry.go centralises ToolDefinition declarations for tools that are
// registered inline in main.go (as closures) and therefore cannot be annotated
// in their own source files.  Each Def var is exported so main.go can call
// tools.SetDefinition(name, Def) after registering the tool.
//
// Also contains defs for the remaining nostr / cron / session tools so that
// every tool the agent can call is discoverable via native function-calling.
package toolbuiltin

import "metiq/internal/agent"

// ─── Memory ──────────────────────────────────────────────────────────────────

// MemorySearchDef is the ToolDefinition for memory.search (global search).
var MemorySearchDef = agent.ToolDefinition{
	Name:        "memory_search",
	Description: "Search the persistent memory store for records matching a query. Returns ranked results across all sessions. Use to recall stored facts, past decisions, user preferences, or any information you've previously saved.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"query": {
				Type:        "string",
				Description: "Full-text search query, e.g. \"project deadline\" or \"user's favourite editor\".",
			},
			"limit": {
				Type:        "integer",
				Description: "Maximum results to return (1–50, default 5).",
			},
		},
		Required: []string{"query"},
	},
}

// ─── ACP / multi-agent ───────────────────────────────────────────────────────

// ACPDelegateDef is the ToolDefinition for acp.delegate.
var ACPDelegateDef = agent.ToolDefinition{
	Name:        "acp_delegate",
	Description: "Delegate a sub-task to a peer agent and wait for the result. The peer executes the instructions in their own session and returns a text response. Use for parallelising work or routing specialised tasks to domain-specific agents.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"peer_pubkey": {
				Type:        "string",
				Description: "Hex Nostr pubkey of the peer agent to delegate to.",
			},
			"instructions": {
				Type:        "string",
				Description: "Detailed instructions for the peer agent describing what to do and any required context.",
			},
			"timeout_ms": {
				Type:        "integer",
				Description: "Milliseconds to wait for the peer's reply (default 60 000, i.e. 60 s).",
			},
		},
		Required: []string{"peer_pubkey", "instructions"},
	},
}

// ─── Canvas ──────────────────────────────────────────────────────────────────

// CanvasUpdateDef is the ToolDefinition for canvas_update.
// The tool is registered inline in main.go; this definition is set via
// tools.SetDefinition so the model sees the correct schema.
var CanvasUpdateDef = agent.ToolDefinition{
	Name:        "canvas_update",
	Description: "Update a named canvas surface with HTML, Markdown, or JSON content. The canvas is broadcast over WebSocket to any connected browser clients (e.g. the webchat UI). Use this to render rich output such as tables, dashboards, or formatted reports that would be unwieldy as plain DM text.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"canvas_id": {
				Type:        "string",
				Description: "Identifier for this canvas surface (e.g. \"main\", \"report\", \"game\"). Multiple canvases can coexist.",
			},
			"content_type": {
				Type:        "string",
				Description: "One of: \"html\" (full HTML page), \"markdown\" (GitHub-flavoured), \"json\" (pretty-printed data).",
				Enum:        []string{"html", "markdown", "json"},
			},
			"data": {
				Type:        "string",
				Description: "The content string — full HTML, Markdown text, or JSON string depending on content_type.",
			},
		},
		Required: []string{"canvas_id", "content_type", "data"},
	},
}

// ─── Sessions ────────────────────────────────────────────────────────────────

// SessionsListDef is the ToolDefinition for sessions_list.
var SessionsListDef = agent.ToolDefinition{
	Name:        "sessions_list",
	Description: "List the active agent sessions. Returns session IDs, creation times, and any associated metadata. Useful for discovering spawned sub-agent sessions.",
	Parameters:  agent.ToolParameters{Type: "object"},
}

// SessionSpawnDef is the ToolDefinition for session_spawn.
var SessionSpawnDef = agent.ToolDefinition{
	Name:        "session_spawn",
	Description: "Spawn a new child agent session with custom instructions or a different model, creating an isolated context for a sub-task. Returns the new session ID.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"instructions": {
				Type:        "string",
				Description: "System instructions for the new session (sets its persona / task scope).",
			},
			"model": {
				Type:        "string",
				Description: "LLM model for this session (e.g. \"gpt-4o\", \"claude-3-5-sonnet-20241022\"). Defaults to parent's model.",
			},
		},
	},
}

// SessionSendDef is the ToolDefinition for session_send.
var SessionSendDef = agent.ToolDefinition{
	Name:        "session_send",
	Description: "Send a message to a specific agent session and wait for its reply. Use after session_spawn to communicate with a child agent.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"session_id": {
				Type:        "string",
				Description: "The session ID to send the message to.",
			},
			"message": {
				Type:        "string",
				Description: "The message text to send.",
			},
		},
		Required: []string{"session_id", "message"},
	},
}

// ─── Cron / scheduling ───────────────────────────────────────────────────────

// CronAddDef is the ToolDefinition for cron_add.
var CronAddDef = agent.ToolDefinition{
	Name:        "cron_add",
	Description: "Schedule a recurring task using a cron expression. At each trigger, the agent receives the given instructions as if they were an incoming message — starting a new proactive turn. Use to set up periodic reminders, monitoring, data polling, or any autonomous recurring action.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"schedule": {
				Type:        "string",
				Description: "Cron expression (5-field UTC), e.g. \"0 9 * * 1\" = every Monday 09:00 UTC, \"*/30 * * * *\" = every 30 minutes.",
			},
			"instructions": {
				Type:        "string",
				Description: "What the agent should do when the cron fires — written as if a user sent this message, e.g. \"Check for new Nostr mentions and summarise them\".",
			},
			"agent_id": {
				Type:        "string",
				Description: "Agent ID to route the scheduled turn to (defaults to the current agent).",
			},
			"label": {
				Type:        "string",
				Description: "Short human-readable label for this job, shown in cron_list.",
			},
		},
		Required: []string{"schedule", "instructions"},
	},
}

// CronListDef is the ToolDefinition for cron_list.
var CronListDef = agent.ToolDefinition{
	Name:        "cron_list",
	Description: "List all scheduled cron jobs, including their IDs, schedules, and methods. Use to review or audit active recurring tasks.",
	Parameters:  agent.ToolParameters{Type: "object"},
}

// CronRemoveDef is the ToolDefinition for cron_remove.
var CronRemoveDef = agent.ToolDefinition{
	Name:        "cron_remove",
	Description: "Remove a scheduled cron job by ID, stopping future invocations. Use to cancel recurring tasks that are no longer needed.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"id": {
				Type:        "string",
				Description: "The cron job ID to remove (from cron_list).",
			},
		},
		Required: []string{"id"},
	},
}

// ─── Nostr profile / NIP-05 ──────────────────────────────────────────────────

// NostrProfileDef is the ToolDefinition for nostr_profile.
var NostrProfileDef = agent.ToolDefinition{
	Name:        "nostr_profile",
	Description: "Fetch the Nostr profile (kind 0) for a pubkey or npub. Returns display name, about text, NIP-05 identifier, picture URL, and other metadata.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"pubkey": {
				Type:        "string",
				Description: "Hex pubkey or npub of the user whose profile to fetch.",
			},
		},
		Required: []string{"pubkey"},
	},
}

// NostrResolveNIP05Def is the ToolDefinition for nostr_resolve_nip05.
var NostrResolveNIP05Def = agent.ToolDefinition{
	Name:        "nostr_resolve_nip05",
	Description: "Resolve a NIP-05 identifier (user@domain) to its Nostr hex pubkey and recommended relays via the domain's /.well-known/nostr.json endpoint.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"nip05": {
				Type:        "string",
				Description: "NIP-05 identifier, e.g. \"alice@example.com\".",
			},
		},
		Required: []string{"nip05"},
	},
}

// ─── Nostr relay tools ───────────────────────────────────────────────────────

// NostrRelayListDef is the ToolDefinition for relay_list.
var NostrRelayListDef = agent.ToolDefinition{
	Name:        "relay_list",
	Description: "List the configured read/write Nostr relays. Returns relay URLs with read/write flags.",
	Parameters:  agent.ToolParameters{Type: "object"},
}

// NostrRelayPingDef is the ToolDefinition for relay_ping.
var NostrRelayPingDef = agent.ToolDefinition{
	Name:        "relay_ping",
	Description: "Check whether a Nostr relay is reachable and accepting connections. Returns latency in milliseconds or an error.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"url": {
				Type:        "string",
				Description: "WebSocket URL of the relay, e.g. \"wss://nos.lol\".",
			},
		},
		Required: []string{"url"},
	},
}

// NostrRelayInfoDef is the ToolDefinition for relay_info.
var NostrRelayInfoDef = agent.ToolDefinition{
	Name:        "relay_info",
	Description: "Fetch NIP-11 relay information document for a relay URL. Returns supported NIPs, name, description, contact, and policies.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"url": {
				Type:        "string",
				Description: "WebSocket URL of the relay, e.g. \"wss://nos.lol\".",
			},
		},
		Required: []string{"url"},
	},
}

// ─── Nostr WoT / social graph ────────────────────────────────────────────────

// NostrFollowsDef is the ToolDefinition for nostr_follows.
var NostrFollowsDef = agent.ToolDefinition{
	Name:        "nostr_follows",
	Description: "Fetch the follow list (kind 3) for a Nostr pubkey. Returns an array of followed pubkeys.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"pubkey": {
				Type:        "string",
				Description: "Hex pubkey or npub whose follow list to fetch.",
			},
		},
		Required: []string{"pubkey"},
	},
}

// NostrFollowersDef is the ToolDefinition for nostr_followers.
var NostrFollowersDef = agent.ToolDefinition{
	Name:        "nostr_followers",
	Description: "Find Nostr users who follow a given pubkey by scanning relays for kind-3 events referencing it. Returns a list of follower pubkeys.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"pubkey": {
				Type:        "string",
				Description: "Hex pubkey or npub to look up followers for.",
			},
		},
		Required: []string{"pubkey"},
	},
}

// NostrWotDistanceDef is the ToolDefinition for nostr_wot_distance.
var NostrWotDistanceDef = agent.ToolDefinition{
	Name:        "nostr_wot_distance",
	Description: "Compute the Web-of-Trust (WoT) social distance between the agent and a target pubkey. Returns the hop count through the follow graph (0 = self, 1 = direct follow, 2 = follow-of-follow, etc.).",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"pubkey": {
				Type:        "string",
				Description: "Hex pubkey or npub of the target user.",
			},
		},
		Required: []string{"pubkey"},
	},
}

// ─── Nostr outbox / relay hints ──────────────────────────────────────────────

// NostrRelayHintsDef is the ToolDefinition for nostr_relay_hints.
var NostrRelayHintsDef = agent.ToolDefinition{
	Name:        "nostr_relay_hints",
	Description: "Look up the outbox (NIP-65) relay list for a pubkey — the relays where their events are published. Use to find relay hints when building NIP-01 tags or routing queries.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"pubkey": {
				Type:        "string",
				Description: "Hex pubkey or npub to look up relay hints for.",
			},
		},
		Required: []string{"pubkey"},
	},
}

// NostrRelayListSetDef is the ToolDefinition for nostr_relay_list_set.
var NostrRelayListSetDef = agent.ToolDefinition{
	Name:        "nostr_relay_list_set",
	Description: "Publish your own NIP-65 relay list metadata event (kind:10002).",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"read_relays": {
				Type:        "array",
				Description: "Relay URLs marked as read.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"write_relays": {
				Type:        "array",
				Description: "Relay URLs marked as write.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"both_relays": {
				Type:        "array",
				Description: "Relay URLs marked as both read and write (no marker).",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"relays": {
				Type:        "array",
				Description: "Optional publish relay overrides.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
	},
}

// NostrDeleteDef is the ToolDefinition for nostr_delete.
var NostrDeleteDef = agent.ToolDefinition{
	Name:        "nostr_delete",
	Description: "Publish a NIP-09 deletion request event (kind 5) for one or more event IDs.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"ids": {
				Type:        "array",
				Description: "Event IDs to request deletion for.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"reason": {
				Type:        "string",
				Description: "Optional deletion reason included as event content.",
			},
			"relays": {
				Type:        "array",
				Description: "Optional relay publish overrides.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"ids"},
	},
}

// NostrEventDeleteDef is the ToolDefinition alias for nostr_event_delete.
var NostrEventDeleteDef = agent.ToolDefinition{
	Name:        "nostr_event_delete",
	Description: "Alias of nostr_delete. Publish a NIP-09 deletion request (kind 5) for event IDs.",
	Parameters:  NostrDeleteDef.Parameters,
}

// NostrArticlePublishDef is the ToolDefinition for nostr_article_publish.
var NostrArticlePublishDef = agent.ToolDefinition{
	Name:        "nostr_article_publish",
	Description: "Publish a NIP-23 long-form article (kind 30023). Supports d-tag/title/summary/image/tags and markdown content.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"title": {
				Type:        "string",
				Description: "Article title.",
			},
			"content": {
				Type:        "string",
				Description: "Markdown article body.",
			},
			"summary": {
				Type:        "string",
				Description: "Optional summary; autogenerated from content if omitted.",
			},
			"image": {
				Type:        "string",
				Description: "Optional hero image URL; first markdown image is used if omitted.",
			},
			"d_tag": {
				Type:        "string",
				Description: "Optional stable d-tag; slugified from title if omitted.",
			},
			"tags": {
				Type:        "array",
				Description: "Optional topic tags; inferred from #hashtags if omitted.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"published_at": {
				Type:        "integer",
				Description: "Optional unix timestamp; defaults to now.",
			},
			"relays": {
				Type:        "array",
				Description: "Optional relay publish overrides.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"title", "content"},
	},
}

// NostrReportDef is the ToolDefinition for nostr_report.
var NostrReportDef = agent.ToolDefinition{
	Name:        "nostr_report",
	Description: "Publish a NIP-56 report event (kind 1984) for abusive/spam/impersonation content.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"report_type": {
				Type:        "string",
				Description: "Report category, e.g. spam, impersonation, nudity, illegal, malware, or other.",
			},
			"target_event_ids": {
				Type:        "array",
				Description: "Event IDs being reported.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"target_pubkeys": {
				Type:        "array",
				Description: "Pubkeys being reported.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"reason": {
				Type:        "string",
				Description: "Optional freeform report reason/details.",
			},
			"relays": {
				Type:        "array",
				Description: "Optional relay publish overrides.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"report_type"},
	},
}

// ─── Nostr zaps ──────────────────────────────────────────────────────────────

// NostrZapSendDef is the ToolDefinition for nostr_zap_send.
var NostrZapSendDef = agent.ToolDefinition{
	Name:        "nostr_zap_send",
	Description: "Send a Nostr zap (NIP-57 lightning tip) to a pubkey or event. Fetches the LNURL-pay endpoint, builds a kind-9734 zap request, and returns the bolt11 invoice. Requires the recipient to have a lightning address or LNURL in their profile.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"target_pubkey": {
				Type:        "string",
				Description: "Hex pubkey or npub of the zap recipient.",
			},
			"amount_sats": {
				Type:        "integer",
				Description: "Amount to zap in satoshis.",
			},
			"comment": {
				Type:        "string",
				Description: "Optional zap comment message.",
			},
			"event_id": {
				Type:        "string",
				Description: "Optional hex event ID to attach the zap to a specific note.",
			},
		},
		Required: []string{"target_pubkey", "amount_sats"},
	},
}

// NostrZapListDef is the ToolDefinition for nostr_zap_list.
var NostrZapListDef = agent.ToolDefinition{
	Name:        "nostr_zap_list",
	Description: "List recent zap receipts (kind 9735) for a pubkey or event ID. Returns sender, amount, comment, and timestamp for each zap.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"pubkey": {
				Type:        "string",
				Description: "Hex pubkey to list received zaps for.",
			},
			"event_id": {
				Type:        "string",
				Description: "Hex event ID to list zaps for a specific note.",
			},
			"limit": {
				Type:        "integer",
				Description: "Max number of zap receipts to return (default 20).",
			},
		},
	},
}

// ─── Nostr watch ─────────────────────────────────────────────────────────────

// NostrWatchDef is the ToolDefinition for nostr_watch.
var NostrWatchDef = agent.ToolDefinition{
	Name:        "nostr_watch",
	Description: "Create a persistent Nostr subscription that delivers matching events back to this session as DM-style messages. Use to monitor hashtags, pubkeys, or any filter in real-time. Returns a watch ID.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"name": {
				Type:        "string",
				Description: "Short label for this watch (used in delivery messages), e.g. \"btc-news\".",
			},
			"filter": {
				Type:        "object",
				Description: "NIP-01 filter object. Supports keys like ids, authors, kinds, since, until, limit, and #<tag> arrays.",
			},
			"session_id": {
				Type:        "string",
				Description: "Session ID to deliver events to. Defaults to current session; only needed for cross-session delivery.",
			},
			"relays": {
				Type:        "array",
				Description: "Optional relay URL overrides.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"ttl_seconds": {
				Type:        "number",
				Description: "Optional watch lifetime in seconds. Default: 3600.",
			},
			"max_events": {
				Type:        "number",
				Description: "Optional max events before auto-stop. Default: 100 (0 = unlimited).",
			},
		},
		Required: []string{"name", "filter"},
	},
}

// NostrUnwatchDef is the ToolDefinition for nostr_unwatch.
var NostrUnwatchDef = agent.ToolDefinition{
	Name:        "nostr_unwatch",
	Description: "Cancel an active Nostr watch subscription by its name.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"name": {
				Type:        "string",
				Description: "Name of the watch to cancel (the label given when creating it with nostr_watch).",
			},
		},
		Required: []string{"name"},
	},
}

// NostrWatchListDef is the ToolDefinition for nostr_watch_list.
var NostrWatchListDef = agent.ToolDefinition{
	Name:        "nostr_watch_list",
	Description: "List all active Nostr watch subscriptions, including their IDs, names, and filter parameters.",
	Parameters:  agent.ToolParameters{Type: "object"},
}

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

// ── Blossom blob storage ───────────────────────────────────────────────────────

// BlossomUploadDef is the ToolDefinition for blossom_upload.
var BlossomUploadDef = agent.ToolDefinition{
	Name:        "blossom_upload",
	Description: "Upload a file or text content to a Blossom blob storage server. Provide either a local file path or raw content string. Returns the SHA256 hash and public URL for the uploaded blob.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"path": {
				Type:        "string",
				Description: "Local filesystem path of the file to upload. Either path or content is required.",
			},
			"content": {
				Type:        "string",
				Description: "Raw text content to upload. Either path or content is required.",
			},
			"mime_type": {
				Type:        "string",
				Description: "MIME type of the content (e.g. 'text/plain', 'image/png'). Auto-detected from extension if omitted.",
			},
			"server_url": {
				Type:        "string",
				Description: "Blossom server URL. Defaults to the configured default server.",
			},
		},
	},
}

// BlossomDownloadDef is the ToolDefinition for blossom_download.
var BlossomDownloadDef = agent.ToolDefinition{
	Name:        "blossom_download",
	Description: "Download a blob from a Blossom server by its SHA256 hash. Small text blobs are returned inline; large or binary blobs require an output_path to save to disk.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"sha256": {
				Type:        "string",
				Description: "SHA256 hex hash of the blob to download.",
			},
			"output_path": {
				Type:        "string",
				Description: "Local filesystem path to save the downloaded blob. Required for binary or large files.",
			},
			"server_url": {
				Type:        "string",
				Description: "Blossom server URL. Defaults to the configured default server.",
			},
		},
		Required: []string{"sha256"},
	},
}

// BlossomListDef is the ToolDefinition for blossom_list.
var BlossomListDef = agent.ToolDefinition{
	Name:        "blossom_list",
	Description: "List all blobs uploaded by a pubkey on a Blossom server. Defaults to your own pubkey if none is specified.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"pubkey": {
				Type:        "string",
				Description: "Hex pubkey to list blobs for. Defaults to the agent's own pubkey.",
			},
			"server_url": {
				Type:        "string",
				Description: "Blossom server URL. Defaults to the configured default server.",
			},
		},
	},
}

// BlossomDeleteDef is the ToolDefinition for blossom_delete.
var BlossomDeleteDef = agent.ToolDefinition{
	Name:        "blossom_delete",
	Description: "Delete a blob from a Blossom server by its SHA256 hash. Only works for blobs you own.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"sha256": {
				Type:        "string",
				Description: "SHA256 hex hash of the blob to delete.",
			},
			"server_url": {
				Type:        "string",
				Description: "Blossom server URL. Defaults to the configured default server.",
			},
		},
		Required: []string{"sha256"},
	},
}

// BlossomMirrorDef is the ToolDefinition for blossom_mirror.
var BlossomMirrorDef = agent.ToolDefinition{
	Name:        "blossom_mirror",
	Description: "Mirror a blob from a source URL to a target Blossom server. Useful for replicating Loom job output or other blobs to your preferred server.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"source_url": {
				Type:        "string",
				Description: "Full URL of the blob to mirror (e.g. https://other-server.com/<sha256>).",
			},
			"sha256": {
				Type:        "string",
				Description: "Expected SHA256 hash of the blob for integrity verification.",
			},
			"server_url": {
				Type:        "string",
				Description: "Target Blossom server URL to mirror the blob to. Defaults to configured default.",
			},
		},
		Required: []string{"source_url"},
	},
}

// ── GRASP NIP-34 git tools ─────────────────────────────────────────────────────

// GRASPRepoAnnounceDef is the ToolDefinition for grasp_repo_announce.
var GRASPRepoAnnounceDef = agent.ToolDefinition{
	Name:        "grasp_repo_announce",
	Description: "Publish a NIP-34 git repository announcement (kind 30617) to Nostr relays. Makes the repository discoverable by GRASP-compatible clients and other agents.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"id": {
				Type:        "string",
				Description: "Repository identifier (d-tag), kebab-case, e.g. 'my-project'. Must be unique per owner.",
			},
			"name": {
				Type:        "string",
				Description: "Human-readable repository name.",
			},
			"description": {
				Type:        "string",
				Description: "Short description of the repository.",
			},
			"clone_urls": {
				Type:        "string",
				Description: "Comma-separated clone URLs (git://, https://, or ssh://).",
			},
			"web_urls": {
				Type:        "string",
				Description: "Comma-separated web UI URLs for the repository.",
			},
			"repo_relays": {
				Type:        "string",
				Description: "Comma-separated relay URLs that host this repository's events.",
			},
			"earliest_commit": {
				Type:        "string",
				Description: "SHA of the earliest commit in the repository history.",
			},
			"labels": {
				Type:        "string",
				Description: "Comma-separated labels/topics for the repository.",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to publish to. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"id"},
	},
}

// GRASPRepoListDef is the ToolDefinition for grasp_repo_list.
var GRASPRepoListDef = agent.ToolDefinition{
	Name:        "grasp_repo_list",
	Description: "Fetch NIP-34 repository announcements (kind 30617) from relays. Optionally filter by owner pubkey.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"pubkey": {
				Type:        "string",
				Description: "Filter by owner pubkey (hex). Returns repos from all owners if omitted.",
			},
			"limit": {
				Type:        "number",
				Description: "Maximum number of repositories to return. Default: 20.",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to query. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
	},
}

// GRASPIssueCreateDef is the ToolDefinition for grasp_issue_create.
var GRASPIssueCreateDef = agent.ToolDefinition{
	Name:        "grasp_issue_create",
	Description: "Publish a NIP-34 issue (kind 1621) on a GRASP repository. Issues are addressable Nostr events linked to a repository via its address tag.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"repo_addr": {
				Type:        "string",
				Description: "Repository address in format '30617:<owner-pubkey>:<repo-id>'.",
			},
			"subject": {
				Type:        "string",
				Description: "Issue title/subject line.",
			},
			"content": {
				Type:        "string",
				Description: "Issue body text (markdown supported).",
			},
			"labels": {
				Type:        "string",
				Description: "Comma-separated labels to apply to the issue.",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to publish to. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"repo_addr", "content"},
	},
}

// GRASPIssueListDef is the ToolDefinition for grasp_issue_list.
var GRASPIssueListDef = agent.ToolDefinition{
	Name:        "grasp_issue_list",
	Description: "Fetch open issues for a GRASP repository (kind 1621).",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"repo_addr": {
				Type:        "string",
				Description: "Repository address in format '30617:<owner-pubkey>:<repo-id>'.",
			},
			"limit": {
				Type:        "number",
				Description: "Maximum number of issues to return. Default: 20.",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to query. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"repo_addr"},
	},
}

// GRASPPatchSubmitDef is the ToolDefinition for grasp_patch_submit.
var GRASPPatchSubmitDef = agent.ToolDefinition{
	Name:        "grasp_patch_submit",
	Description: "Publish a NIP-34 patch (kind 1617) to a GRASP repository. The content should be the output of 'git format-patch'.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"repo_addr": {
				Type:        "string",
				Description: "Repository address in format '30617:<owner-pubkey>:<repo-id>'.",
			},
			"content": {
				Type:        "string",
				Description: "Patch content — output of 'git format-patch' or a unified diff.",
			},
			"commit_id": {
				Type:        "string",
				Description: "SHA of the commit this patch is based on.",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to publish to. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"repo_addr", "content"},
	},
}

// GRASPPRCreateDef is the ToolDefinition for grasp_pr_create.
var GRASPPRCreateDef = agent.ToolDefinition{
	Name:        "grasp_pr_create",
	Description: "Publish a NIP-34 pull request (kind 1618) to a GRASP repository, proposing a branch or set of commits for merge.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"repo_addr": {
				Type:        "string",
				Description: "Repository address in format '30617:<owner-pubkey>:<repo-id>'.",
			},
			"subject": {
				Type:        "string",
				Description: "PR title.",
			},
			"content": {
				Type:        "string",
				Description: "PR description (markdown supported).",
			},
			"commit_tip": {
				Type:        "string",
				Description: "SHA of the tip commit being proposed for merge.",
			},
			"branch_name": {
				Type:        "string",
				Description: "Name of the branch being proposed.",
			},
			"clone_urls": {
				Type:        "string",
				Description: "Comma-separated clone URLs where the branch can be fetched from.",
			},
			"labels": {
				Type:        "string",
				Description: "Comma-separated labels.",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to publish to. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"repo_addr"},
	},
}

// ── NIP-51 list management ─────────────────────────────────────────────────────

// ListGetDef is the ToolDefinition for list_get.
var ListGetDef = agent.ToolDefinition{
	Name:        "list_get",
	Description: "Fetch a NIP-51 list from relays by kind and optional d-tag. Defaults to your own mute list (kind 10000) if no kind is specified.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"kind": {
				Type:        "number",
				Description: "Nostr event kind for the list (e.g. 10000=mute, 10001=pinned, 30000=people, 30001=bookmarks). Default: 10000.",
			},
			"d_tag": {
				Type:        "string",
				Description: "d-tag identifier for parameterized replaceable lists (kind 30000+).",
			},
			"pubkey": {
				Type:        "string",
				Description: "Pubkey (hex) of the list owner. Defaults to your own pubkey.",
			},
		},
	},
}

// ListAddDef is the ToolDefinition for list_add.
var ListAddDef = agent.ToolDefinition{
	Name:        "list_add",
	Description: "Add an entry to a NIP-51 list. Fetches the current list, appends the new entry, and publishes the updated event.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"tag": {
				Type:        "string",
				Description: "Tag type for the entry, e.g. 'p' (pubkey), 'e' (event), 't' (hashtag), 'r' (relay).",
			},
			"value": {
				Type:        "string",
				Description: "Value for the tag entry (pubkey hex, event ID, hashtag string, relay URL, etc.).",
			},
			"kind": {
				Type:        "number",
				Description: "List kind. Default: 10000 (mute list).",
			},
			"d_tag": {
				Type:        "string",
				Description: "d-tag for parameterized lists (kind 30000+).",
			},
			"relay": {
				Type:        "string",
				Description: "Optional relay hint URL for 'p' or 'e' entries.",
			},
			"petname": {
				Type:        "string",
				Description: "Optional petname for 'p' entries.",
			},
		},
		Required: []string{"tag", "value"},
	},
}

// ListRemoveDef is the ToolDefinition for list_remove.
var ListRemoveDef = agent.ToolDefinition{
	Name:        "list_remove",
	Description: "Remove an entry from a NIP-51 list by tag type and value.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"tag": {
				Type:        "string",
				Description: "Tag type of the entry to remove (e.g. 'p', 'e', 't', 'r').",
			},
			"value": {
				Type:        "string",
				Description: "Value of the entry to remove.",
			},
			"kind": {
				Type:        "number",
				Description: "List kind. Default: 10000.",
			},
			"d_tag": {
				Type:        "string",
				Description: "d-tag for parameterized lists (kind 30000+).",
			},
		},
		Required: []string{"tag", "value"},
	},
}

// ListCreateDef is the ToolDefinition for list_create.
var ListCreateDef = agent.ToolDefinition{
	Name:        "list_create",
	Description: "Create a new named NIP-51 list (kind 30000 people list by default). Uses the name as the d-tag identifier.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"name": {
				Type:        "string",
				Description: "List name used as the d-tag (unique identifier). Required.",
			},
			"title": {
				Type:        "string",
				Description: "Human-readable display title for the list.",
			},
			"kind": {
				Type:        "number",
				Description: "List kind. Default: 30000 (people list).",
			},
		},
		Required: []string{"name"},
	},
}

// ListDeleteDef is the ToolDefinition for list_delete.
var ListDeleteDef = agent.ToolDefinition{
	Name:        "list_delete",
	Description: "Delete a NIP-51 list by publishing an empty replaceable event and a NIP-09 deletion event.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"kind": {
				Type:        "number",
				Description: "List kind to delete. Default: 30000.",
			},
			"d_tag": {
				Type:        "string",
				Description: "d-tag of the list to delete.",
			},
		},
	},
}

// ListCheckAllowlistDef is the ToolDefinition for list_check_allowlist.
var ListCheckAllowlistDef = agent.ToolDefinition{
	Name:        "list_check_allowlist",
	Description: "Check whether a pubkey passes the allow/block/mute filters for a given owner. Returns muted, blocked, allowed, and a combined pass boolean.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"pubkey": {
				Type:        "string",
				Description: "Hex pubkey to check.",
			},
			"owner_pubkey": {
				Type:        "string",
				Description: "Hex pubkey of the list owner whose filters to check against. Defaults to your own pubkey.",
			},
		},
		Required: []string{"pubkey"},
	},
}

// ── Cashu NUT ecash ───────────────────────────────────────────────────────────

// NutsMintInfoDef is the ToolDefinition for nuts_mint_info.
var NutsMintInfoDef = agent.ToolDefinition{
	Name:        "nuts_mint_info",
	Description: "Fetch public information about a Cashu mint: name, version, supported NUTs, contact info, and available units.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"mint_url": {
				Type:        "string",
				Description: "Cashu mint URL. Defaults to the configured default mint.",
			},
		},
	},
}

// NutsMintQuoteDef is the ToolDefinition for nuts_mint_quote.
var NutsMintQuoteDef = agent.ToolDefinition{
	Name:        "nuts_mint_quote",
	Description: "Request a Lightning invoice from a Cashu mint to mint new ecash tokens. Pay the invoice, then use nuts_mint_status to confirm payment before minting tokens.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"amount": {
				Type:        "number",
				Description: "Amount in satoshis to mint.",
			},
			"unit": {
				Type:        "string",
				Description: "Currency unit, e.g. 'sat'. Uses mint default if omitted.",
			},
			"mint_url": {
				Type:        "string",
				Description: "Cashu mint URL. Defaults to configured default.",
			},
		},
		Required: []string{"amount"},
	},
}

// NutsMintStatusDef is the ToolDefinition for nuts_mint_status.
var NutsMintStatusDef = agent.ToolDefinition{
	Name:        "nuts_mint_status",
	Description: "Check whether a Cashu mint quote (Lightning invoice) has been paid. Returns state: UNPAID, PAID, or ISSUED.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"quote_id": {
				Type:        "string",
				Description: "Quote ID returned by nuts_mint_quote.",
			},
			"mint_url": {
				Type:        "string",
				Description: "Cashu mint URL. Defaults to configured default.",
			},
		},
		Required: []string{"quote_id"},
	},
}

// NutsMeltQuoteDef is the ToolDefinition for nuts_melt_quote.
var NutsMeltQuoteDef = agent.ToolDefinition{
	Name:        "nuts_melt_quote",
	Description: "Get a quote for paying a Lightning invoice using Cashu ecash tokens. Returns the total amount (including fee reserve) required. Use nuts_melt to execute the payment.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"invoice": {
				Type:        "string",
				Description: "BOLT-11 Lightning invoice to pay.",
			},
			"unit": {
				Type:        "string",
				Description: "Token unit to spend. Uses mint default if omitted.",
			},
			"mint_url": {
				Type:        "string",
				Description: "Cashu mint URL. Defaults to configured default.",
			},
		},
		Required: []string{"invoice"},
	},
}

// NutsMeltDef is the ToolDefinition for nuts_melt.
var NutsMeltDef = agent.ToolDefinition{
	Name:        "nuts_melt",
	Description: "Pay a Lightning invoice by melting Cashu ecash proofs at a mint. The token is consumed and the invoice is paid. Returns the Lightning payment preimage on success.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"quote_id": {
				Type:        "string",
				Description: "Melt quote ID from nuts_melt_quote.",
			},
			"token": {
				Type:        "string",
				Description: "Cashu token string (cashuA...) containing the proofs to spend.",
			},
			"mint_url": {
				Type:        "string",
				Description: "Cashu mint URL. Defaults to configured default.",
			},
		},
		Required: []string{"quote_id", "token"},
	},
}

// NutsBalanceDef is the ToolDefinition for nuts_balance.
var NutsBalanceDef = agent.ToolDefinition{
	Name:        "nuts_balance",
	Description: "Calculate the total value of a Cashu token without spending it. Returns the balance in the token's unit.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"token": {
				Type:        "string",
				Description: "Cashu token string (cashuA...) to check the balance of.",
			},
		},
		Required: []string{"token"},
	},
}

// NutsDecodeDef is the ToolDefinition for nuts_decode.
var NutsDecodeDef = agent.ToolDefinition{
	Name:        "nuts_decode",
	Description: "Decode and inspect a Cashu token without spending it. Returns the full proof structure, balance, unit, and memo.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"token": {
				Type:        "string",
				Description: "Cashu token string (cashuA...) to decode.",
			},
		},
		Required: []string{"token"},
	},
}

// ── Relay memory ──────────────────────────────────────────────────────────────

// RelayRememberDef is the ToolDefinition for relay_remember.
var RelayRememberDef = agent.ToolDefinition{
	Name:        "relay_remember",
	Description: "Store a memory note on Nostr relays as a replaceable kind:30078 event (or kind:1 for free-form). Memories persist across sessions and can be retrieved with relay_recall.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"content": {
				Type:        "string",
				Description: "The memory content to store.",
			},
			"topic": {
				Type:        "string",
				Description: "Topic or category for the memory, used as the d-tag for retrieval. Auto-generated timestamp if omitted.",
			},
			"tags": {
				Type:        "array",
				Description: "Hashtag strings to attach to the memory for filtering (e.g. [\"task\", \"debug\"]).",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"kind": {
				Type:        "number",
				Description: "Nostr event kind. 30078 (default) for replaceable memories; 1 for ephemeral notes.",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to publish to. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"content"},
	},
}

// RelayRecallDef is the ToolDefinition for relay_recall.
var RelayRecallDef = agent.ToolDefinition{
	Name:        "relay_recall",
	Description: "Search relay history for stored memories. Filter by topic (exact d-tag match) or keyword query (NIP-50 search, with client-side fallback). Returns memory content with timestamps.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"query": {
				Type:        "string",
				Description: "Keyword or phrase to search for in memory content.",
			},
			"topic": {
				Type:        "string",
				Description: "Exact topic/d-tag to retrieve. Takes precedence over query.",
			},
			"limit": {
				Type:        "number",
				Description: "Maximum number of memories to return. Default: 10, max: 50.",
			},
			"since": {
				Type:        "number",
				Description: "Unix timestamp — only return memories created after this time.",
			},
			"until": {
				Type:        "number",
				Description: "Unix timestamp — only return memories created before this time.",
			},
			"timeout_seconds": {
				Type:        "number",
				Description: "Relay query timeout in seconds. Default: 10.",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to query. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
	},
}

// RelayForgetDef is the ToolDefinition for relay_forget.
var RelayForgetDef = agent.ToolDefinition{
	Name:        "relay_forget",
	Description: "Delete a stored memory from relays by publishing a NIP-09 deletion event for its event ID.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"event_id": {
				Type:        "string",
				Description: "Event ID (hex) of the memory to delete, as returned by relay_remember.",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to publish the deletion to. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"event_id"},
	},
}

// ── nostr_nips plain tools ────────────────────────────────────────────────────

// NostrReactDef is the ToolDefinition for nostr_react.
var NostrReactDef = agent.ToolDefinition{
	Name:        "nostr_react",
	Description: "Publish a NIP-25 reaction (kind 7) to a Nostr event. Use '+' for a like, '-' for a dislike, or any emoji string.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"event_id": {
				Type:        "string",
				Description: "Hex ID of the event to react to.",
			},
			"content": {
				Type:        "string",
				Description: "Reaction content: '+' (like), '-' (dislike), or an emoji. Default: '+'.",
			},
			"relay_hint": {
				Type:        "string",
				Description: "Optional relay URL hint for the target event.",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to publish to. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"event_id"},
	},
}

// NostrCommentDef is the ToolDefinition for nostr_comment.
var NostrCommentDef = agent.ToolDefinition{
	Name:        "nostr_comment",
	Description: "Publish a NIP-22 threaded comment (kind 1111) on any Nostr event. Supports nested replies by specifying a parent_id distinct from root_id.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"root_id": {
				Type:        "string",
				Description: "Hex ID of the root event being commented on.",
			},
			"root_kind": {
				Type:        "number",
				Description: "Kind of the root event (e.g. 1, 30023).",
			},
			"root_relay": {
				Type:        "string",
				Description: "Relay URL hint for the root event.",
			},
			"parent_id": {
				Type:        "string",
				Description: "Hex ID of the direct parent event if this is a nested reply. Omit for top-level comments.",
			},
			"content": {
				Type:        "string",
				Description: "Comment text (markdown supported).",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to publish to. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"root_id", "content"},
	},
}

// NostrArticleGetDef is the ToolDefinition for nostr_article_get.
var NostrArticleGetDef = agent.ToolDefinition{
	Name:        "nostr_article_get",
	Description: "Fetch a NIP-23 long-form article (kind 30023) by author pubkey and optional d-tag slug.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"author": {
				Type:        "string",
				Description: "Hex pubkey of the article author.",
			},
			"d_tag": {
				Type:        "string",
				Description: "Article slug (d-tag). If omitted, returns the author's most recent article.",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to query. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"author"},
	},
}

// NostrSearchDef is the ToolDefinition for nostr_search.
var NostrSearchDef = agent.ToolDefinition{
	Name:        "nostr_search",
	Description: "Search Nostr events by keyword using NIP-50. Queries search-capable relays (relay.primal.net, nostr.wine by default). Optionally filter by event kinds.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"query": {
				Type:        "string",
				Description: "Search query string.",
			},
			"limit": {
				Type:        "number",
				Description: "Maximum results to return. Default: 20, max: 100.",
			},
			"kinds": {
				Type:        "array",
				Description: "Event kinds to filter by (e.g. [1, 30023]). Returns all kinds if omitted.",
				Items:       &agent.ToolParamProp{Type: "number"},
			},
			"timeout_seconds": {
				Type:        "number",
				Description: "Query timeout in seconds. Default: 10.",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to query. Defaults to relay.primal.net and nostr.wine.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"query"},
	},
}

// NostrAppDataSetDef is the ToolDefinition for nostr_appdata_set.
var NostrAppDataSetDef = agent.ToolDefinition{
	Name:        "nostr_appdata_set",
	Description: "Store application-specific key-value data on Nostr relays as a NIP-78 kind:30078 event. Data is scoped by app_id and key, and replaces any previous value for that combination.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"app_id": {
				Type:        "string",
				Description: "Application namespace identifier (e.g. 'metiq', 'my-app').",
			},
			"key": {
				Type:        "string",
				Description: "Key within the app namespace.",
			},
			"value": {
				Type:        "string",
				Description: "Value to store (string; use JSON encoding for structured data).",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to publish to. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"app_id", "key"},
	},
}

// NostrAppDataGetDef is the ToolDefinition for nostr_appdata_get.
var NostrAppDataGetDef = agent.ToolDefinition{
	Name:        "nostr_appdata_get",
	Description: "Retrieve application-specific data stored with nostr_appdata_set. Returns the most recent value for the app_id/key combination.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"app_id": {
				Type:        "string",
				Description: "Application namespace identifier.",
			},
			"key": {
				Type:        "string",
				Description: "Key within the app namespace.",
			},
			"author": {
				Type:        "string",
				Description: "Hex pubkey of the data owner. Defaults to your own pubkey.",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to query. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"app_id", "key"},
	},
}

// NostrFileAnnounceDef is the ToolDefinition for nostr_file_announce.
var NostrFileAnnounceDef = agent.ToolDefinition{
	Name:        "nostr_file_announce",
	Description: "Publish a NIP-94 file metadata event (kind 1063) announcing a file's URL, type, hash, and description. Used to make files discoverable on Nostr.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"url": {
				Type:        "string",
				Description: "Public URL where the file can be downloaded.",
			},
			"mime_type": {
				Type:        "string",
				Description: "MIME type of the file (e.g. 'image/png', 'application/pdf').",
			},
			"sha256": {
				Type:        "string",
				Description: "SHA256 hex hash of the file for integrity verification.",
			},
			"description": {
				Type:        "string",
				Description: "Human-readable description of the file.",
			},
			"size": {
				Type:        "number",
				Description: "File size in bytes.",
			},
			"dim": {
				Type:        "string",
				Description: "Image/video dimensions in WxH format (e.g. '1920x1080'). Optional.",
			},
			"relays": {
				Type:        "array",
				Description: "Relay URLs to publish to. Defaults to configured relays.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
		Required: []string{"url", "mime_type"},
	},
}
