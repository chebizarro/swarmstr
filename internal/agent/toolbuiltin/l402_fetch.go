package toolbuiltin

import (
	"context"
	"fmt"
	"time"

	"metiq/internal/agent"
	"metiq/internal/agent/toolgrpc"
	"metiq/internal/browser"
)

type L402Fetcher interface {
	Fetch(context.Context, browser.Request) (browser.Response, error)
}

type L402FetchOpts struct {
	Client            L402Fetcher
	MaxPaymentTimeout time.Duration
}

func L402FetchTool(opts L402FetchOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		if opts.Client == nil {
			return "", fmt.Errorf("l402_fetch: client is unavailable")
		}
		rawURL := agent.ArgString(args, "url")
		if rawURL == "" {
			return "", fmt.Errorf("l402_fetch: url is required")
		}
		maxChars := agent.ArgInt(args, "max_chars", defaultWebFetchMaxChars)
		if maxChars <= 0 {
			maxChars = defaultWebFetchMaxChars
		}
		timeout := time.Duration(agent.ArgInt(args, "timeout_seconds", defaultWebFetchTimeoutSec)) * time.Second
		if timeout <= 0 {
			timeout = defaultWebFetchTimeoutSec * time.Second
		}
		if opts.MaxPaymentTimeout > 0 && timeout > opts.MaxPaymentTimeout {
			timeout = opts.MaxPaymentTimeout
		}

		response, err := opts.Client.Fetch(ctx, browser.Request{
			Method:    "GET",
			URL:       rawURL,
			TimeoutMS: int(timeout / time.Millisecond),
		})
		if err != nil {
			return "", fmt.Errorf("l402_fetch: %w", toolgrpc.NewRedactor().RedactError(err))
		}
		content := response.Text
		if content == "" {
			content = response.Body
		}
		return Truncate(content, maxChars), nil
	}
}

// L402FetchRegistration builds the controller-owned registration without
// requiring a temporary registry.
func L402FetchRegistration(opts L402FetchOpts) agent.ToolRegistration {
	return agent.ToolRegistration{
		Func: L402FetchTool(opts),
		Descriptor: agent.ToolDescriptor{
			Name: L402FetchDef.Name, Description: L402FetchDef.Description,
			Parameters: L402FetchDef.Parameters, ParamAliases: L402FetchDef.ParamAliases,
			Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindBuiltin},
			Traits: agent.ToolTraits{
				ConcurrencySafe: true, Destructive: true,
				InterruptBehavior: agent.ToolInterruptBehaviorBlock,
			},
			Exposure: agent.ToolExposureModeAuto,
		},
		ProviderVisible: true,
	}
}
