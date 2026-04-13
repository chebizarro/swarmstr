package toolbuiltin

import "metiq/internal/agent"

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
