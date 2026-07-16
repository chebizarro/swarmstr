package toolbuiltin

import "metiq/internal/agent"

// L402FetchDef is the separate, payment-capable HTTP fetch permission surface.
var L402FetchDef = agent.ToolDefinition{
	Name:        "l402_fetch",
	Description: "Fetch text from an explicitly allowed L402/LSAT payment-metered HTTPS origin. May pay one Lightning invoice and retry once; ordinary web_fetch never pays.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"url": {
				Type:        "string",
				Description: "Exact HTTPS resource URL on an operator-approved L402 origin.",
			},
			"max_chars": {
				Type:        "integer",
				Description: "Maximum returned Unicode characters. Defaults to 50000.",
				Default:     50000,
			},
			"timeout_seconds": {
				Type:        "integer",
				Description: "Total HTTP request timeout in seconds, capped by the configured payment timeout.",
				Default:     30,
			},
		},
		Required: []string{"url"},
	},
	ParamAliases: map[string]string{
		"timeout":  "timeout_seconds",
		"limit":    "max_chars",
		"max_size": "max_chars",
	},
}
