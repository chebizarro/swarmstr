package agent

import (
	"context"
	"encoding/json"
	"fmt"
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

type ToolRegistry struct {
	tools map[string]ToolFunc
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: map[string]ToolFunc{}}
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
	return fn(ctx, call.Args)
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
