package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	jsonschema "github.com/google/jsonschema-go/jsonschema"
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

// ToolSemanticValidator is the Go analogue of src validateInput. It runs after
// schema validation and before pre-execute hooks.
type ToolSemanticValidator func(ctx context.Context, call ToolCall, desc ToolDescriptor) error

// ToolTraitResolvers provide input-aware runtime trait resolution matching the
// canonical src callable trait methods.
type ToolTraitResolvers struct {
	IsConcurrencySafe func(args map[string]any) bool
	IsReadOnly        func(args map[string]any) bool
	IsDestructive     func(args map[string]any) bool
	InterruptBehavior func() ToolInterruptBehavior
}

// ToolRegistration is the additive registration surface for tools that need
// validators or runtime trait resolvers in addition to static descriptors.
type ToolRegistration struct {
	Func            ToolFunc
	Descriptor      ToolDescriptor
	ProviderVisible bool
	Validate        ToolSemanticValidator
	Traits          ToolTraitResolvers
}

// ToolExecutionPhase identifies which execution phase failed.
type ToolExecutionPhase string

const (
	ToolExecutionPhaseSchemaValidation   ToolExecutionPhase = "schema_validation"
	ToolExecutionPhaseSemanticValidation ToolExecutionPhase = "semantic_validation"
	ToolExecutionPhasePreExecute         ToolExecutionPhase = "pre_execute"
	ToolExecutionPhaseExecute            ToolExecutionPhase = "execute"
	ToolExecutionPhasePostExecute        ToolExecutionPhase = "post_execute"
)

// ToolExecutionError preserves which phase failed while keeping Execute's
// existing string,error contract unchanged.
type ToolExecutionError struct {
	ToolName string
	Phase    ToolExecutionPhase
	Cause    error
}

func (e *ToolExecutionError) Error() string {
	if e == nil || e.Cause == nil {
		return ""
	}
	return e.Cause.Error()
}

func (e *ToolExecutionError) Unwrap() error { return e.Cause }

// ToolPreExecuteHook runs after validation but before execution. It may reject
// the call or rewrite the ToolCall passed to the execute phase.
type ToolPreExecuteHook func(ctx context.Context, call ToolCall, desc ToolDescriptor) (ToolCall, error)

// ToolPostExecuteHook runs after successful execution and may rewrite the raw
// tool result before it is returned.
type ToolPostExecuteHook func(ctx context.Context, call ToolCall, desc ToolDescriptor, result string) (string, error)

// ToolExecuteErrorHook observes execute/post-execute failures and may replace
// the error returned to the caller.
type ToolExecuteErrorHook func(ctx context.Context, call ToolCall, desc ToolDescriptor, err error) error

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

// ToolMiddleware is the legacy execute-phase wrapper. It now wraps only the raw
// execute phase, not schema validation or pre/post hooks.
type ToolMiddleware func(ctx context.Context, call ToolCall, next func(context.Context, ToolCall) (string, error)) (string, error)

type toolEntry struct {
	fn              ToolFunc
	descriptor      ToolDescriptor
	providerVisible bool
	validate        ToolSemanticValidator
	traits          ToolTraitResolvers
	schemaOnce      sync.Once
	resolvedSchema  *jsonschema.Resolved
	schemaErr       error
}

type ToolRegistry struct {
	entries    map[string]*toolEntry
	middleware ToolMiddleware
	preHooks   []ToolPreExecuteHook
	postHooks  []ToolPostExecuteHook
	errorHooks []ToolExecuteErrorHook
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{entries: map[string]*toolEntry{}}
}

// SetMiddleware installs a middleware that wraps the execute phase of every
// Execute call. Calling SetMiddleware again replaces the previous middleware.
// Pass nil to remove it.
func (r *ToolRegistry) SetMiddleware(mw ToolMiddleware) {
	r.middleware = mw
}

// AddPreExecuteHook appends a pre-execute hook to the registry pipeline.
func (r *ToolRegistry) AddPreExecuteHook(h ToolPreExecuteHook) {
	if h != nil {
		r.preHooks = append(r.preHooks, h)
	}
}

// AddPostExecuteHook appends a post-execute hook to the registry pipeline.
func (r *ToolRegistry) AddPostExecuteHook(h ToolPostExecuteHook) {
	if h != nil {
		r.postHooks = append(r.postHooks, h)
	}
}

// AddExecuteErrorHook appends an execute-error hook to the registry pipeline.
func (r *ToolRegistry) AddExecuteErrorHook(h ToolExecuteErrorHook) {
	if h != nil {
		r.errorHooks = append(r.errorHooks, h)
	}
}

func (r *ToolRegistry) Register(name string, fn ToolFunc) {
	r.RegisterTool(name, ToolRegistration{
		Func: fn,
		Descriptor: ToolDescriptor{
			Name:   name,
			Origin: ToolOrigin{Kind: ToolOriginKindBuiltin},
		},
		ProviderVisible: false,
	})
}

// RegisterWithDef registers a tool alongside its ToolDefinition for provider-side
// native function-calling.  If def.Name is empty it is set to name.
func (r *ToolRegistry) RegisterWithDef(name string, fn ToolFunc, def ToolDefinition) {
	r.RegisterTool(name, ToolRegistration{
		Func:            fn,
		Descriptor:      descriptorFromDefinition(name, def),
		ProviderVisible: true,
	})
}

// RegisterWithDescriptor registers a tool alongside its canonical descriptor.
func (r *ToolRegistry) RegisterWithDescriptor(name string, fn ToolFunc, desc ToolDescriptor) {
	r.RegisterTool(name, ToolRegistration{
		Func:            fn,
		Descriptor:      desc,
		ProviderVisible: true,
	})
}

// RegisterTool is the canonical additive registration path for executable tools.
func (r *ToolRegistry) RegisterTool(name string, reg ToolRegistration) {
	if name == "" {
		return
	}
	entry := r.entry(name)
	if reg.Func != nil {
		entry.fn = reg.Func
	}
	entry.descriptor = normalizeToolDescriptor(name, reg.Descriptor)
	entry.providerVisible = entry.providerVisible || reg.ProviderVisible
	if reg.Validate != nil {
		entry.validate = reg.Validate
	}
	if hasTraitResolvers(reg.Traits) {
		entry.traits = reg.Traits
	}
	entry.resetSchemaCache()
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
	if name == "" {
		return
	}
	entry := r.entry(name)
	entry.descriptor = normalizeToolDescriptor(name, desc)
	entry.providerVisible = true
	entry.resetSchemaCache()
}

// Definitions returns the provider-visible ToolDefinitions, sorted by name for
// deterministic ordering.
func (r *ToolRegistry) Definitions() []ToolDefinition {
	out := make([]ToolDefinition, 0, len(r.entries))
	for _, entry := range r.entries {
		if entry == nil || !entry.providerVisible {
			continue
		}
		out = append(out, entry.descriptor.Definition())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Descriptor returns the canonical descriptor for a registered tool.
func (r *ToolRegistry) Descriptor(name string) (ToolDescriptor, bool) {
	entry, ok := r.entries[name]
	if !ok || entry == nil {
		return ToolDescriptor{}, false
	}
	return entry.descriptor, true
}

// Descriptors returns all canonical tool descriptors sorted by name.
func (r *ToolRegistry) Descriptors() []ToolDescriptor {
	out := make([]ToolDescriptor, 0, len(r.entries))
	for _, entry := range r.entries {
		if entry == nil {
			continue
		}
		out = append(out, entry.descriptor)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// EffectiveTraits resolves the runtime-effective traits for a call, applying any
// input-aware trait resolvers on top of the descriptor defaults.
func (r *ToolRegistry) EffectiveTraits(call ToolCall) (traits ToolTraits, ok bool) {
	entry, ok := r.entries[call.Name]
	if !ok || entry == nil {
		return ToolTraits{}, false
	}
	prepared := normalizeToolCall(call)
	traits = normalizeToolDescriptor(call.Name, entry.descriptor).Traits
	validatedArgs, err := entry.validatedArgs(prepared.Args)
	if err != nil {
		traits.ConcurrencySafe = false
		return traits, true
	}
	prepared.Args = validatedArgs
	defer func() {
		if recover() != nil {
			traits.ConcurrencySafe = false
			ok = true
		}
	}()
	if entry.traits.IsConcurrencySafe != nil {
		traits.ConcurrencySafe = entry.traits.IsConcurrencySafe(prepared.Args)
	}
	if entry.traits.IsReadOnly != nil {
		traits.ReadOnly = entry.traits.IsReadOnly(prepared.Args)
	}
	if entry.traits.IsDestructive != nil {
		traits.Destructive = entry.traits.IsDestructive(prepared.Args)
	}
	if entry.traits.InterruptBehavior != nil {
		if behavior := entry.traits.InterruptBehavior(); behavior != "" {
			traits.InterruptBehavior = behavior
		}
	}
	return traits, true
}

func (r *ToolRegistry) Execute(ctx context.Context, call ToolCall) (string, error) {
	entry, ok := r.entries[call.Name]
	if !ok || entry == nil || entry.fn == nil {
		return "", fmt.Errorf("unknown tool %q", call.Name)
	}

	prepared := normalizeToolCall(call)
	desc := normalizeToolDescriptor(call.Name, entry.descriptor)

	validatedArgs, err := entry.validatedArgs(prepared.Args)
	if err != nil {
		return "", newToolExecutionError(call.Name, ToolExecutionPhaseSchemaValidation, err)
	}
	prepared.Args = validatedArgs
	if entry.validate != nil {
		if err := entry.validate(ctx, prepared, desc); err != nil {
			return "", newToolExecutionError(call.Name, ToolExecutionPhaseSemanticValidation, err)
		}
	}
	for _, hook := range r.preHooks {
		nextCall, err := hook(ctx, prepared, desc)
		if err != nil {
			return "", newToolExecutionError(call.Name, ToolExecutionPhasePreExecute, err)
		}
		prepared = normalizeToolCall(nextCall)
		if prepared.Name == "" {
			prepared.Name = call.Name
		}
	}

	rawExec := func(ctx context.Context, c ToolCall) (string, error) {
		return entry.fn(ctx, c.Args)
	}

	result, err := r.executePhase(ctx, prepared, rawExec)
	if err != nil {
		return "", r.runErrorHooks(ctx, prepared, desc, ToolExecutionPhaseExecute, err)
	}
	for _, hook := range r.postHooks {
		result, err = hook(ctx, prepared, desc, result)
		if err != nil {
			return "", r.runErrorHooks(ctx, prepared, desc, ToolExecutionPhasePostExecute, err)
		}
	}
	return result, nil
}

func (r *ToolRegistry) List() []string {
	out := make([]string, 0, len(r.entries))
	for name, entry := range r.entries {
		if entry != nil && entry.fn != nil {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func (r *ToolRegistry) executePhase(ctx context.Context, call ToolCall, rawExec func(context.Context, ToolCall) (string, error)) (string, error) {
	if r.middleware != nil {
		return r.middleware(ctx, call, rawExec)
	}
	return rawExec(ctx, call)
}

func (r *ToolRegistry) runErrorHooks(ctx context.Context, call ToolCall, desc ToolDescriptor, phase ToolExecutionPhase, err error) error {
	wrapped := newToolExecutionError(call.Name, phase, err)
	for _, hook := range r.errorHooks {
		next := hook(ctx, call, desc, wrapped)
		if next == nil {
			continue
		}
		if _, ok := next.(*ToolExecutionError); ok {
			wrapped = next
			continue
		}
		wrapped = newToolExecutionError(call.Name, phase, next)
	}
	return wrapped
}

func (r *ToolRegistry) entry(name string) *toolEntry {
	if r.entries == nil {
		r.entries = map[string]*toolEntry{}
	}
	if entry, ok := r.entries[name]; ok && entry != nil {
		return entry
	}
	entry := &toolEntry{descriptor: normalizeToolDescriptor(name, ToolDescriptor{Name: name, Origin: ToolOrigin{Kind: ToolOriginKindUnknown}})}
	r.entries[name] = entry
	return entry
}

func (e *toolEntry) resetSchemaCache() {
	e.schemaOnce = sync.Once{}
	e.resolvedSchema = nil
	e.schemaErr = nil
}

func (e *toolEntry) validatedArgs(args map[string]any) (map[string]any, error) {
	prepared := normalizeToolArgs(args)
	resolved, err := e.validationSchema()
	if err != nil || resolved == nil {
		return prepared, err
	}
	if err := resolved.Validate(prepared); err != nil {
		return nil, err
	}
	return prepared, nil
}

func (e *toolEntry) validationSchema() (*jsonschema.Resolved, error) {
	desc := normalizeToolDescriptor(e.descriptor.Name, e.descriptor)
	if !descriptorHasValidationSchema(desc) {
		return nil, nil
	}
	e.schemaOnce.Do(func() {
		schemaMap := toolInputSchemaMap(desc.Definition())
		b, err := json.Marshal(schemaMap)
		if err != nil {
			e.schemaErr = err
			return
		}
		var schema jsonschema.Schema
		if err := json.Unmarshal(b, &schema); err != nil {
			e.schemaErr = err
			return
		}
		e.resolvedSchema, e.schemaErr = schema.Resolve(nil)
	})
	return e.resolvedSchema, e.schemaErr
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
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
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

func descriptorHasValidationSchema(desc ToolDescriptor) bool {
	return len(desc.InputJSONSchema) > 0 || desc.Parameters.Type != "" || len(desc.Parameters.Properties) > 0 || len(desc.Parameters.Required) > 0
}

func hasTraitResolvers(traits ToolTraitResolvers) bool {
	return traits.IsConcurrencySafe != nil || traits.IsReadOnly != nil || traits.IsDestructive != nil || traits.InterruptBehavior != nil
}

func normalizeToolCall(call ToolCall) ToolCall {
	call.Name = strings.TrimSpace(call.Name)
	call.Args = normalizeToolArgs(call.Args)
	return call
}

func normalizeToolArgs(args map[string]any) map[string]any {
	if args == nil {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(args))
	for k, v := range args {
		cloned[k] = v
	}
	return cloned
}

func newToolExecutionError(name string, phase ToolExecutionPhase, err error) error {
	if err == nil {
		return nil
	}
	if existing, ok := err.(*ToolExecutionError); ok {
		return existing
	}
	return &ToolExecutionError{ToolName: name, Phase: phase, Cause: err}
}
