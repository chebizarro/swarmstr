// Package toolbuiltin – Cashu NUT ecash tools.
//
// Registers: nuts_mint_quote, nuts_mint_status, nuts_melt_quote, nuts_melt,
//            nuts_balance, nuts_decode, nuts_mint_info
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"

	"swarmstr/internal/agent"
	"swarmstr/internal/nuts"
)

// NutsToolOpts configures the Cashu NUT tools.
type NutsToolOpts struct {
	DefaultMintURL string // default Cashu mint URL
}

// RegisterNutsTools registers Cashu NUT tools into the registry.
func RegisterNutsTools(tools *agent.ToolRegistry, opts NutsToolOpts) {
	resolveMint := func(args map[string]any) string {
		if v, ok := args["mint_url"].(string); ok && v != "" {
			return v
		}
		return opts.DefaultMintURL
	}

	// nuts_mint_info: get mint information.
	tools.Register("nuts_mint_info", func(ctx context.Context, args map[string]any) (string, error) {
		mintURL := resolveMint(args)
		if mintURL == "" {
			return "", fmt.Errorf("nuts_mint_info: mint_url is required")
		}
		client := nuts.NewClient(mintURL)
		info, err := client.Info(ctx)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(info)
		return string(out), nil
	})

	// nuts_mint_quote: create a Lightning invoice to mint ecash tokens.
	tools.Register("nuts_mint_quote", func(ctx context.Context, args map[string]any) (string, error) {
		mintURL := resolveMint(args)
		if mintURL == "" {
			return "", fmt.Errorf("nuts_mint_quote: mint_url is required")
		}
		amount := 0
		if v, ok := args["amount"].(float64); ok {
			amount = int(v)
		}
		if amount <= 0 {
			return "", fmt.Errorf("nuts_mint_quote: amount (sats) is required")
		}
		unit, _ := args["unit"].(string)

		client := nuts.NewClient(mintURL)
		quote, err := client.MintQuoteRequest(ctx, amount, unit)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{
			"quote":    quote.Quote,
			"invoice":  quote.Request,
			"amount":   amount,
			"state":    quote.State,
			"paid":     quote.State == "PAID",
			"expiry":   quote.Expiry,
			"mint_url": mintURL,
		})
		return string(out), nil
	})

	// nuts_mint_status: check if a mint quote has been paid.
	tools.Register("nuts_mint_status", func(ctx context.Context, args map[string]any) (string, error) {
		mintURL := resolveMint(args)
		quoteID, _ := args["quote_id"].(string)
		if mintURL == "" || quoteID == "" {
			return "", fmt.Errorf("nuts_mint_status: mint_url and quote_id are required")
		}
		client := nuts.NewClient(mintURL)
		quote, err := client.CheckMintQuote(ctx, quoteID)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{
			"quote_id": quoteID,
			"state":    quote.State,
			"paid":     quote.State == "PAID",
			"expiry":   quote.Expiry,
		})
		return string(out), nil
	})

	// nuts_melt_quote: create a quote for paying a Lightning invoice with tokens.
	tools.Register("nuts_melt_quote", func(ctx context.Context, args map[string]any) (string, error) {
		mintURL := resolveMint(args)
		invoice, _ := args["invoice"].(string)
		unit, _ := args["unit"].(string)
		if mintURL == "" || invoice == "" {
			return "", fmt.Errorf("nuts_melt_quote: mint_url and invoice are required")
		}
		client := nuts.NewClient(mintURL)
		quote, err := client.MeltQuoteRequest(ctx, invoice, unit)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{
			"quote_id":    quote.Quote,
			"amount":      quote.Amount,
			"fee_reserve": quote.FeeReserve,
			"total":       quote.Amount + quote.FeeReserve,
			"state":       quote.State,
			"paid":        quote.State == "PAID",
			"expiry":      quote.Expiry,
		})
		return string(out), nil
	})

	// nuts_melt: pay a Lightning invoice by melting ecash proofs.
	tools.Register("nuts_melt", func(ctx context.Context, args map[string]any) (string, error) {
		mintURL := resolveMint(args)
		quoteID, _ := args["quote_id"].(string)
		tokenStr, _ := args["token"].(string)
		if mintURL == "" || quoteID == "" || tokenStr == "" {
			return "", fmt.Errorf("nuts_melt: mint_url, quote_id, and token are required")
		}

		token, err := nuts.Decode(tokenStr)
		if err != nil {
			return "", fmt.Errorf("nuts_melt: decode token: %w", err)
		}

		// Collect all proofs.
		var proofs []nuts.Proof
		for _, entry := range token.Token {
			proofs = append(proofs, entry.Proofs...)
		}

		client := nuts.NewClient(mintURL)
		preimage, err := client.Melt(ctx, quoteID, proofs)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{
			"ok":       true,
			"preimage": preimage,
		})
		return string(out), nil
	})

	// nuts_balance: get the total value of a token.
	tools.Register("nuts_balance", func(ctx context.Context, args map[string]any) (string, error) {
		tokenStr, _ := args["token"].(string)
		if tokenStr == "" {
			return "", fmt.Errorf("nuts_balance: token is required")
		}
		token, err := nuts.Decode(tokenStr)
		if err != nil {
			return "", fmt.Errorf("nuts_balance: decode token: %w", err)
		}
		balance := nuts.Balance(token)
		out, _ := json.Marshal(map[string]any{
			"balance": balance,
			"unit":    token.Unit,
			"memo":    token.Memo,
			"proofs":  len(token.Token[0].Proofs), // rough count
		})
		return string(out), nil
	})

	// nuts_decode: decode and inspect a token without spending it.
	tools.Register("nuts_decode", func(ctx context.Context, args map[string]any) (string, error) {
		tokenStr, _ := args["token"].(string)
		if tokenStr == "" {
			return "", fmt.Errorf("nuts_decode: token is required")
		}
		token, err := nuts.Decode(tokenStr)
		if err != nil {
			return "", fmt.Errorf("nuts_decode: %w", err)
		}
		out, _ := json.MarshalIndent(map[string]any{
			"balance": nuts.Balance(token),
			"unit":    token.Unit,
			"memo":    token.Memo,
			"entries": token.Token,
		}, "", "  ")
		return string(out), nil
	})
}
