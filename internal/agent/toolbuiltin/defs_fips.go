package toolbuiltin

import "metiq/internal/agent"

// FIPSStatusDef is the ToolDefinition for fips_status.
var FIPSStatusDef = agent.ToolDefinition{
	Name:        "fips_status",
	Description: "Show FIPS mesh connectivity status including transport health, control channel, routing preference, and fleet peer reachability. Returns structured JSON with connection counts, listener addresses, and per-peer status.",
	Parameters: agent.ToolParameters{
		Type:       "object",
		Properties: map[string]agent.ToolParamProp{},
	},
}
