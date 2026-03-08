package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

type ToolCall struct {
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
	tools      map[string]ToolFunc
	middleware ToolMiddleware
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: map[string]ToolFunc{}}
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
