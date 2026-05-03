package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"metiq/internal/plugins/registry"
	"metiq/internal/plugins/runtime"
)

const DefaultHookTimeout = 5 * time.Second

type NodeHookCaller interface {
	InvokeHookHandler(ctx context.Context, event, hookID string, payload any) (runtime.HookResult, error)
}

type NativeHandler func(ctx context.Context, payload any) (any, error)

type HookInvoker struct {
	registry *registry.HookRegistry
	host     NodeHookCaller

	nativeMu       sync.RWMutex
	nativeHandlers map[registry.HookEvent][]nativeRegistration
	nextNativeID   int64
}

type nativeRegistration struct {
	ID, PluginID string
	Event        registry.HookEvent
	Priority     int
	Handler      NativeHandler
}

type EmitOptions struct {
	StopOnMutation, StopOnReject, StopOnError bool
	Timeout, HandlerTimeout                   time.Duration
}

type HandlerResult struct {
	PluginID  string        `json:"plugin_id,omitempty"`
	HookID    string        `json:"hook_id,omitempty"`
	Source    string        `json:"source,omitempty"`
	Result    any           `json:"result,omitempty"`
	Error     error         `json:"-"`
	ErrorText string        `json:"error,omitempty"`
	Duration  time.Duration `json:"duration,omitempty"`
}

type EmitResult struct {
	Results      []HandlerResult  `json:"results,omitempty"`
	Mutations    []map[string]any `json:"mutations,omitempty"`
	Mutation     map[string]any   `json:"mutation,omitempty"`
	Rejected     bool             `json:"rejected,omitempty"`
	RejectReason string           `json:"reject_reason,omitempty"`
	Error        error            `json:"-"`
}

func NewHookInvoker(reg *registry.HookRegistry, host NodeHookCaller) *HookInvoker {
	if reg == nil {
		reg = registry.NewHookRegistry()
	}
	return &HookInvoker{registry: reg, host: host, nativeHandlers: map[registry.HookEvent][]nativeRegistration{}}
}

func (i *HookInvoker) RegisterNative(event registry.HookEvent, hookID string, priority int, handler NativeHandler) string {
	if i == nil || handler == nil {
		return ""
	}
	i.nativeMu.Lock()
	defer i.nativeMu.Unlock()
	if strings.TrimSpace(hookID) == "" {
		i.nextNativeID++
		hookID = fmt.Sprintf("native:%s:%d", event, i.nextNativeID)
	}
	reg := nativeRegistration{ID: hookID, PluginID: "native", Event: event, Priority: priority, Handler: handler}
	i.nativeHandlers[event] = append(i.nativeHandlers[event], reg)
	sort.SliceStable(i.nativeHandlers[event], func(a, b int) bool {
		l, r := i.nativeHandlers[event][a], i.nativeHandlers[event][b]
		if l.Priority == r.Priority {
			return l.ID < r.ID
		}
		return l.Priority < r.Priority
	})
	return hookID
}

func (i *HookInvoker) Emit(ctx context.Context, event registry.HookEvent, payload any, opts EmitOptions) (*EmitResult, error) {
	if i == nil {
		return &EmitResult{}, nil
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	if opts.HandlerTimeout <= 0 {
		opts.HandlerTimeout = DefaultHookTimeout
	}
	out := &EmitResult{Mutation: map[string]any{}}
	for _, entry := range i.orderedEntries(event) {
		select {
		case <-ctx.Done():
			if out.Error == nil {
				out.Error = ctx.Err()
			}
			return out, ctx.Err()
		default:
		}
		start := time.Now()
		res, err := i.invokeOne(ctx, entry, event, payload, opts.HandlerTimeout)
		hr := HandlerResult{PluginID: entry.pluginID, HookID: entry.id, Source: string(entry.source), Result: res, Error: err, Duration: time.Since(start)}
		if err != nil {
			hr.ErrorText = err.Error()
			if out.Error == nil {
				out.Error = err
			}
			out.Results = append(out.Results, hr)
			if opts.StopOnError {
				break
			}
			continue
		}
		if mutation := ExtractMutation(res); len(mutation) > 0 {
			out.Mutations = append(out.Mutations, mutation)
			out.Mutation = MergeMap(out.Mutation, mutation)
			if opts.StopOnMutation {
				out.Results = append(out.Results, hr)
				break
			}
		}
		if rejected, reason := ExtractRejection(res); rejected {
			out.Rejected = true
			out.RejectReason = reason
			out.Results = append(out.Results, hr)
			if opts.StopOnReject {
				break
			}
			continue
		}
		out.Results = append(out.Results, hr)
	}
	if len(out.Mutation) == 0 {
		out.Mutation = nil
	}
	return out, out.Error
}

type hookEntry struct {
	id, pluginID string
	priority     int
	source       registry.HookSource
	raw          map[string]any
	native       NativeHandler
}

func (i *HookInvoker) orderedEntries(event registry.HookEvent) []hookEntry {
	var entries []hookEntry
	if i.registry != nil {
		for _, h := range i.registry.HandlersFor(event) {
			entries = append(entries, hookEntry{id: h.ID, pluginID: h.PluginID, priority: h.Priority, source: h.Source, raw: h.Raw})
		}
	}
	i.nativeMu.RLock()
	for _, h := range i.nativeHandlers[event] {
		entries = append(entries, hookEntry{id: h.ID, pluginID: h.PluginID, priority: h.Priority, source: registry.HookSourceNative, native: h.Handler})
	}
	i.nativeMu.RUnlock()
	sort.SliceStable(entries, func(a, b int) bool {
		l, r := entries[a], entries[b]
		if l.priority == r.priority {
			if l.pluginID == r.pluginID {
				return l.id < r.id
			}
			return l.pluginID < r.pluginID
		}
		return l.priority < r.priority
	})
	return entries
}

func (i *HookInvoker) invokeOne(ctx context.Context, entry hookEntry, event registry.HookEvent, payload any, fallback time.Duration) (any, error) {
	if timeout := timeoutForEntry(entry.raw, fallback); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if entry.source == registry.HookSourceNative {
		if entry.native == nil {
			return nil, fmt.Errorf("native hook %q has no handler", entry.id)
		}
		return invokeNativeWithTimeout(ctx, entry, payload)
	}
	if i.host == nil {
		return nil, fmt.Errorf("node hook %q has no host", entry.id)
	}
	res, err := i.host.InvokeHookHandler(ctx, string(event), entry.id, payload)
	if err != nil {
		return nil, err
	}
	if !res.OK {
		if res.Error == "" {
			res.Error = "hook returned ok=false"
		}
		return res.Result, fmt.Errorf("node hook %s/%s: %s", res.PluginID, res.HookID, res.Error)
	}
	return res.Result, nil
}

func invokeNativeWithTimeout(ctx context.Context, entry hookEntry, payload any) (any, error) {
	type nativeResult struct {
		result any
		err    error
	}
	ch := make(chan nativeResult, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- nativeResult{err: fmt.Errorf("native hook %q panic: %v", entry.id, r)}
			}
		}()
		result, err := entry.native(ctx, payload)
		ch <- nativeResult{result: result, err: err}
	}()
	select {
	case result := <-ch:
		return result.result, result.err
	case <-ctx.Done():
		return nil, fmt.Errorf("native hook %q: %w", entry.id, ctx.Err())
	}
}

func timeoutForEntry(raw map[string]any, fallback time.Duration) time.Duration {
	if raw != nil {
		for _, key := range []string{"timeoutMs", "timeout_ms"} {
			if v, ok := raw[key]; ok {
				if ms := numberToInt64(v); ms > 0 {
					return time.Duration(ms) * time.Millisecond
				}
			}
		}
	}
	return fallback
}
func numberToInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case int32:
		return int64(n)
	case float64:
		return int64(n)
	case float32:
		return int64(n)
	case json.Number:
		out, _ := n.Int64()
		return out
	case string:
		out, _ := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return out
	default:
		return 0
	}
}
