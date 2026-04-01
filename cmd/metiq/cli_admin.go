package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/config"
	"metiq/internal/nostr/events"
	nostruntime "metiq/internal/nostr/runtime"
)

type gatewayCaller interface {
	call(method string, params any) (map[string]any, error)
}

type gatewayCloser interface {
	Close()
}

// adminClient is a minimal HTTP client for the metiqd admin API.
type adminClient struct {
	addr  string
	token string
}

type nostrControlClient struct {
	hub            *nostruntime.NostrHub
	callerPubKey   string
	targetPub      nostr.PubKey
	targetPubKey   string
	fallbackRelays []string
	timeout        time.Duration
}

var resolveGWClientFn = resolveGWClient

func resolveGWClient(transport, addrFlag, tokenFlag, bootstrapPath, controlTargetPubKey, controlSignerURL string, timeout time.Duration) (gatewayCaller, error) {
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "", "http":
		return resolveAdminClient(addrFlag, tokenFlag, bootstrapPath)
	case "nostr":
		return resolveNostrControlClient(bootstrapPath, controlTargetPubKey, controlSignerURL, timeout)
	default:
		return nil, fmt.Errorf("unsupported gw transport %q (expected http or nostr)", transport)
	}
}

func resolveNostrControlClient(bootstrapPath, controlTargetPubKey, controlSignerURL string, timeout time.Duration) (*nostrControlClient, error) {
	cfg, err := config.LoadBootstrapForControl(bootstrapPath)
	if err != nil {
		return nil, fmt.Errorf("load bootstrap: %w", err)
	}

	targetRaw := strings.TrimSpace(controlTargetPubKey)
	if targetRaw == "" {
		targetRaw = strings.TrimSpace(cfg.ControlTargetPubKey)
	}
	if targetRaw == "" {
		return nil, fmt.Errorf(
			"nostr control target pubkey not configured.\n" +
				"Set control_target_pubkey in ~/.metiq/bootstrap.json,\n" +
				"or pass --control-target-pubkey.",
		)
	}
	target, err := nostruntime.ParsePubKey(targetRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid control target pubkey: %w", err)
	}

	resolveCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	keyer, err := config.ResolveSigner(resolveCtx, effectiveControlSignerConfig(cfg, controlSignerURL), nil)
	if err != nil {
		return nil, fmt.Errorf("resolve control signer: %w", err)
	}
	caller, err := keyer.GetPublicKey(resolveCtx)
	if err != nil {
		return nil, fmt.Errorf("resolve control signer pubkey: %w", err)
	}
	if caller.Hex() == target.Hex() {
		return nil, fmt.Errorf(
			"nostr control caller pubkey %s matches target daemon pubkey %s; configure a distinct control signer via control_signer_url or --control-signer-url",
			caller.Hex(), target.Hex(),
		)
	}

	selector := nostruntime.NewRelaySelector(cfg.Relays, cfg.Relays)
	hub, err := nostruntime.NewHub(context.Background(), keyer, selector)
	if err != nil {
		return nil, fmt.Errorf("create nostr control hub: %w", err)
	}

	return &nostrControlClient{
		hub:            hub,
		callerPubKey:   caller.Hex(),
		targetPub:      target,
		targetPubKey:   target.Hex(),
		fallbackRelays: append([]string{}, cfg.Relays...),
		timeout:        timeout,
	}, nil
}

func effectiveControlSignerConfig(cfg config.BootstrapConfig, signerURLOverride string) config.BootstrapConfig {
	override := strings.TrimSpace(signerURLOverride)
	if override != "" {
		return config.BootstrapConfig{SignerURL: override}
	}
	if control := strings.TrimSpace(cfg.ControlSignerURL); control != "" {
		cfg.PrivateKey = ""
		cfg.SignerURL = control
	}
	return cfg
}

func (c *nostrControlClient) Close() {
	if c != nil && c.hub != nil {
		c.hub.Close()
	}
}

// resolveAdminClient builds an adminClient from the --admin-addr flag (or env
// METIQ_ADMIN_ADDR) and the --admin-token flag (or env METIQ_ADMIN_TOKEN).
// If neither is set, it falls back to admin_listen_addr in the bootstrap config.
func resolveAdminClient(addrFlag, tokenFlag, bootstrapPath string) (*adminClient, error) {
	addr := addrFlag
	token := tokenFlag

	if addr == "" {
		addr = os.Getenv("METIQ_ADMIN_ADDR")
	}
	if token == "" {
		token = os.Getenv("METIQ_ADMIN_TOKEN")
	}

	// Fall back to bootstrap config (lenient read — no validation).
	if addr == "" {
		bsPath := bootstrapPath
		if bsPath == "" {
			if p, err := config.DefaultBootstrapPath(); err == nil {
				bsPath = p
			}
		}
		if bsPath != "" {
			if raw, err := os.ReadFile(bsPath); err == nil {
				var m map[string]any
				if json.Unmarshal(raw, &m) == nil {
					if v, ok := m["admin_listen_addr"].(string); ok {
						addr = v
					}
					if token == "" {
						if v, ok := m["admin_token"].(string); ok {
							token = v
						}
					}
				}
			}
		}
	}

	if addr == "" {
		return nil, fmt.Errorf(
			"admin API address not configured.\n" +
				"Set admin_listen_addr in ~/.metiq/bootstrap.json,\n" +
				"or pass --admin-addr, or set METIQ_ADMIN_ADDR.",
		)
	}

	return &adminClient{addr: addr, token: token}, nil
}

func (c *adminClient) baseURL() string {
	return "http://" + c.addr
}

// call invokes a gateway method via POST /call.
func (c *adminClient) call(method string, params any) (map[string]any, error) {
	body, err := json.Marshal(map[string]any{
		"method": method,
		"params": params,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL()+"/call", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	cl := &http.Client{Timeout: 30 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach daemon at %s: %w", c.addr, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var envelope struct {
		OK     bool           `json:"ok"`
		Error  string         `json:"error,omitempty"`
		Result map[string]any `json:"result,omitempty"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("invalid response from daemon: %w", err)
	}
	if !envelope.OK {
		msg := envelope.Error
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("daemon error: %s", msg)
	}
	return envelope.Result, nil
}

func (c *nostrControlClient) call(method string, params any) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	requestID, err := newControlRequestID()
	if err != nil {
		return nil, fmt.Errorf("generate request id: %w", err)
	}

	body, err := json.Marshal(map[string]any{
		"method": method,
		"params": params,
	})
	if err != nil {
		return nil, err
	}

	responseCh := make(chan nostr.Event, 1)
	subscriptionID := "gw-control-" + requestID
	_, err = c.hub.Subscribe(ctx, nostruntime.SubOpts{
		ID:     subscriptionID,
		Relays: append([]string{}, c.fallbackRelays...),
		Filter: nostr.Filter{
			Kinds:   []nostr.Kind{nostr.Kind(events.KindMCPResult)},
			Authors: []nostr.PubKey{c.targetPub},
			Tags: nostr.TagMap{
				"p":   []string{c.callerPubKey},
				"req": []string{requestID},
			},
		},
		OnEvent: func(re nostr.RelayEvent) {
			evt := re.Event
			if !evt.CheckID() || !evt.VerifySignature() {
				return
			}
			select {
			case responseCh <- evt:
			default:
			}
		},
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe for control response: %w", err)
	}
	defer c.hub.Unsubscribe(subscriptionID)

	evt := nostr.Event{
		Kind:      nostr.Kind(events.KindControl),
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"p", c.targetPubKey},
			{"req", requestID},
			{"t", "control_rpc"},
		},
		Content: string(body),
	}
	if err := c.hub.SignEvent(ctx, &evt); err != nil {
		return nil, fmt.Errorf("sign control request: %w", err)
	}
	if err := c.publishRequest(ctx, evt, requestID); err != nil {
		return nil, err
	}

	select {
	case response := <-responseCh:
		return decodeControlResponse(response.Content)
	case <-ctx.Done():
		return nil, fmt.Errorf("timed out waiting for nostr control response req=%s: %w", requestID, ctx.Err())
	}
}

func (c *nostrControlClient) publishRequest(ctx context.Context, evt nostr.Event, requestID string) error {
	published := false
	var lastErr error
	for res := range c.hub.Publish(ctx, append([]string{}, c.fallbackRelays...), evt) {
		if res.Error == nil {
			published = true
			continue
		}
		lastErr = fmt.Errorf("relay %s: %w", res.RelayURL, res.Error)
	}
	if published {
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("publish nostr control request req=%s: %w", requestID, lastErr)
	}
	return fmt.Errorf("publish nostr control request req=%s: no relays accepted the event", requestID)
}

func decodeControlResponse(content string) (map[string]any, error) {
	var envelope struct {
		Result map[string]any `json:"result,omitempty"`
		Error  *struct {
			Code    int            `json:"code"`
			Message string         `json:"message"`
			Data    map[string]any `json:"data,omitempty"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal([]byte(content), &envelope); err != nil {
		return nil, fmt.Errorf("invalid nostr control response: %w", err)
	}
	if envelope.Error != nil {
		msg := envelope.Error.Message
		if msg == "" {
			msg = "unknown daemon error"
		}
		return nil, fmt.Errorf("daemon error: %s", msg)
	}
	return envelope.Result, nil
}

func newControlRequestID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// get performs an HTTP GET and decodes the JSON response.
func (c *adminClient) get(path string) (map[string]any, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL()+path, nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	cl := &http.Client{Timeout: 10 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach daemon at %s: %w", c.addr, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}
	return result, nil
}

// printJSON pretty-prints v as indented JSON to stdout.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// stringField safely extracts a string from a map.
func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// floatField safely extracts a float64 from a map.
func floatField(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	if v, ok := m[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}
