// Package canvas provides a thread-safe store for named canvases and notifies
// registered listeners when content changes.
//
// A canvas is a named, versioned content surface identified by a string ID.
// Agents update canvases via the canvas_update tool; browser UI clients
// subscribe to canvas.update WebSocket events to receive live content.
//
// Supported content types: "html", "json", "markdown".
package canvas

import (
	"fmt"
	"sync"
	"time"
)

// SupportedContentTypes is the set of content types canvases accept.
var SupportedContentTypes = map[string]bool{
	"html":     true,
	"json":     true,
	"markdown": true,
}

// Canvas holds the current state of a single named canvas.
type Canvas struct {
	ID          string    `json:"id"`
	ContentType string    `json:"content_type"`
	Data        string    `json:"data"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// UpdateEvent is broadcast to listeners after every successful UpdateCanvas call.
type UpdateEvent struct {
	CanvasID    string
	ContentType string
	Data        string
}

// Host manages a collection of named canvases and notifies subscribers of
// updates.
type Host struct {
	mu        sync.RWMutex
	canvases  map[string]*Canvas
	listeners []func(UpdateEvent)
}

// NewHost returns an empty Host.
func NewHost() *Host {
	return &Host{canvases: map[string]*Canvas{}}
}

// Subscribe registers a listener that is called synchronously after every
// successful UpdateCanvas call.  Listeners must not block.
func (h *Host) Subscribe(fn func(UpdateEvent)) {
	h.mu.Lock()
	h.listeners = append(h.listeners, fn)
	h.mu.Unlock()
}

// UpdateCanvas stores or replaces the content of canvas id with contentType
// and data.  contentType must be one of the SupportedContentTypes.  All
// registered listeners are notified after the update.
func (h *Host) UpdateCanvas(id, contentType, data string) error {
	if id == "" {
		return fmt.Errorf("canvas id must not be empty")
	}
	if !SupportedContentTypes[contentType] {
		return fmt.Errorf("unsupported content type %q (use html, json, or markdown)", contentType)
	}
	h.mu.Lock()
	h.canvases[id] = &Canvas{
		ID:          id,
		ContentType: contentType,
		Data:        data,
		UpdatedAt:   time.Now(),
	}
	listeners := make([]func(UpdateEvent), len(h.listeners))
	copy(listeners, h.listeners)
	h.mu.Unlock()

	ev := UpdateEvent{CanvasID: id, ContentType: contentType, Data: data}
	for _, fn := range listeners {
		fn(ev)
	}
	return nil
}

// GetCanvas returns the current canvas for id, or nil if it does not exist.
func (h *Host) GetCanvas(id string) *Canvas {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c := h.canvases[id]
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}

// ListCanvases returns a snapshot of all canvas IDs and their metadata
// (without the full data payload).
func (h *Host) ListCanvases() []Canvas {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]Canvas, 0, len(h.canvases))
	for _, c := range h.canvases {
		out = append(out, Canvas{
			ID:          c.ID,
			ContentType: c.ContentType,
			UpdatedAt:   c.UpdatedAt,
		})
	}
	return out
}

// DeleteCanvas removes a canvas by ID.  Returns false if the canvas did not
// exist.
func (h *Host) DeleteCanvas(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.canvases[id]; !ok {
		return false
	}
	delete(h.canvases, id)
	return true
}
