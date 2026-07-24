package channels

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"metiq/internal/plugins/sdk"
	"metiq/internal/store/state"
)

// channelAccount is a configured, named channel instance that supplies
// credentials and routing defaults internally without exposing secrets.
type channelAccount struct {
	ID       string
	Provider string
	Config   map[string]any
	Default  bool
}

var configuredChannelAccounts = struct {
	sync.RWMutex
	byProvider map[string]map[string]channelAccount
}{byProvider: map[string]map[string]channelAccount{}}

// ConfigureChannelAccounts atomically replaces the gateway action account
// registry from live channel configuration. NostrChannels map keys are the
// account IDs; a single account, an account named "default", or one marked
// config.default_account=true is eligible as the provider default.
func ConfigureChannelAccounts(cfg state.NostrChannelsConfig) {
	byProvider := make(map[string]map[string]channelAccount)
	for accountID, channelCfg := range cfg {
		provider := normalizeChannelProvider(channelCfg.Kind)
		accountID = strings.TrimSpace(accountID)
		if provider == "" || accountID == "" {
			continue
		}
		accountCfg := channelConfigToMap(channelCfg)
		isDefault, _ := accountCfg["default_account"].(bool)
		if !isDefault {
			isDefault = strings.EqualFold(accountID, "default")
		}
		if byProvider[provider] == nil {
			byProvider[provider] = make(map[string]channelAccount)
		}
		byProvider[provider][accountID] = channelAccount{
			ID:       accountID,
			Provider: provider,
			Config:   cloneAccountParams(accountCfg),
			Default:  isDefault,
		}
	}

	configuredChannelAccounts.Lock()
	configuredChannelAccounts.byProvider = byProvider
	configuredChannelAccounts.Unlock()
}

// ResolveChannelAccountParams merges a named/default configured account into
// gateway action params. Caller-supplied action fields take precedence. When a
// provider has no configured accounts, params are copied unchanged to preserve
// legacy direct-credential invocations.
func ResolveChannelAccountParams(provider string, params map[string]any) (map[string]any, error) {
	provider = normalizeChannelProvider(provider)
	requested := requestedAccountID(params)

	configuredChannelAccounts.RLock()
	accounts := configuredChannelAccounts.byProvider[provider]
	selected, err := selectChannelAccount(provider, requested, accounts)
	configuredChannelAccounts.RUnlock()
	if err != nil {
		return nil, err
	}
	if selected == nil {
		return cloneAccountParams(params), nil
	}

	resolved := cloneAccountParams(selected.Config)
	for key, value := range params {
		if key == "accountId" || key == "account_id" {
			continue
		}
		resolved[key] = value
	}
	resolved["account_id"] = selected.ID
	return resolved, nil
}

// AccountScopedGatewayMethods wraps channel gateway handlers with configured
// account resolution. All built-in action-capable channel plugins use this
// helper so account semantics remain consistent across providers.
func AccountScopedGatewayMethods(provider string, methods []sdk.GatewayMethod) []sdk.GatewayMethod {
	wrapped := make([]sdk.GatewayMethod, len(methods))
	for i, method := range methods {
		wrapped[i] = method
		if method.Handle == nil {
			continue
		}
		handle := method.Handle
		methodName := method.Method
		wrapped[i].Handle = func(ctx context.Context, params map[string]any) (map[string]any, error) {
			resolved, err := ResolveChannelAccountParams(provider, params)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", methodName, err)
			}
			return handle(ctx, resolved)
		}
	}
	return wrapped
}

func selectChannelAccount(provider, requested string, accounts map[string]channelAccount) (*channelAccount, error) {
	if len(accounts) == 0 {
		if requested != "" {
			return nil, fmt.Errorf("channel account %q is not configured for provider %q", requested, provider)
		}
		return nil, nil
	}
	if requested != "" {
		account, ok := accounts[requested]
		if !ok {
			return nil, fmt.Errorf("channel account %q is not configured for provider %q (available: %s)", requested, provider, strings.Join(sortedAccountIDs(accounts), ", "))
		}
		copy := account
		copy.Config = cloneAccountParams(account.Config)
		return &copy, nil
	}

	var defaults []channelAccount
	for _, account := range accounts {
		if account.Default {
			defaults = append(defaults, account)
		}
	}
	if len(defaults) > 1 {
		ids := make([]string, 0, len(defaults))
		for _, account := range defaults {
			ids = append(ids, account.ID)
		}
		sort.Strings(ids)
		return nil, fmt.Errorf("multiple default channel accounts configured for provider %q: %s", provider, strings.Join(ids, ", "))
	}
	if len(defaults) == 1 {
		copy := defaults[0]
		copy.Config = cloneAccountParams(copy.Config)
		return &copy, nil
	}
	if len(accounts) == 1 {
		for _, account := range accounts {
			copy := account
			copy.Config = cloneAccountParams(copy.Config)
			return &copy, nil
		}
	}
	return nil, fmt.Errorf("channel account is required for provider %q (available: %s); mark one config.default_account=true to select a default", provider, strings.Join(sortedAccountIDs(accounts), ", "))
}

func requestedAccountID(params map[string]any) string {
	for _, key := range []string{"account_id", "accountId"} {
		if value, ok := params[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sortedAccountIDs(accounts map[string]channelAccount) []string {
	ids := make([]string, 0, len(accounts))
	for id := range accounts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func normalizeChannelProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "nextcloud" {
		return "nextcloud-talk"
	}
	return provider
}

func cloneAccountParams(src map[string]any) map[string]any {
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}
