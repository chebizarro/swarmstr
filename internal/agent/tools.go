package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
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
	// Parameters is a JSON Schema object describing the tool's input.
	Parameters ToolParameters `json:"input_schema,omitempty"`
}

// ToolParameters is a JSON Schema object for a tool's input.
type ToolParameters struct {
	Type       string                    `json:"type"`
	Properties map[string]ToolParamProp  `json:"properties,omitempty"`
	Required   []string                  `json:"required,omitempty"`
}

// ToolParamProp describes a single parameter property.
type ToolParamProp struct {
	Type        string      `json:"type"`
	Description string      `json:"description,omitempty"`
	Enum        []string    `json:"enum,omitempty"`
	Items       *ToolParamProp `json:"items,omitempty"`   // for array types
	Default     interface{} `json:"default,omitempty"`
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

// ToolMiddleware is a function that wraps a tool execution.  It receives the
// tool call and a next function that performs the actual execution.  The
// middleware can inspect or modify the call, block execution (by returning an
// error without calling next), or post-process the result.
type ToolMiddleware func(ctx context.Context, call ToolCall, next func(context.Context, ToolCall) (string, error)) (string, error)

type ToolRegistry struct {
	tools       map[string]ToolFunc
	middleware  ToolMiddleware
	definitions map[string]ToolDefinition
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools:       map[string]ToolFunc{},
		definitions: map[string]ToolDefinition{},
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
	if name == "" || fn == nil {
		return
	}
	if def.Name == "" {
		def.Name = name
	}
	r.tools[name] = fn
	r.definitions[name] = def
}

// SetDefinition attaches or updates a ToolDefinition for an already-registered
// tool.  Useful when tools are registered via Register() and descriptions are
// added later.
func (r *ToolRegistry) SetDefinition(name string, def ToolDefinition) {
	if def.Name == "" {
		def.Name = name
	}
	r.definitions[name] = def
}

// Definitions returns the ToolDefinitions for all tools that have them,
// sorted by name for deterministic ordering.
func (r *ToolRegistry) Definitions() []ToolDefinition {
	out := make([]ToolDefinition, 0, len(r.definitions))
	for _, def := range r.definitions {
		out = append(out, def)
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
