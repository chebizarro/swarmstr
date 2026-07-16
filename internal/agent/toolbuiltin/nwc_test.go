package toolbuiltin

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"metiq/internal/agent"
)

type recordingNWCClient struct {
	method         string
	params         map[string]any
	result         map[string]any
	err            error
	paymentCalls   int
	paymentInvoice string
	paymentAmount  int64
}

func (c *recordingNWCClient) Request(_ context.Context, method string, params map[string]any) (map[string]any, error) {
	c.method = method
	c.params = params
	return c.result, c.err
}

func (c *recordingNWCClient) PayInvoiceTool(_ context.Context, invoice string, amountMSat int64) (map[string]any, error) {
	c.paymentCalls++
	c.paymentInvoice = invoice
	c.paymentAmount = amountMSat
	return c.result, c.err
}

func execNWCTool(t *testing.T, registry *agent.ToolRegistry, name string, args map[string]any) (string, error) {
	t.Helper()
	return registry.Execute(context.Background(), agent.ToolCall{Name: name, Args: args})
}

func TestRegisterNWCToolsPreservesAllPublicContracts(t *testing.T) {
	registry := agent.NewToolRegistry()
	RegisterNWCTools(registry, NWCToolOpts{Client: &recordingNWCClient{}})
	for _, name := range []string{
		"nwc_get_balance", "nwc_pay_invoice", "nwc_make_invoice",
		"nwc_lookup_invoice", "nwc_list_transactions",
	} {
		if !slices.Contains(registry.List(), name) {
			t.Fatalf("tool %q is not registered: %v", name, registry.List())
		}
		if descriptor, ok := registry.Descriptor(name); !ok || descriptor.Name != name {
			t.Fatalf("descriptor for %q was not preserved: %#v", name, descriptor)
		}
	}
}

func TestNWCPayInvoiceForwardsLegacyArgumentsAndRedactsSecrets(t *testing.T) {
	client := &recordingNWCClient{result: map[string]any{
		"preimage": "abc", "fees_paid": float64(7),
		"nested": map[string]any{"macaroon": "secret-macaroon", "token": "secret-token"},
		"items":  []any{map[string]any{"access_token": "secret-access"}},
	}}
	registry := agent.NewToolRegistry()
	RegisterNWCTools(registry, NWCToolOpts{Client: client})

	output, err := execNWCTool(t, registry, "nwc_pay_invoice", map[string]any{
		"invoice": "lnbc-test", "amount_msats": float64(1234),
	})
	if err != nil {
		t.Fatalf("nwc_pay_invoice: %v", err)
	}
	if client.paymentCalls != 1 || client.paymentInvoice != "lnbc-test" || client.paymentAmount != 1234 {
		t.Fatalf("typed payment call = count=%d invoice=%q amount=%d", client.paymentCalls, client.paymentInvoice, client.paymentAmount)
	}
	if client.method != "" {
		t.Fatalf("raw request path was used: %q %#v", client.method, client.params)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	nested, _ := decoded["nested"].(map[string]any)
	items, _ := decoded["items"].([]any)
	item, _ := items[0].(map[string]any)
	if decoded["preimage"] != redactedNWCToolValue || decoded["fees_paid"] != float64(7) ||
		nested["macaroon"] != redactedNWCToolValue || nested["token"] != redactedNWCToolValue ||
		item["access_token"] != redactedNWCToolValue {
		t.Fatalf("sensitive result fields were not redacted: %#v", decoded)
	}
}

func TestNWCToolErrorsRedactWalletSecrets(t *testing.T) {
	client := &recordingNWCClient{err: errors.New("wallet error: macaroon=secret-macaroon preimage=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef access_token=secret-access")}
	registry := agent.NewToolRegistry()
	RegisterNWCTools(registry, NWCToolOpts{Client: client})

	_, err := execNWCTool(t, registry, "nwc_get_balance", nil)
	if err == nil || strings.Contains(err.Error(), "secret-macaroon") ||
		strings.Contains(err.Error(), "0123456789abcdef") || strings.Contains(err.Error(), "secret-access") {
		t.Fatalf("wallet error was not redacted: %v", err)
	}
}

func TestNWCMakeInvoiceForwardsOptionalFields(t *testing.T) {
	client := &recordingNWCClient{result: map[string]any{"invoice": "lnbc-created"}}
	registry := agent.NewToolRegistry()
	RegisterNWCTools(registry, NWCToolOpts{Client: client})

	if _, err := execNWCTool(t, registry, "nwc_make_invoice", map[string]any{
		"amount_msats": float64(2500), "description": "coffee", "expiry": float64(90),
	}); err != nil {
		t.Fatalf("nwc_make_invoice: %v", err)
	}
	if client.method != nwcMethodMakeInvoice ||
		client.params["amount"] != int64(2500) ||
		client.params["description"] != "coffee" ||
		client.params["expiry"] != int64(90) {
		t.Fatalf("forwarded request = %s %#v", client.method, client.params)
	}
}

func TestNWCLookupAndListPreserveParameterNames(t *testing.T) {
	client := &recordingNWCClient{result: map[string]any{}}
	registry := agent.NewToolRegistry()
	RegisterNWCTools(registry, NWCToolOpts{Client: client})

	if _, err := execNWCTool(t, registry, "nwc_lookup_invoice", map[string]any{"payment_hash": "00"}); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if client.method != nwcMethodLookupInvoice || client.params["payment_hash"] != "00" {
		t.Fatalf("lookup request = %s %#v", client.method, client.params)
	}
	if _, err := execNWCTool(t, registry, "nwc_list_transactions", map[string]any{
		"from": float64(1), "until": float64(2), "limit": float64(3), "type": "outgoing",
	}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if client.method != nwcMethodListTransactions ||
		client.params["from"] != int64(1) ||
		client.params["until"] != int64(2) ||
		client.params["limit"] != int64(3) ||
		client.params["type"] != "outgoing" {
		t.Fatalf("list request = %s %#v", client.method, client.params)
	}
}

func TestNWCToolArgumentValidationHappensBeforeClient(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
	}{
		{name: "nwc_pay_invoice", args: map[string]any{}},
		{name: "nwc_make_invoice", args: map[string]any{}},
		{name: "nwc_lookup_invoice", args: map[string]any{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &recordingNWCClient{result: map[string]any{}}
			registry := agent.NewToolRegistry()
			RegisterNWCTools(registry, NWCToolOpts{Client: client})
			if _, err := execNWCTool(t, registry, test.name, test.args); err == nil {
				t.Fatal("expected argument validation error")
			}
			if client.method != "" {
				t.Fatalf("client was called with %q", client.method)
			}
		})
	}
}

func TestNWCToolsWithoutURIStillRegisterAndFailClosed(t *testing.T) {
	registry := agent.NewToolRegistry()
	RegisterNWCTools(registry, NWCToolOpts{})
	if !slices.Contains(registry.List(), "nwc_get_balance") {
		t.Fatal("nwc_get_balance was not registered")
	}
	if _, err := execNWCTool(t, registry, "nwc_get_balance", map[string]any{}); err == nil {
		t.Fatal("expected missing configuration error")
	}
}
