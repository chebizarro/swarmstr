// defs_registry.go centralises ToolDefinition declarations for tools that are
// registered inline in main.go (as closures) and therefore cannot be annotated
// in their own source files.  Each Def var is exported so main.go can call
// tools.SetDefinition(name, Def) after registering the tool.
//
// Also contains defs for the remaining nostr / cron / session tools so that
// every tool the agent can call is discoverable via native function-calling.
package toolbuiltin

import "swarmstr/internal/agent"

// ─── Memory ──────────────────────────────────────────────────────────────────

// MemorySearchDef is the ToolDefinition for memory.search (global search).
var MemorySearchDef = agent.ToolDefinition{
	Name:        "memory.search",
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
	Name:        "acp.delegate",
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
				Description: "WebSocket URL of the relay, e.g. \"wss://relay.damus.io\".",
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
				Description: "WebSocket URL of the relay, e.g. \"wss://relay.damus.io\".",
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
			"kinds": {
				Type:        "array",
				Description: "Event kinds to watch.",
				Items:       &agent.ToolParamProp{Type: "integer"},
			},
			"authors": {
				Type:        "array",
				Description: "Filter by author pubkeys.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
			"tags": {
				Type:        "string",
				Description: "JSON-encoded tag filters, e.g. {\"t\":[\"bitcoin\"]}.",
			},
		},
		Required: []string{"name"},
	},
}

// NostrUnwatchDef is the ToolDefinition for nostr_unwatch.
var NostrUnwatchDef = agent.ToolDefinition{
	Name:        "nostr_unwatch",
	Description: "Cancel an active Nostr watch subscription by its ID.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"id": {
				Type:        "string",
				Description: "Watch ID returned by nostr_watch.",
			},
		},
		Required: []string{"id"},
	},
}

// NostrWatchListDef is the ToolDefinition for nostr_watch_list.
var NostrWatchListDef = agent.ToolDefinition{
	Name:        "nostr_watch_list",
	Description: "List all active Nostr watch subscriptions, including their IDs, names, and filter parameters.",
	Parameters:  agent.ToolParameters{Type: "object"},
}
