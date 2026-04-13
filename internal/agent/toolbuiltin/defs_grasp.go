package toolbuiltin

import "metiq/internal/agent"

// ── GRASP NIP-34 git tools ─────────────────────────────────────────────────────

// GRASPRepoAnnounceDef is the ToolDefinition for grasp_repo_announce.
var GRASPRepoAnnounceDef = agent.ToolDefinition{
	Name:        "grasp_repo_announce",
	Description: "Publish a NIP-34 git repository announcement event to Nostr relays. Makes the repository discoverable by GRASP-compatible clients and other agents.",
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
	Description: "Fetch NIP-34 repository announcement events from relays. Optionally filter by owner pubkey.",
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
	Description: "Publish a NIP-34 issue event on a GRASP repository. Issues are addressable Nostr events linked to a repository via its address tag.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"repo_addr": {
				Type:        "string",
				Description: "Repository address in NIP-34 address format '<repo-announcement-kind>:<owner-pubkey>:<repo-id>'.",
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
	Description: "Fetch open NIP-34 issue events for a GRASP repository.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"repo_addr": {
				Type:        "string",
				Description: "Repository address in NIP-34 address format '<repo-announcement-kind>:<owner-pubkey>:<repo-id>'.",
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
	Description: "Publish a NIP-34 patch event to a GRASP repository. The content should be the output of 'git format-patch'.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"repo_addr": {
				Type:        "string",
				Description: "Repository address in NIP-34 address format '<repo-announcement-kind>:<owner-pubkey>:<repo-id>'.",
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
	Description: "Publish a NIP-34 pull request event to a GRASP repository, proposing a branch or set of commits for merge.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"repo_addr": {
				Type:        "string",
				Description: "Repository address in NIP-34 address format '<repo-announcement-kind>:<owner-pubkey>:<repo-id>'.",
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
