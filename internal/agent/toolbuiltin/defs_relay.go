package toolbuiltin

import "metiq/internal/agent"

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
	Description: "Search Nostr events by keyword using NIP-50. Queries the supplied relays, or the agent's configured relays when no override is provided. Optionally filter by event kinds.",
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
				Description: "Relay URLs to query. Defaults to the agent's configured relays when omitted.",
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
