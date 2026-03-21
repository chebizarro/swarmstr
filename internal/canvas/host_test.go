package canvas_test

import (
	"sync"
	"testing"
	"time"

	"metiq/internal/canvas"
)

func TestNewHost_Empty(t *testing.T) {
	h := canvas.NewHost()
	if got := h.ListCanvases(); len(got) != 0 {
		t.Fatalf("expected empty list, got %d", len(got))
	}
	if h.GetCanvas("nonexistent") != nil {
		t.Fatal("expected nil for nonexistent canvas")
	}
}

func TestUpdateCanvas_InvalidContentType(t *testing.T) {
	h := canvas.NewHost()
	err := h.UpdateCanvas("c1", "xml", "<data/>")
	if err == nil {
		t.Fatal("expected error for unsupported content type")
	}
}

func TestUpdateCanvas_EmptyID(t *testing.T) {
	h := canvas.NewHost()
	err := h.UpdateCanvas("", "html", "<p>hi</p>")
	if err == nil {
		t.Fatal("expected error for empty canvas ID")
	}
}

func TestUpdateCanvas_HTML(t *testing.T) {
	h := canvas.NewHost()
	if err := h.UpdateCanvas("main", "html", "<h1>Hello</h1>"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c := h.GetCanvas("main")
	if c == nil {
		t.Fatal("expected canvas to exist")
	}
	if c.ContentType != "html" || c.Data != "<h1>Hello</h1>" {
		t.Fatalf("unexpected canvas content: %+v", c)
	}
	if c.UpdatedAt.IsZero() {
		t.Fatal("expected non-zero UpdatedAt")
	}
}

func TestUpdateCanvas_JSON(t *testing.T) {
	h := canvas.NewHost()
	if err := h.UpdateCanvas("data", "json", `{"key":"value"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c := h.GetCanvas("data")
	if c == nil || c.ContentType != "json" {
		t.Fatalf("unexpected canvas: %+v", c)
	}
}

func TestUpdateCanvas_Markdown(t *testing.T) {
	h := canvas.NewHost()
	if err := h.UpdateCanvas("docs", "markdown", "# Title"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c := h.GetCanvas("docs")
	if c == nil || c.ContentType != "markdown" {
		t.Fatalf("unexpected canvas: %+v", c)
	}
}

func TestUpdateCanvas_Overwrite(t *testing.T) {
	h := canvas.NewHost()
	_ = h.UpdateCanvas("c", "html", "<p>v1</p>")
	t1 := h.GetCanvas("c").UpdatedAt
	time.Sleep(2 * time.Millisecond)
	_ = h.UpdateCanvas("c", "markdown", "v2")
	c := h.GetCanvas("c")
	if c.ContentType != "markdown" || c.Data != "v2" {
		t.Fatalf("expected overwritten canvas: %+v", c)
	}
	if !c.UpdatedAt.After(t1) {
		t.Fatal("expected UpdatedAt to advance on overwrite")
	}
}

func TestListCanvases(t *testing.T) {
	h := canvas.NewHost()
	_ = h.UpdateCanvas("a", "html", "<p>a</p>")
	_ = h.UpdateCanvas("b", "json", `{}`)
	list := h.ListCanvases()
	if len(list) != 2 {
		t.Fatalf("expected 2 canvases, got %d", len(list))
	}
	// ListCanvases should not include the full data payload.
	for _, c := range list {
		if c.Data != "" {
			t.Fatalf("expected empty data in listing, got %q", c.Data)
		}
	}
}

func TestDeleteCanvas(t *testing.T) {
	h := canvas.NewHost()
	_ = h.UpdateCanvas("del", "html", "<p/>")
	if !h.DeleteCanvas("del") {
		t.Fatal("expected true on first delete")
	}
	if h.GetCanvas("del") != nil {
		t.Fatal("expected canvas to be gone after delete")
	}
	if h.DeleteCanvas("del") {
		t.Fatal("expected false on second delete of nonexistent canvas")
	}
}

func TestSubscribe_Notified(t *testing.T) {
	h := canvas.NewHost()
	var mu sync.Mutex
	var events []canvas.UpdateEvent
	h.Subscribe(func(ev canvas.UpdateEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})
	_ = h.UpdateCanvas("c1", "html", "<p>hi</p>")
	_ = h.UpdateCanvas("c2", "json", `{}`)
	mu.Lock()
	n := len(events)
	mu.Unlock()
	if n != 2 {
		t.Fatalf("expected 2 events, got %d", n)
	}
	if events[0].CanvasID != "c1" || events[0].ContentType != "html" {
		t.Fatalf("unexpected first event: %+v", events[0])
	}
}

func TestSubscribe_NoNotifyOnError(t *testing.T) {
	h := canvas.NewHost()
	notified := false
	h.Subscribe(func(_ canvas.UpdateEvent) { notified = true })
	_ = h.UpdateCanvas("", "html", "ignored") // invalid — triggers error
	if notified {
		t.Fatal("expected no notification for failed update")
	}
}

func TestGetCanvas_IsolatedCopy(t *testing.T) {
	h := canvas.NewHost()
	_ = h.UpdateCanvas("c", "html", "original")
	c := h.GetCanvas("c")
	c.Data = "mutated"
	// Stored canvas must be unaffected.
	if h.GetCanvas("c").Data != "original" {
		t.Fatal("GetCanvas should return a copy, not a pointer to internal state")
	}
}
