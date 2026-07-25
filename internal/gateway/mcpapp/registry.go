// Package mcpapp implements the process-local MCP-App view registry backing
// the mcp.app.* gateway surface. A view is minted when an MCP tool call made
// during an agent session returns content referencing an interactive ui://
// resource (the MCP Apps / mcp-ui convention): either an embedded resource
// carrying the app HTML inline, or a resource link that is fetched lazily on
// first mcp.app.view. Views are ephemeral capability handles: they expire
// after ViewTTL, never survive a daemon restart, and scope every mcp.app.*
// operation to the originating server for the owning session.
package mcpapp

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// ViewTTL bounds how long a minted view authorizes mcp.app.* operations.
	// Metiq deviation: OpenClaw leases track the runtime lifecycle; Metiq
	// uses a fixed TTL refreshed on successful resolution.
	ViewTTL = 30 * time.Minute
	// MaxViews caps registry growth; the oldest views are evicted first.
	MaxViews = 256
	// UIResourcePrefix marks interactive app resources per the MCP Apps
	// convention.
	UIResourcePrefix = "ui://"
)

// ErrViewExpired is returned for unknown, expired, or foreign-session views
// so callers cannot distinguish the cases.
var ErrViewExpired = errors.New("mcp app view is expired or unknown")

// View is one minted MCP-App view.
type View struct {
	ViewID        string
	SessionKey    string
	ServerName    string
	ToolName      string
	ToolCallID    string
	UIResourceURI string
	// HTML is the app document. Empty until the embedded resource supplied
	// it inline or the first mcp.app.view fetched it via resources/read.
	HTML       string
	ToolInput  map[string]any
	ToolResult string
	// ReadOnly views render but refuse every interactive operation.
	ReadOnly  bool
	ExpiresAt time.Time
}

// Registry is the concurrency-safe view registry.
type Registry struct {
	mu    sync.Mutex
	views map[string]*View
	order []string // insertion order for eviction
	now   func() time.Time
}

// NewRegistry returns an empty view registry.
func NewRegistry() *Registry {
	return &Registry{views: map[string]*View{}, now: time.Now}
}

func newViewID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return "view_" + hex.EncodeToString(buf)
}

func cloneArgs(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		out[k] = v
	}
	return out
}

func cloneView(v *View) View {
	out := *v
	out.ToolInput = cloneArgs(v.ToolInput)
	return out
}

func (r *Registry) evictLocked(now time.Time) {
	kept := r.order[:0]
	for _, id := range r.order {
		v, ok := r.views[id]
		if !ok {
			continue
		}
		if !v.ExpiresAt.After(now) {
			delete(r.views, id)
			continue
		}
		kept = append(kept, id)
	}
	r.order = kept
	for len(r.order) > 0 && len(r.views) >= MaxViews {
		oldest := r.order[0]
		r.order = r.order[1:]
		delete(r.views, oldest)
	}
}

// Mint registers a view, assigning its ViewID and expiry.
func (r *Registry) Mint(v View) View {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.evictLocked(now)
	v.ViewID = newViewID()
	v.ExpiresAt = now.Add(ViewTTL)
	v.ToolInput = cloneArgs(v.ToolInput)
	stored := v
	r.views[v.ViewID] = &stored
	r.order = append(r.order, v.ViewID)
	return cloneView(&stored)
}

// Resolve returns the view when it exists, is fresh, and belongs to
// sessionKey. Any other outcome is ErrViewExpired.
func (r *Registry) Resolve(sessionKey, viewID string) (View, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.views[viewID]
	if !ok || v.SessionKey != sessionKey || !v.ExpiresAt.After(r.now()) {
		return View{}, ErrViewExpired
	}
	return cloneView(v), nil
}

// SetHTML caches the lazily fetched app document on the view.
func (r *Registry) SetHTML(viewID, html string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := r.views[viewID]; ok {
		v.HTML = html
	}
}

// uiResourceFromResult scans tool-call content for the first ui:// resource.
// Returns the URI, inline HTML when the resource was embedded with text
// content, and whether anything was found.
func uiResourceFromResult(result *mcp.CallToolResult) (uri, html string, ok bool) {
	if result == nil || result.IsError {
		return "", "", false
	}
	for _, content := range result.Content {
		switch c := content.(type) {
		case *mcp.EmbeddedResource:
			if c.Resource != nil && strings.HasPrefix(c.Resource.URI, UIResourcePrefix) {
				return c.Resource.URI, c.Resource.Text, true
			}
		case *mcp.ResourceLink:
			if strings.HasPrefix(c.URI, UIResourcePrefix) {
				return c.URI, "", true
			}
		}
	}
	return "", "", false
}

func textFromResult(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var parts []string
	for _, content := range result.Content {
		if c, ok := content.(*mcp.TextContent); ok && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// ObserveToolResult mints a view when an MCP tool result references a ui://
// resource. It returns the minted view and true, or false when the result
// carries no app content or no session is attributable.
func (r *Registry) ObserveToolResult(sessionKey, serverName, toolName, toolCallID string, args map[string]any, result *mcp.CallToolResult) (View, bool) {
	if sessionKey == "" || serverName == "" || toolName == "" {
		return View{}, false
	}
	uri, html, ok := uiResourceFromResult(result)
	if !ok {
		return View{}, false
	}
	view := r.Mint(View{
		SessionKey:    sessionKey,
		ServerName:    serverName,
		ToolName:      toolName,
		ToolCallID:    toolCallID,
		UIResourceURI: uri,
		HTML:          html,
		ToolInput:     args,
		ToolResult:    textFromResult(result),
	})
	return view, true
}
