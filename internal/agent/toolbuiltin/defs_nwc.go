package toolbuiltin

import "metiq/internal/agent"

// ── NIP-47 Nostr Wallet Connect (NWC) tools ──────────────────────────────────

// NWCGetBalanceDef is the ToolDefinition for nwc_get_balance.
var NWCGetBalanceDef = agent.ToolDefinition{
	Name:        "nwc_get_balance",
	Description: "Check the lightning wallet balance via Nostr Wallet Connect (NIP-47). Returns the balance in millisatoshis.",
	Parameters: agent.ToolParameters{
		Type:       "object",
		Properties: map[string]agent.ToolParamProp{},
	},
}

// NWCPayInvoiceDef is the ToolDefinition for nwc_pay_invoice.
var NWCPayInvoiceDef = agent.ToolDefinition{
	Name:        "nwc_pay_invoice",
	Description: "Pay a BOLT-11 lightning invoice via Nostr Wallet Connect (NIP-47). Returns the payment preimage on success.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"invoice": {
				Type:        "string",
				Description: "BOLT-11 encoded lightning invoice to pay.",
			},
			"amount_msats": {
				Type:        "number",
				Description: "Amount in millisatoshis. Required only for zero-amount invoices.",
			},
		},
		Required: []string{"invoice"},
	},
}

// NWCMakeInvoiceDef is the ToolDefinition for nwc_make_invoice.
var NWCMakeInvoiceDef = agent.ToolDefinition{
	Name:        "nwc_make_invoice",
	Description: "Create a lightning invoice to receive payment via Nostr Wallet Connect (NIP-47). Returns the BOLT-11 invoice string and payment hash.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"amount_msats": {
				Type:        "number",
				Description: "Amount to request in millisatoshis.",
			},
			"description": {
				Type:        "string",
				Description: "Human-readable invoice description.",
			},
			"expiry": {
				Type:        "number",
				Description: "Invoice expiry in seconds. Defaults to wallet's default if omitted.",
			},
		},
		Required: []string{"amount_msats"},
	},
}

// NWCLookupInvoiceDef is the ToolDefinition for nwc_lookup_invoice.
var NWCLookupInvoiceDef = agent.ToolDefinition{
	Name:        "nwc_lookup_invoice",
	Description: "Look up the status of a lightning invoice by payment hash or BOLT-11 string via Nostr Wallet Connect (NIP-47). Returns whether the invoice has been paid.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"payment_hash": {
				Type:        "string",
				Description: "Hex-encoded payment hash of the invoice.",
			},
			"invoice": {
				Type:        "string",
				Description: "BOLT-11 invoice string. Used if payment_hash is not provided.",
			},
		},
	},
}

// NWCListTransactionsDef is the ToolDefinition for nwc_list_transactions.
var NWCListTransactionsDef = agent.ToolDefinition{
	Name:        "nwc_list_transactions",
	Description: "List recent lightning transactions (incoming and outgoing) via Nostr Wallet Connect (NIP-47). Returns payment type, amount, timestamp, and description for each.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"from": {
				Type:        "number",
				Description: "Start timestamp (unix seconds). Defaults to last 24 hours if omitted.",
			},
			"until": {
				Type:        "number",
				Description: "End timestamp (unix seconds). Defaults to now if omitted.",
			},
			"limit": {
				Type:        "number",
				Description: "Maximum number of transactions to return. Default 20.",
			},
			"type": {
				Type:        "string",
				Description: "Filter by transaction type: 'incoming', 'outgoing', or omit for both.",
			},
		},
	},
}
