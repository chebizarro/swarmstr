package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"metiq/internal/agent"
	"metiq/internal/plugins/registry"
	"metiq/internal/plugins/runtime"
)

type ProviderInvoker interface {
	InvokeProvider(ctx context.Context, providerID, method string, params any) (any, error)
}

var _ ProviderInvoker = (*runtime.OpenClawPluginHost)(nil)
var _ agent.ChatProvider = (*PluginProviderBridge)(nil)
var _ agent.Provider = (*PluginProviderBridge)(nil)

type ModelEntry struct {
	ID            string         `json:"id"`
	Name          string         `json:"name,omitempty"`
	ProviderID    string         `json:"provider_id,omitempty"`
	API           string         `json:"api,omitempty"`
	BaseURL       string         `json:"base_url,omitempty"`
	ContextWindow int            `json:"context_window,omitempty"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
	Input         []string       `json:"input,omitempty"`
	Reasoning     bool           `json:"reasoning,omitempty"`
	Cost          map[string]any `json:"cost,omitempty"`
	Raw           map[string]any `json:"raw,omitempty"`
}

type AuthResult struct {
	OK     bool           `json:"ok"`
	Raw    map[string]any `json:"raw,omitempty"`
	Error  string         `json:"error,omitempty"`
	Config map[string]any `json:"config,omitempty"`
}

type PluginProviderBridge struct {
	providerID string
	pluginID   string
	host       ProviderInvoker
	metadata   *registry.RegisteredProvider

	modelID string
	config  map[string]any
	env     map[string]string
	apiKeys map[string]string

	chatMethods   []string
	streamMethods []string
	fallbacks     []agent.ChatProvider

	mu           sync.RWMutex
	catalogCache []ModelEntry
	catalogTime  time.Time
	nativeCache  map[string]agent.ChatProvider
}

type BridgeOption func(*PluginProviderBridge)

func WithModel(modelID string) BridgeOption {
	return func(p *PluginProviderBridge) { p.modelID = strings.TrimSpace(modelID) }
}
func WithConfig(config map[string]any) BridgeOption {
	return func(p *PluginProviderBridge) { p.config = cloneMap(config) }
}
func WithEnv(env map[string]string) BridgeOption {
	return func(p *PluginProviderBridge) { p.env = cloneStringMap(env) }
}
func WithAPIKeys(apiKeys map[string]string) BridgeOption {
	return func(p *PluginProviderBridge) { p.apiKeys = cloneStringMap(apiKeys) }
}
func WithFallbacks(fallbacks ...agent.ChatProvider) BridgeOption {
	return func(p *PluginProviderBridge) { p.fallbacks = append([]agent.ChatProvider(nil), fallbacks...) }
}
func WithMethods(methods ...string) BridgeOption {
	return func(p *PluginProviderBridge) { p.chatMethods = cleanMethods(methods) }
}
func WithStreamMethods(methods ...string) BridgeOption {
	return func(p *PluginProviderBridge) { p.streamMethods = cleanMethods(methods) }
}

func NewPluginProviderBridge(providerID, pluginID string, host ProviderInvoker, meta *registry.RegisteredProvider, opts ...BridgeOption) *PluginProviderBridge {
	p := &PluginProviderBridge{
		providerID: strings.TrimSpace(providerID), pluginID: strings.TrimSpace(pluginID), host: host,
		metadata: cloneProviderMetadata(meta), chatMethods: []string{"chat", "complete", "messages", "generate"},
		streamMethods: []string{"stream", "chat_stream", "streamChat"}, nativeCache: map[string]agent.ChatProvider{},
	}
	if p.providerID == "" && meta != nil {
		p.providerID = meta.ID
	}
	if p.pluginID == "" && meta != nil {
		p.pluginID = meta.PluginID
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *PluginProviderBridge) ProviderID() string { return p.providerID }
func (p *PluginProviderBridge) PluginID() string   { return p.pluginID }

func (p *PluginProviderBridge) Chat(ctx context.Context, messages []agent.LLMMessage, tools []agent.ToolDefinition, opts agent.ChatOptions) (*agent.LLMResponse, error) {
	if p == nil || p.host == nil {
		return nil, fmt.Errorf("plugin provider bridge is not configured")
	}
	req := p.chatRequest(messages, tools, opts, false)
	var lastErr error
	for _, method := range p.chatMethods {
		result, err := p.host.InvokeProvider(ctx, p.providerID, method, req)
		if err != nil {
			lastErr = err
			if isMissingProviderMethod(err) {
				continue
			}
			break
		}
		resp, err := TranslateResponseFromOpenClaw(result)
		if err != nil {
			return nil, fmt.Errorf("provider %s %s response: %w", p.providerID, method, err)
		}
		return resp, nil
	}
	if native, err := p.nativeCatalogProvider(ctx); err == nil && native != nil {
		if resp, err := native.Chat(ctx, messages, tools, opts); err == nil {
			return resp, nil
		} else {
			lastErr = err
		}
	}
	for _, fb := range p.fallbacks {
		if fb != nil {
			if resp, err := fb.Chat(ctx, messages, tools, opts); err == nil {
				return resp, nil
			} else {
				lastErr = err
			}
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("provider %s chat: %w", p.providerID, lastErr)
	}
	return nil, fmt.Errorf("provider %s has no supported chat method", p.providerID)
}

func (p *PluginProviderBridge) Generate(ctx context.Context, turn agent.Turn) (agent.ProviderResult, error) {
	messages := make([]agent.LLMMessage, 0, len(turn.History)+2)
	if sys := strings.TrimSpace(strings.Join(nonEmpty(turn.StaticSystemPrompt, turn.Context), "\n\n")); sys != "" {
		messages = append(messages, agent.LLMMessage{Role: "system", Content: sys})
	}
	for _, h := range turn.History {
		m := agent.LLMMessage{Role: h.Role, Content: h.Content, ToolCallID: h.ToolCallID}
		for _, ref := range h.ToolCalls {
			tc := agent.ToolCall{ID: ref.ID, Name: ref.Name}
			if ref.ArgsJSON != "" {
				_ = json.Unmarshal([]byte(ref.ArgsJSON), &tc.Args)
			}
			m.ToolCalls = append(m.ToolCalls, tc)
		}
		messages = append(messages, m)
	}
	messages = append(messages, agent.LLMMessage{Role: "user", Content: turn.UserText, Images: turn.Images})
	maxTokens := 4096
	if turn.ThinkingBudget > 0 {
		maxTokens = turn.ThinkingBudget + turn.ThinkingBudget/2
		if maxTokens < 16000 {
			maxTokens = 16000
		}
	}
	resp, err := agent.RunAgenticLoop(ctx, agent.AgenticLoopConfig{Provider: p, InitialMessages: messages, Tools: turn.Tools, Executor: turn.Executor, Options: agent.ChatOptions{MaxTokens: maxTokens, ThinkingBudget: turn.ThinkingBudget, CacheSystem: true, CacheTools: true}, MaxIterations: turn.MaxAgenticIterations, ModelID: p.effectiveModelID(), LogPrefix: "plugin-provider:" + p.providerID, SessionID: turn.SessionID, TurnID: turn.TurnID, ToolEventSink: turn.ToolEventSink, ContextWindowTokens: turn.ContextWindowTokens, Trace: turn.Trace, LastAssistantTime: turn.LastAssistantTime, DeferredTools: turn.DeferredTools})
	if err != nil {
		return agent.ProviderResult{}, err
	}
	return agent.ProviderResult{Text: resp.Content, ToolCalls: resp.ToolCalls, Usage: resp.Usage, HistoryDelta: resp.HistoryDelta, Outcome: resp.Outcome, StopReason: resp.StopReason}, nil
}

func (p *PluginProviderBridge) StreamChat(ctx context.Context, messages []agent.LLMMessage, tools []agent.ToolDefinition, opts agent.ChatOptions, onChunk func(string)) (*agent.LLMResponse, error) {
	req := p.chatRequest(messages, tools, opts, true)
	for _, method := range p.streamMethods {
		result, err := p.host.InvokeProvider(ctx, p.providerID, method, req)
		if err != nil {
			if isMissingProviderMethod(err) {
				continue
			}
			break
		}
		resp, chunks, err := translateStreamResult(result)
		if err != nil {
			return nil, err
		}
		if onChunk != nil {
			for _, c := range chunks {
				onChunk(c)
			}
		}
		return resp, nil
	}
	resp, err := p.Chat(ctx, messages, tools, opts)
	if err != nil {
		return nil, err
	}
	if onChunk != nil && resp.Content != "" {
		onChunk(resp.Content)
	}
	return resp, nil
}

func (p *PluginProviderBridge) RefreshCatalog(ctx context.Context) ([]ModelEntry, error) {
	if p == nil || p.host == nil {
		return nil, fmt.Errorf("plugin provider bridge is not configured")
	}
	result, err := p.host.InvokeProvider(ctx, p.providerID, "catalog", p.catalogRequest())
	if err != nil && isMissingProviderMethod(err) {
		result, err = p.host.InvokeProvider(ctx, p.providerID, "staticCatalog", p.catalogRequest())
	}
	if err != nil {
		return nil, fmt.Errorf("provider %s catalog: %w", p.providerID, err)
	}
	entries, err := ParseCatalogResult(p.providerID, result)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.catalogCache = append([]ModelEntry(nil), entries...)
	p.catalogTime = time.Now()
	p.mu.Unlock()
	return entries, nil
}

func (p *PluginProviderBridge) Catalog(ctx context.Context) ([]ModelEntry, error) {
	p.mu.RLock()
	if len(p.catalogCache) > 0 {
		out := append([]ModelEntry(nil), p.catalogCache...)
		p.mu.RUnlock()
		return out, nil
	}
	p.mu.RUnlock()
	return p.RefreshCatalog(ctx)
}

func (p *PluginProviderBridge) Auth(ctx context.Context, authID string, params map[string]any) (AuthResult, error) {
	if p == nil || p.host == nil {
		return AuthResult{}, fmt.Errorf("plugin provider bridge is not configured")
	}
	req := cloneMap(params)
	if req == nil {
		req = map[string]any{}
	}
	req["auth_id"] = authID
	req["provider_id"] = p.providerID
	req["plugin_id"] = p.pluginID
	req["config"] = cloneMap(p.config)
	req["env"] = p.effectiveEnv()
	req["api_keys"] = p.effectiveAPIKeys()
	result, err := p.host.InvokeProvider(ctx, p.providerID, "auth", req)
	if err != nil {
		return AuthResult{}, fmt.Errorf("provider %s auth: %w", p.providerID, err)
	}
	m := asMap(result)
	if len(m) == 0 {
		return AuthResult{OK: true, Raw: map[string]any{"value": result}}, nil
	}
	out := AuthResult{OK: true, Raw: cloneMap(m)}
	if v, ok := m["ok"]; ok {
		out.OK = boolValue(v)
	}
	out.Error, _ = m["error"].(string)
	if cfg, ok := m["config"].(map[string]any); ok {
		out.Config = cloneMap(cfg)
	}
	return out, nil
}

func (p *PluginProviderBridge) chatRequest(messages []agent.LLMMessage, tools []agent.ToolDefinition, opts agent.ChatOptions, stream bool) map[string]any {
	return map[string]any{"provider_id": p.providerID, "plugin_id": p.pluginID, "model": p.effectiveModelID(), "messages": TranslateMessagesToOpenClaw(messages), "tools": TranslateToolsToOpenClaw(tools), "options": map[string]any{"max_tokens": opts.MaxTokens, "thinking_budget": opts.ThinkingBudget, "cache_system": opts.CacheSystem, "cache_tools": opts.CacheTools, "stream": stream}, "config": cloneMap(p.config), "env": p.effectiveEnv(), "api_keys": p.effectiveAPIKeys()}
}
func (p *PluginProviderBridge) catalogRequest() map[string]any {
	return map[string]any{"provider_id": p.providerID, "plugin_id": p.pluginID, "model": p.effectiveModelID(), "config": cloneMap(p.config), "env": p.effectiveEnv(), "api_keys": p.effectiveAPIKeys()}
}
func (p *PluginProviderBridge) effectiveModelID() string {
	if strings.TrimSpace(p.modelID) != "" {
		return strings.TrimSpace(p.modelID)
	}
	return p.providerID
}
func (p *PluginProviderBridge) effectiveEnv() map[string]string {
	if p.env != nil {
		return cloneStringMap(p.env)
	}
	out := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}
func (p *PluginProviderBridge) effectiveAPIKeys() map[string]string {
	out := cloneStringMap(p.apiKeys)
	if out == nil {
		out = map[string]string{}
	}
	if p.apiKeys == nil {
		if _, ok := out[p.providerID]; !ok {
			if key := p.resolveAPIKeyFromEnv(); key != "" {
				out[p.providerID] = key
			}
		}
	}
	return out
}
func (p *PluginProviderBridge) resolveAPIKeyFromEnv() string {
	for _, envKey := range p.providerEnvVars() {
		if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
			return v
		}
	}
	return ""
}
func (p *PluginProviderBridge) providerEnvVars() []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v != "" {
			if _, ok := seen[v]; !ok {
				seen[v] = struct{}{}
				out = append(out, v)
			}
		}
	}
	if p.metadata != nil {
		for _, key := range []string{"envVar", "envVars", "providerAuthEnvVars"} {
			for _, v := range stringSliceFromAny(p.metadata.Raw[key]) {
				add(v)
			}
		}
		if authMethods, ok := p.metadata.Raw["auth"].([]any); ok {
			for _, raw := range authMethods {
				add(stringValue(asMap(raw)["envVar"]))
			}
		}
	}
	switch strings.ToLower(p.providerID) {
	case "anthropic":
		add("ANTHROPIC_API_KEY")
	case "openai":
		add("OPENAI_API_KEY")
	case "google", "gemini", "google-gemini-cli":
		add("GEMINI_API_KEY")
		add("GOOGLE_API_KEY")
		add("GOOGLE_GENERATIVE_AI_API_KEY")
	}
	return out
}

func (p *PluginProviderBridge) nativeCatalogProvider(ctx context.Context) (agent.ChatProvider, error) {
	model := p.effectiveModelID()
	p.mu.RLock()
	if cached := p.nativeCache[model]; cached != nil {
		p.mu.RUnlock()
		return cached, nil
	}
	p.mu.RUnlock()
	entries, err := p.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	entry, ok := selectModelEntry(p.providerID, model, entries)
	if !ok {
		return nil, fmt.Errorf("model %q not found in provider %s catalog", model, p.providerID)
	}
	native, err := nativeProviderForEntry(entry, p.effectiveAPIKeys())
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.nativeCache[model] = native
	p.mu.Unlock()
	return native, nil
}
func selectModelEntry(providerID, model string, entries []ModelEntry) (ModelEntry, bool) {
	model = strings.TrimSpace(model)
	trimmed := strings.TrimPrefix(model, providerID+"/")
	for _, e := range entries {
		if e.ID == model || e.ID == trimmed || e.ProviderID+"/"+e.ID == model {
			return e, true
		}
	}
	if len(entries) == 1 {
		return entries[0], true
	}
	return ModelEntry{}, false
}
func nativeProviderForEntry(entry ModelEntry, apiKeys map[string]string) (agent.ChatProvider, error) {
	api := strings.ToLower(strings.TrimSpace(entry.API))
	apiKey := strings.TrimSpace(firstNonEmpty(stringValue(entry.Raw["apiKey"]), stringValue(entry.Raw["api_key"]), apiKeys[entry.ProviderID]))
	model := strings.TrimSpace(entry.ID)
	switch api {
	case "", "openai", "openai-completions", "openai-chat-completions", "openai-compatible":
		return &agent.OpenAIChatProviderChat{BaseURL: entry.BaseURL, APIKey: apiKey, Model: model}, nil
	case "anthropic", "anthropic-messages":
		opts := []agent.AnthropicChatOption{agent.WithModel(model)}
		if entry.BaseURL != "" {
			opts = append(opts, agent.WithBaseURL(entry.BaseURL))
		}
		return agent.NewAnthropicChatProvider(apiKey, opts...), nil
	case "google", "gemini", "google-gemini", "google-generative-ai":
		return &agent.GeminiChatProvider{APIKey: apiKey, Model: model}, nil
	default:
		return nil, fmt.Errorf("provider %s model %s uses unsupported catalog api %q", entry.ProviderID, entry.ID, entry.API)
	}
}
func isMissingProviderMethod(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unknown provider method") || strings.Contains(s, "not a function") || strings.Contains(s, "is not executable")
}

func TranslateMessagesToOpenClaw(messages []agent.LLMMessage) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		oc := map[string]any{"role": msg.Role}
		parts := make([]map[string]any, 0, 1+len(msg.Images)+len(msg.ToolCalls))
		if msg.Content != "" {
			parts = append(parts, map[string]any{"type": "text", "text": msg.Content})
		}
		for _, img := range msg.Images {
			part := map[string]any{"type": "image"}
			if img.URL != "" {
				part["source"] = map[string]any{"type": "url", "url": img.URL}
				part["url"] = img.URL
			} else if img.Base64 != "" {
				mt := firstNonEmpty(img.MimeType, "image/jpeg")
				part["source"] = map[string]any{"type": "base64", "media_type": mt, "data": img.Base64}
				part["media_type"] = mt
				part["data"] = img.Base64
			}
			parts = append(parts, part)
		}
		for _, tc := range msg.ToolCalls {
			parts = append(parts, map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": cloneMap(tc.Args)})
		}
		if msg.Role == "tool" {
			oc["tool_call_id"] = msg.ToolCallID
			oc["tool_use_id"] = msg.ToolCallID
			if len(parts) == 0 {
				parts = append(parts, map[string]any{"type": "tool_result", "tool_use_id": msg.ToolCallID, "content": msg.Content})
			} else {
				parts[0]["type"] = "tool_result"
				parts[0]["tool_use_id"] = msg.ToolCallID
				parts[0]["content"] = msg.Content
			}
		}
		if len(parts) == 1 && msg.Role != "assistant" && msg.Role != "tool" && len(msg.Images) == 0 {
			oc["content"] = msg.Content
		} else {
			oc["content"] = parts
		}
		out = append(out, oc)
	}
	return out
}

func TranslateToolsToOpenClaw(tools []agent.ToolDefinition) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		schema := cloneMap(tool.InputJSONSchema)
		if len(schema) == 0 {
			schema = toolParametersMap(tool.Parameters)
		}
		out = append(out, map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "input_schema": schema, "parameters": cloneMap(schema), "function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": cloneMap(schema)}})
	}
	return out
}
func toolParametersMap(params agent.ToolParameters) map[string]any {
	if params.Type == "" && len(params.Properties) == 0 && len(params.Required) == 0 {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	b, _ := json.Marshal(params)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if stringValue(m["type"]) == "" {
		m["type"] = "object"
	}
	return m
}

func TranslateResponseFromOpenClaw(result any) (*agent.LLMResponse, error) {
	data := asMap(result)
	if len(data) == 0 {
		if s, ok := result.(string); ok {
			return &agent.LLMResponse{Content: strings.TrimSpace(s)}, nil
		}
		return nil, fmt.Errorf("unexpected response type: %T", result)
	}
	resp := &agent.LLMResponse{}
	appendContentAndTools(resp, data)
	if choices, ok := data["choices"].([]any); ok && len(choices) > 0 {
		choice := asMap(choices[0])
		if msg := asMap(choice["message"]); len(msg) > 0 {
			appendContentAndTools(resp, msg)
		}
		if finish := stringValue(choice["finish_reason"]); finish != "" {
			resp.NeedsToolResults = finish == "tool_calls" || finish == "tool_use"
		}
	}
	if usage := asMap(firstPresent(data, "usage", "usageMetadata")); len(usage) > 0 {
		resp.Usage = parseUsage(usage)
	}
	if stop := strings.ToLower(firstNonEmpty(stringValue(data["stop_reason"]), stringValue(data["stopReason"]), stringValue(data["finish_reason"]), stringValue(data["finishReason"]))); stop != "" {
		resp.NeedsToolResults = resp.NeedsToolResults || stop == "tool_use" || stop == "tool_calls"
	}
	if len(resp.ToolCalls) > 0 && !hasExplicitNonToolStop(data) {
		resp.NeedsToolResults = true
	}
	resp.Content = strings.TrimSpace(resp.Content)
	return resp, nil
}
func hasExplicitNonToolStop(data map[string]any) bool {
	stop := strings.ToLower(firstNonEmpty(stringValue(data["stop_reason"]), stringValue(data["stopReason"]), stringValue(data["finish_reason"]), stringValue(data["finishReason"])))
	return stop != "" && stop != "tool_use" && stop != "tool_calls"
}
func appendContentAndTools(resp *agent.LLMResponse, data map[string]any) {
	if text := firstNonEmpty(stringValue(data["content"]), stringValue(data["text"]), stringValue(data["output_text"])); text != "" {
		resp.Content += text
	} else if content, ok := data["content"].([]any); ok {
		for _, part := range content {
			appendPart(resp, asMap(part))
		}
	}
	if content, ok := data["output"].([]any); ok {
		for _, part := range content {
			appendPart(resp, asMap(part))
		}
	}
	if calls, ok := firstPresent(data, "tool_calls", "toolCalls").([]any); ok {
		for _, raw := range calls {
			if tc, ok := parseToolCall(asMap(raw)); ok {
				resp.ToolCalls = append(resp.ToolCalls, tc)
			}
		}
	}
}
func appendPart(resp *agent.LLMResponse, part map[string]any) {
	switch strings.ToLower(firstNonEmpty(stringValue(part["type"]), stringValue(part["kind"]))) {
	case "text", "output_text", "message":
		resp.Content += stringValue(firstPresent(part, "text", "content"))
	case "tool_use", "tool_call", "function_call":
		if tc, ok := parseToolCall(part); ok {
			resp.ToolCalls = append(resp.ToolCalls, tc)
		}
	default:
		if text := stringValue(firstPresent(part, "text", "content")); text != "" {
			resp.Content += text
		}
	}
}
func parseToolCall(m map[string]any) (agent.ToolCall, bool) {
	if len(m) == 0 {
		return agent.ToolCall{}, false
	}
	fn := asMap(m["function"])
	id := firstNonEmpty(stringValue(m["id"]), stringValue(m["tool_call_id"]), stringValue(m["toolUseId"]))
	name := firstNonEmpty(stringValue(m["name"]), stringValue(fn["name"]))
	if name == "" {
		return agent.ToolCall{}, false
	}
	args := asMap(firstPresent(m, "input", "args", "arguments"))
	if len(args) == 0 {
		args = asMap(fn["arguments"])
		if len(args) == 0 {
			if s := stringValue(fn["arguments"]); s != "" {
				_ = json.Unmarshal([]byte(s), &args)
			}
		}
	}
	return agent.ToolCall{ID: id, Name: name, Args: args}, true
}
func parseUsage(usage map[string]any) agent.ProviderUsage {
	return agent.ProviderUsage{InputTokens: int64Value(firstPresent(usage, "input_tokens", "inputTokens", "prompt_tokens", "promptTokens", "promptTokenCount")), OutputTokens: int64Value(firstPresent(usage, "output_tokens", "outputTokens", "completion_tokens", "completionTokens", "candidatesTokenCount")), CacheReadTokens: int64Value(firstPresent(usage, "cache_read_input_tokens", "cacheReadInputTokens", "cached_tokens", "cachedTokens", "cachedContentTokenCount")), CacheCreationTokens: int64Value(firstPresent(usage, "cache_creation_input_tokens", "cacheCreationInputTokens", "cache_write_input_tokens", "cacheWriteInputTokens"))}
}
func translateStreamResult(result any) (*agent.LLMResponse, []string, error) {
	var chunks []string
	if arr, ok := result.([]any); ok {
		merged := &agent.LLMResponse{}
		for _, item := range arr {
			m := asMap(item)
			if text := firstNonEmpty(stringValue(m["delta"]), stringValue(m["text"]), stringValue(m["content"])); text != "" {
				chunks = append(chunks, text)
				merged.Content += text
				continue
			}
			resp, err := TranslateResponseFromOpenClaw(item)
			if err == nil {
				merged.Content += resp.Content
				merged.ToolCalls = append(merged.ToolCalls, resp.ToolCalls...)
				merged.Usage.InputTokens += resp.Usage.InputTokens
				merged.Usage.OutputTokens += resp.Usage.OutputTokens
				merged.Usage.CacheReadTokens += resp.Usage.CacheReadTokens
				merged.Usage.CacheCreationTokens += resp.Usage.CacheCreationTokens
				if resp.Content != "" {
					chunks = append(chunks, resp.Content)
				}
			}
		}
		return merged, chunks, nil
	}
	resp, err := TranslateResponseFromOpenClaw(result)
	if err != nil {
		return nil, nil, err
	}
	if resp.Content != "" {
		chunks = append(chunks, resp.Content)
	}
	return resp, chunks, nil
}

func ParseCatalogResult(defaultProviderID string, result any) ([]ModelEntry, error) {
	root := asMap(result)
	if len(root) == 0 {
		return nil, nil
	}
	var entries []ModelEntry
	if _, hasModels := root["models"]; hasModels {
		entries = append(entries, entriesFromProviderConfig(defaultProviderID, root)...)
	}
	if provider := asMap(root["provider"]); len(provider) > 0 {
		entries = append(entries, entriesFromProviderConfig(defaultProviderID, provider)...)
	}
	if providers := asMap(root["providers"]); len(providers) > 0 {
		keys := make([]string, 0, len(providers))
		for k := range providers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, id := range keys {
			entries = append(entries, entriesFromProviderConfig(id, asMap(providers[id]))...)
		}
	}
	return entries, nil
}
func entriesFromProviderConfig(providerID string, cfg map[string]any) []ModelEntry {
	models, _ := cfg["models"].([]any)
	out := make([]ModelEntry, 0, len(models))
	for _, raw := range models {
		model := asMap(raw)
		if len(model) == 0 {
			if id := stringValue(raw); id != "" {
				model = map[string]any{"id": id, "name": id}
			}
		}
		id := stringValue(model["id"])
		if id == "" {
			continue
		}
		e := ModelEntry{ID: id, Name: stringValue(model["name"]), ProviderID: providerID, API: firstNonEmpty(stringValue(model["api"]), stringValue(cfg["api"])), BaseURL: firstNonEmpty(stringValue(model["baseUrl"]), stringValue(model["base_url"]), stringValue(cfg["baseUrl"]), stringValue(cfg["base_url"])), ContextWindow: intValue(firstPresent(model, "contextWindow", "context_window", "contextTokens")), MaxTokens: intValue(firstPresent(model, "maxTokens", "max_tokens")), Input: stringSliceFromAny(model["input"]), Reasoning: boolValue(model["reasoning"]), Cost: asMap(model["cost"]), Raw: cloneMap(model)}
		if e.Raw == nil {
			e.Raw = map[string]any{}
		}
		for _, key := range []string{"apiKey", "api_key", "headers"} {
			if v, ok := cfg[key]; ok {
				e.Raw[key] = v
			}
		}
		out = append(out, e)
	}
	return out
}

func cloneProviderMetadata(meta *registry.RegisteredProvider) *registry.RegisteredProvider {
	if meta == nil {
		return nil
	}
	cp := *meta
	cp.Raw = cloneMap(meta.Raw)
	return &cp
}
func cleanMethods(methods []string) []string {
	out := make([]string, 0, len(methods))
	seen := map[string]struct{}{}
	for _, m := range methods {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if _, ok := seen[m]; !ok {
			seen[m] = struct{}{}
			out = append(out, m)
		}
	}
	return out
}
func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}
func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneAny(v)
	}
	return out
}
func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func cloneAny(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneMap(t)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = cloneAny(item)
		}
		return out
	default:
		return t
	}
}
func asMap(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		return t
	case map[string]string:
		out := make(map[string]any, len(t))
		for k, v := range t {
			out[k] = v
		}
		return out
	case string:
		var m map[string]any
		if json.Unmarshal([]byte(t), &m) == nil {
			return m
		}
	}
	return nil
}
func firstPresent(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
func stringValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return ""
	}
}
func int64Value(v any) int64 {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int64:
		return t
	case int32:
		return int64(t)
	case float64:
		return int64(t)
	case float32:
		return int64(t)
	case json.Number:
		n, _ := t.Int64()
		return n
	}
	return 0
}
func intValue(v any) int { return int(int64Value(v)) }
func boolValue(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(t, "true") || t == "1"
	default:
		return false
	}
}
func stringSliceFromAny(v any) []string {
	switch t := v.(type) {
	case []string:
		return append([]string(nil), t...)
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s := stringValue(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	default:
		return nil
	}
}
