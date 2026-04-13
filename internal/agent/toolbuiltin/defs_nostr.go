package toolbuiltin

import "metiq/internal/agent"

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
				Description: "WebSocket URL of the relay, e.g. \"wss://<relay-url>\".",
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
				Description: "WebSocket URL of the relay, e.g. \"wss://<relay-url>\".",
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
