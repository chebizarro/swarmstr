// Package toolbuiltin provides the existing NIP-47 Nostr Wallet Connect tools.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/agent"
	"metiq/internal/agent/toolgrpc"
	"metiq/internal/lightning"
	nostruntime "metiq/internal/nostr/runtime"
)

const (
	KindNWCInfo     = lightning.KindNWCInfo
	KindNWCRequest  = lightning.KindNWCRequest
	KindNWCResponse = lightning.KindNWCResponse

	nwcMethodGetBalance       = lightning.NWCMethodGetBalance
	nwcMethodPayInvoice       = lightning.NWCMethodPayInvoice
	nwcMethodMakeInvoice      = lightning.NWCMethodMakeInvoice
	nwcMethodLookupInvoice    = lightning.NWCMethodLookupInvoice
	nwcMethodListTransactions = lightning.NWCMethodListTransactions
)

// NWCToolClient is the raw result contract needed by the compatibility tools.
// *lightning.NWCClient implements it.
type NWCToolClient interface {
	Request(context.Context, string, map[string]any) (map[string]any, error)
}

type nwcToolPaymentClient interface {
	PayInvoiceTool(context.Context, string, int64) (map[string]any, error)
}

const redactedNWCToolValue = "[REDACTED]"

func redactNWCToolError(err error) error {
	return toolgrpc.NewRedactor().RedactError(err)
}

func redactNWCToolResult(value any) any {
	return toolgrpc.NewRedactor().RedactValue(value)
}

// NWCToolOpts configures the NWC tools. Client is preferred; the legacy fields
// remain supported so existing daemon assembly and external callers keep their
// contracts while sharing the same internal/lightning implementation.
type NWCToolOpts struct {
	Client NWCToolClient

	HubFunc func() *nostruntime.NostrHub
	Keyer   nostr.Keyer
	NWCUri  string
	Relays  []string
	Timeout time.Duration
}

// RegisterNWCTools registers the existing five nwc_* tools without changing
// their names, schemas, arguments, or JSON result objects.
func RegisterNWCTools(tools *agent.ToolRegistry, opts NWCToolOpts) {
	client := opts.Client
	var clientErr error
	if client == nil {
		timeout := opts.Timeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		client, clientErr = lightning.NewNWCClient(lightning.NWCClientConfig{
			ID: "nwc", URI: opts.NWCUri, Relays: opts.Relays, Keyer: opts.Keyer,
			Timeout: timeout, Transport: lightning.NewHubNWCTransport(opts.HubFunc),
		})
	}
	request := func(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
		if clientErr != nil {
			return nil, fmt.Errorf("NWC is not configured: %w", clientErr)
		}
		if client == nil {
			return nil, fmt.Errorf("NWC client is unavailable")
		}
		return client.Request(ctx, method, params)
	}
	jsonResult := func(data any) (string, error) {
		out, err := json.MarshalIndent(redactNWCToolResult(data), "", "  ")
		if err != nil {
			return "", fmt.Errorf("NWC: marshal result: %w", err)
		}
		return string(out), nil
	}

	tools.RegisterWithDef("nwc_get_balance", func(ctx context.Context, _ map[string]any) (string, error) {
		result, err := request(ctx, nwcMethodGetBalance, nil)
		if err != nil {
			return "", redactNWCToolError(err)
		}
		return jsonResult(result)
	}, NWCGetBalanceDef)

	tools.RegisterWithDef("nwc_pay_invoice", func(ctx context.Context, args map[string]any) (string, error) {
		invoice, _ := args["invoice"].(string)
		if invoice == "" {
			return "", fmt.Errorf("nwc_pay_invoice: invoice is required")
		}
		params := map[string]any{"invoice": invoice}
		var amountMSat int64
		if amountMSats, ok := args["amount_msats"].(float64); ok && amountMSats > 0 {
			amountMSat = int64(amountMSats)
			params["amount"] = amountMSat
		}
		var result map[string]any
		var err error
		if paymentClient, ok := client.(nwcToolPaymentClient); ok && clientErr == nil {
			result, err = paymentClient.PayInvoiceTool(ctx, invoice, amountMSat)
		} else {
			result, err = request(ctx, nwcMethodPayInvoice, params)
		}
		if err != nil {
			return "", redactNWCToolError(err)
		}
		return jsonResult(result)
	}, NWCPayInvoiceDef)

	tools.RegisterWithDef("nwc_make_invoice", func(ctx context.Context, args map[string]any) (string, error) {
		amountMSats, ok := args["amount_msats"].(float64)
		if !ok || amountMSats <= 0 {
			return "", fmt.Errorf("nwc_make_invoice: amount_msats is required and must be positive")
		}
		params := map[string]any{"amount": int64(amountMSats)}
		if description, ok := args["description"].(string); ok && description != "" {
			params["description"] = description
		}
		if expiry, ok := args["expiry"].(float64); ok && expiry > 0 {
			params["expiry"] = int64(expiry)
		}
		result, err := request(ctx, nwcMethodMakeInvoice, params)
		if err != nil {
			return "", redactNWCToolError(err)
		}
		return jsonResult(result)
	}, NWCMakeInvoiceDef)

	tools.RegisterWithDef("nwc_lookup_invoice", func(ctx context.Context, args map[string]any) (string, error) {
		params := map[string]any{}
		if paymentHash, ok := args["payment_hash"].(string); ok && paymentHash != "" {
			params["payment_hash"] = paymentHash
		} else if invoice, ok := args["invoice"].(string); ok && invoice != "" {
			params["invoice"] = invoice
		} else {
			return "", fmt.Errorf("nwc_lookup_invoice: payment_hash or invoice is required")
		}
		result, err := request(ctx, nwcMethodLookupInvoice, params)
		if err != nil {
			return "", redactNWCToolError(err)
		}
		return jsonResult(result)
	}, NWCLookupInvoiceDef)

	tools.RegisterWithDef("nwc_list_transactions", func(ctx context.Context, args map[string]any) (string, error) {
		params := map[string]any{}
		if from, ok := args["from"].(float64); ok && from > 0 {
			params["from"] = int64(from)
		}
		if until, ok := args["until"].(float64); ok && until > 0 {
			params["until"] = int64(until)
		}
		if limit, ok := args["limit"].(float64); ok && limit > 0 {
			params["limit"] = int64(limit)
		} else {
			params["limit"] = int64(20)
		}
		if transactionType, ok := args["type"].(string); ok && transactionType != "" {
			params["type"] = transactionType
		}
		result, err := request(ctx, nwcMethodListTransactions, params)
		if err != nil {
			return "", redactNWCToolError(err)
		}
		return jsonResult(result)
	}, NWCListTransactionsDef)
}
