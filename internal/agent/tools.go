package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ─── Tool definition types ────────────────────────────────────────────────────

// ToolDefinition describes a tool to the LLM provider via its native API.
// Providers (Anthropic, OpenAI, Gemini) use these to generate proper function-call
// responses rather than relying on system-prompt text alone.
type ToolDefinition struct {
	// Name is the unique tool identifier (snake_case, matches ToolRegistry key).
	Name string `json:"name"`
	// Description explains what the tool does. Good descriptions dramatically
	// improve how reliably the model chooses and parameterises the tool.
	Description string `json:"description"`
	// Parameters is the normalized JSON Schema object describing the tool's input.
	// Built-in tools generally use this path.
	Parameters ToolParameters `json:"input_schema,omitempty"`
	// InputJSONSchema preserves richer raw JSON Schema when the canonical source
	// defines it directly (matching src inputJSONSchema semantics for MCP/plugin tools).
	InputJSONSchema map[string]any `json:"-"`
}

// ToolParameters is a JSON Schema object for a tool's input.
type ToolParameters struct {
	Type       string                   `json:"type"`
	Properties map[string]ToolParamProp `json:"properties,omitempty"`
	Required   []string                 `json:"required,omitempty"`
}

// ToolParamProp describes a single parameter property.
type ToolParamProp struct {
	Type        string         `json:"type"`
	Description string         `json:"description,omitempty"`
	Enum        []string       `json:"enum,omitempty"`
	Items       *ToolParamProp `json:"items,omitempty"` // for array types
	Default     interface{}    `json:"default,omitempty"`
}

// ToolOriginKind identifies where a tool came from.
// Mirrors the provenance split used in the canonical src tool pool assembly.
type ToolOriginKind string

const (
	ToolOriginKindUnknown ToolOriginKind = "unknown"
	ToolOriginKindBuiltin ToolOriginKind = "builtin"
	ToolOriginKindPlugin  ToolOriginKind = "plugin"
	ToolOriginKindMCP     ToolOriginKind = "mcp"
)

// ToolInterruptBehavior describes what should happen when a new user message
// arrives while a tool is still running. The canonical src default is "block".
type ToolInterruptBehavior string

const (
	ToolInterruptBehaviorBlock  ToolInterruptBehavior = "block"
	ToolInterruptBehaviorCancel ToolInterruptBehavior = "cancel"
)

// ToolOrigin captures the descriptor provenance for a registered tool.
type ToolOrigin struct {
	Kind          ToolOriginKind `json:"kind,omitempty"`
	PluginID      string         `json:"plugin_id,omitempty"`
	ServerName    string         `json:"server_name,omitempty"`
	CanonicalName string         `json:"canonical_name,omitempty"`
}

// ToolTraits captures runtime traits ported from the canonical src Tool type.
// Defaults fail closed to match src/Tool.ts TOOL_DEFAULTS:
// concurrency safe=false, read only=false, destructive=false, interrupt=block.
type ToolTraits struct {
	ConcurrencySafe   bool                  `json:"concurrency_safe,omitempty"`
	ReadOnly          bool                  `json:"read_only,omitempty"`
	Destructive       bool                  `json:"destructive,omitempty"`
	InterruptBehavior ToolInterruptBehavior `json:"interrupt_behavior,omitempty"`
}

// ToolDescriptor is the canonical metadata contract for registered tools.
// Provider-facing ToolDefinition values are projected from descriptors.
type ToolDescriptor struct {
	Name            string         `json:"name"`
	Description     string         `json:"description,omitempty"`
	Parameters      ToolParameters `json:"input_schema,omitempty"`
	InputJSONSchema map[string]any `json:"-"`
	Origin          ToolOrigin     `json:"origin,omitempty"`
	Traits          ToolTraits     `json:"traits,omitempty"`
}

// Definition projects a provider-facing ToolDefinition from the canonical
// descriptor contract.
func (d ToolDescriptor) Definition() ToolDefinition {
	d = normalizeToolDescriptor(d.Name, d)
	return ToolDefinition{
		Name:            d.Name,
		Description:     d.Description,
		Parameters:      d.Parameters,
		InputJSONSchema: cloneJSONMap(d.InputJSONSchema),
	}
}

type ToolCall struct {
	ID   string         `json:"id,omitempty"` // provider-assigned call ID for tool_result linking
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type ToolTrace struct {
	Call   ToolCall `json:"call"`
	Result string   `json:"result,omitempty"`
	Error  string   `json:"error,omitempty"`
}

type ToolExecutor interface {
	Execute(context.Context, ToolCall) (string, error)
}

type ToolFunc func(context.Context, map[string]any) (string, error)

// sessionIDKey is the context key for the current session ID.
type sessionIDKey struct{}

// ContextWithSessionID returns a child context carrying the session ID.
func ContextWithSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sessionIDKey{}, id)
}

// SessionIDFromContext extracts the session ID injected by the runtime.
// Returns "" if no session ID is set.
func SessionIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(sessionIDKey{}).(string); ok {
		return v
	}
	return ""
}

// ResolveSessionID returns the explicit arg value if non-empty, otherwise
// falls back to the session ID from context.  Tools should prefer this over
// reading args["session_id"] directly — it lets the runtime provide the
// default while still allowing explicit cross-session overrides.
func ResolveSessionID(ctx context.Context, args map[string]any) string {
	if v := ArgString(args, "session_id"); v != "" {
		return v
	}
	return SessionIDFromContext(ctx)
}

// ResolveSessionIDStrict resolves session_id with strict type validation.
// If args includes session_id, it must be a string (empty string falls back
// to context). If args omits session_id, context value is used.
func ResolveSessionIDStrict(ctx context.Context, args map[string]any) (string, error) {
	if args != nil {
		if raw, exists := args["session_id"]; exists {
			s, ok := raw.(string)
			if !ok {
				return "", fmt.Errorf("session_id must be a string")
			}
			s = strings.TrimSpace(s)
			if s != "" {
				return s, nil
			}
		}
	}
	return SessionIDFromContext(ctx), nil
}

// ToolMiddleware is a function that wraps a tool execution.  It receives the
// tool call and a next function that performs the actual execution.  The
// middleware can inspect or modify the call, block execution (by returning an
// error without calling next), or post-process the result.
type ToolMiddleware func(ctx context.Context, call ToolCall, next func(context.Context, ToolCall) (string, error)) (string, error)

type ToolRegistry struct {
	tools       map[string]ToolFunc
	middleware  ToolMiddleware
	descriptors map[string]ToolDescriptor
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools:       map[string]ToolFunc{},
		descriptors: map[string]ToolDescriptor{},
	}
}

// SetMiddleware installs a middleware that wraps every Execute call.  Only one
// middleware is supported; calling SetMiddleware again replaces the previous
// one.  Pass nil to remove the middleware.
func (r *ToolRegistry) SetMiddleware(mw ToolMiddleware) {
	r.middleware = mw
}

func (r *ToolRegistry) Register(name string, fn ToolFunc) {
	if name == "" || fn == nil {
		return
	}
	r.tools[name] = fn
}

// RegisterWithDef registers a tool alongside its ToolDefinition for provider-side
// native function-calling.  If def.Name is empty it is set to name.
func (r *ToolRegistry) RegisterWithDef(name string, fn ToolFunc, def ToolDefinition) {
	r.RegisterWithDescriptor(name, fn, descriptorFromDefinition(name, def))
}

// RegisterWithDescriptor registers a tool alongside its canonical descriptor.
func (r *ToolRegistry) RegisterWithDescriptor(name string, fn ToolFunc, desc ToolDescriptor) {
	if name == "" || fn == nil {
		return
	}
	r.tools[name] = fn
	r.descriptors[name] = normalizeToolDescriptor(name, desc)
}

// SetDefinition attaches or updates a ToolDefinition for an already-registered
// tool.  Useful when tools are registered via Register() and descriptions are
// added later.
func (r *ToolRegistry) SetDefinition(name string, def ToolDefinition) {
	r.SetDescriptor(name, descriptorFromDefinition(name, def))
}

// SetDescriptor attaches or updates a ToolDescriptor for an already-registered
// tool. Useful when execution and metadata are wired in separate phases.
func (r *ToolRegistry) SetDescriptor(name string, desc ToolDescriptor) {
	r.descriptors[name] = normalizeToolDescriptor(name, desc)
}

// Definitions returns the ToolDefinitions for all tools that have them,
// sorted by name for deterministic ordering.
func (r *ToolRegistry) Definitions() []ToolDefinition {
	out := make([]ToolDefinition, 0, len(r.descriptors))
	for _, desc := range r.descriptors {
		out = append(out, desc.Definition())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Descriptor returns the canonical descriptor for a registered tool.
func (r *ToolRegistry) Descriptor(name string) (ToolDescriptor, bool) {
	desc, ok := r.descriptors[name]
	return desc, ok
}

// Descriptors returns all canonical tool descriptors sorted by name.
func (r *ToolRegistry) Descriptors() []ToolDescriptor {
	out := make([]ToolDescriptor, 0, len(r.descriptors))
	for _, desc := range r.descriptors {
		out = append(out, desc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *ToolRegistry) Execute(ctx context.Context, call ToolCall) (string, error) {
	fn, ok := r.tools[call.Name]
	if !ok {
		return "", fmt.Errorf("unknown tool %q", call.Name)
	}
	rawExec := func(ctx context.Context, c ToolCall) (string, error) {
		f, ok := r.tools[c.Name]
		if !ok {
			return "", fmt.Errorf("unknown tool %q", c.Name)
		}
		return f(ctx, c.Args)
	}
	if r.middleware != nil {
		return r.middleware(ctx, call, rawExec)
	}
	return fn(ctx, call.Args)
}

func (r *ToolRegistry) List() []string {
	out := make([]string, 0, len(r.tools))
	for name := range r.tools {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func ArgString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	if v, ok := args[key]; ok {
		switch t := v.(type) {
		case string:
			return t
		default:
			b, _ := json.Marshal(v)
			return string(b)
		}
	}
	return ""
}

func ArgInt(args map[string]any, key string, def int) int {
	if args == nil {
		return def
	}
	v, ok := args[key]
	if !ok {
		return def
	}
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		i, err := strconv.Atoi(t)
		if err == nil {
			return i
		}
	}
	return def
}

func normalizeToolDescriptor(name string, desc ToolDescriptor) ToolDescriptor {
	if desc.Name == "" {
		desc.Name = name
	}
	if desc.Origin.Kind == "" {
		desc.Origin.Kind = ToolOriginKindUnknown
	}
	if desc.Traits.InterruptBehavior == "" {
		desc.Traits.InterruptBehavior = ToolInterruptBehaviorBlock
	}
	return desc
}

func descriptorFromDefinition(name string, def ToolDefinition) ToolDescriptor {
	if def.Name == "" {
		def.Name = name
	}
	return normalizeToolDescriptor(name, ToolDescriptor{
		Name:            def.Name,
		Description:     def.Description,
		Parameters:      def.Parameters,
		InputJSONSchema: cloneJSONMap(def.InputJSONSchema),
		Origin:          ToolOrigin{Kind: ToolOriginKindBuiltin},
	})
}

func toolInputSchemaMap(d ToolDefinition) map[string]any {
	if len(d.InputJSONSchema) > 0 {
		schema := cloneJSONMap(d.InputJSONSchema)
		if schema == nil {
			schema = map[string]any{}
		}
		if _, ok := schema["type"]; !ok {
			schema["type"] = "object"
		}
		if _, ok := schema["properties"]; !ok {
			schema["properties"] = map[string]any{}
		}
		return schema
	}

	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	props := schema["properties"].(map[string]any)
	for k, v := range d.Parameters.Properties {
		props[k] = toolParamPropSchemaMap(v)
	}
	if len(d.Parameters.Required) > 0 {
		required := make([]string, len(d.Parameters.Required))
		copy(required, d.Parameters.Required)
		schema["required"] = required
	}
	return schema
}

func toolParamPropSchemaMap(v ToolParamProp) map[string]any {
	prop := map[string]any{"type": v.Type}
	if v.Description != "" {
		prop["description"] = v.Description
	}
	if len(v.Enum) > 0 {
		prop["enum"] = append([]string(nil), v.Enum...)
	}
	if v.Items != nil {
		prop["items"] = toolParamPropSchemaMap(*v.Items)
	}
	if v.Default != nil {
		prop["default"] = v.Default
	}
	return prop
}

func cloneJSONMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	b, err := json.Marshal(src)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}
