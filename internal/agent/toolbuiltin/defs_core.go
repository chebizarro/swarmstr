package toolbuiltin

import "metiq/internal/agent"

// ─── Memory ──────────────────────────────────────────────────────────────────

// MemorySearchDef is the ToolDefinition for memory.search (global search).
var MemorySearchDef = agent.ToolDefinition{
	Name:        "memory_search",
	Description: "Search the persistent memory store for records matching a narrow, concrete query. Returns ranked results across all sessions. USE THIS PROACTIVELY when the user asks about prior conversations, remembered preferences, past decisions, or says 'what do you remember'. Automatic recall is a PARTIAL SHORTLIST—this tool provides exhaustive search. Use to recall stored facts, project context, or external references you've previously saved.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"query": {
				Type:        "string",
				Description: "Narrow, concrete search query using keywords from the user's question. Examples: \"deployment preferences\", \"staging canary policy\", \"user editor choice\". Use specific terms, not generic words.",
			},
			"limit": {
				Type:        "integer",
				Description: "Maximum results to return (1–50). Default 5 for focused queries; increase to 10-20 when exploring broadly.",
			},
		},
		Required: []string{"query"},
	},
	ParamAliases: map[string]string{
		"q":           "query",
		"search":      "query",
		"text":        "query",
		"count":       "limit",
		"max_results": "limit",
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
				Description: "Target peer agent as a fleet name, npub, or hex Nostr pubkey. Capability-aware routing prefers registered peers with discovered ACP support when names collide.",
			},
			"instructions": {
				Type:        "string",
				Description: "Detailed instructions for the peer agent describing what to do and any required context.",
			},
			"timeout_ms": {
				Type:        "integer",
				Description: "Milliseconds to wait for the peer's reply (default 60 000, i.e. 60 s).",
			},
			"memory_scope": {
				Type:        "string",
				Description: "Optional worker memory scope. One of: user, project, local.",
				Enum:        []string{"user", "project", "local"},
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
			"memory_scope": {
				Type:        "string",
				Description: "Optional child memory scope. One of: user, project, local.",
				Enum:        []string{"user", "project", "local"},
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
