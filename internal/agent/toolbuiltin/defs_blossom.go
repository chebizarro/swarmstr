package toolbuiltin

import "metiq/internal/agent"

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
