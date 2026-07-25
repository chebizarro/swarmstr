package mcpapp

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func uiResult(uri, html string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: "rendered"},
		&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: uri, MIMEType: "text/html", Text: html}},
	}}
}

func TestObserveToolResultMintsView(t *testing.T) {
	r := NewRegistry()
	view, minted := r.ObserveToolResult("sess", "srv", "show_chart", "call1", map[string]any{"x": 1}, uiResult("ui://srv/chart", "<html>app</html>"))
	if !minted {
		t.Fatal("expected view mint")
	}
	if view.ViewID == "" || view.UIResourceURI != "ui://srv/chart" || view.HTML != "<html>app</html>" || view.ToolResult != "rendered" {
		t.Fatalf("unexpected view: %+v", view)
	}
	resolved, err := r.Resolve("sess", view.ViewID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.ServerName != "srv" || resolved.ToolName != "show_chart" || resolved.ToolInput["x"] != 1 {
		t.Fatalf("unexpected resolved view: %+v", resolved)
	}
	// Foreign sessions cannot resolve the view.
	if _, err := r.Resolve("other", view.ViewID); err != ErrViewExpired {
		t.Fatalf("expected ErrViewExpired, got %v", err)
	}
	if _, err := r.Resolve("sess", "view_nope"); err != ErrViewExpired {
		t.Fatalf("expected ErrViewExpired, got %v", err)
	}
}

func TestObserveToolResultIgnoresNonAppResults(t *testing.T) {
	r := NewRegistry()
	cases := map[string]*mcp.CallToolResult{
		"nil result":  nil,
		"error":       {IsError: true, Content: []mcp.Content{&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "ui://x", Text: "<p/>"}}}},
		"plain text":  {Content: []mcp.Content{&mcp.TextContent{Text: "hi"}}},
		"non-ui uri":  {Content: []mcp.Content{&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "file://x", Text: "<p/>"}}}},
		"nil content": {Content: []mcp.Content{&mcp.EmbeddedResource{}}},
	}
	for label, result := range cases {
		if _, minted := r.ObserveToolResult("sess", "srv", "tool", "", nil, result); minted {
			t.Fatalf("%s: unexpected mint", label)
		}
	}
	// Missing attribution never mints.
	if _, minted := r.ObserveToolResult("", "srv", "tool", "", nil, uiResult("ui://x", "<p/>")); minted {
		t.Fatal("sessionless mint")
	}
}

func TestObserveToolResultResourceLinkLazyHTML(t *testing.T) {
	r := NewRegistry()
	result := &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.ResourceLink{URI: "ui://srv/app", MIMEType: "text/html"},
	}}
	view, minted := r.ObserveToolResult("sess", "srv", "tool", "", nil, result)
	if !minted || view.HTML != "" {
		t.Fatalf("expected lazy view: minted=%v %+v", minted, view)
	}
	r.SetHTML(view.ViewID, "<html>fetched</html>")
	resolved, err := r.Resolve("sess", view.ViewID)
	if err != nil || resolved.HTML != "<html>fetched</html>" {
		t.Fatalf("cached html missing: %+v err=%v", resolved, err)
	}
}

func TestViewExpiry(t *testing.T) {
	r := NewRegistry()
	now := time.Now()
	r.now = func() time.Time { return now }
	view, _ := r.ObserveToolResult("sess", "srv", "tool", "", nil, uiResult("ui://x", "<p/>"))
	now = now.Add(ViewTTL - time.Second)
	if _, err := r.Resolve("sess", view.ViewID); err != nil {
		t.Fatalf("resolve within ttl: %v", err)
	}
	now = now.Add(2 * time.Second)
	if _, err := r.Resolve("sess", view.ViewID); err != ErrViewExpired {
		t.Fatalf("expected expiry, got %v", err)
	}
}

func TestRegistryEvictionCap(t *testing.T) {
	r := NewRegistry()
	first, _ := r.ObserveToolResult("sess", "srv", "tool", "", nil, uiResult("ui://0", "<p/>"))
	for i := 1; i <= MaxViews; i++ {
		r.ObserveToolResult("sess", "srv", "tool", "", nil, uiResult(fmt.Sprintf("ui://%d", i), "<p/>"))
	}
	if _, err := r.Resolve("sess", first.ViewID); err != ErrViewExpired {
		t.Fatalf("expected oldest view evicted, got %v", err)
	}
	r.mu.Lock()
	count := len(r.views)
	r.mu.Unlock()
	if count > MaxViews {
		t.Fatalf("registry exceeded cap: %d", count)
	}
}

func TestRegistryConcurrency(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	ids := make(chan string, 64)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				view, minted := r.ObserveToolResult("sess", "srv", "tool", "", map[string]any{"n": n}, uiResult("ui://x", "<p/>"))
				if minted {
					select {
					case ids <- view.ViewID:
					default:
					}
				}
			}
		}(i)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				select {
				case id := <-ids:
					_, _ = r.Resolve("sess", id)
					r.SetHTML(id, "<p>x</p>")
				default:
				}
			}
		}()
	}
	wg.Wait()
}
