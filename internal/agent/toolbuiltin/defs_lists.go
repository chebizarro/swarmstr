package toolbuiltin

import "metiq/internal/agent"

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
