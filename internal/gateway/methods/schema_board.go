package methods

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Board method schemas. Params mirror the OpenClaw board.* wire contract for
// the A7.4 slice; board state is backed by internal/gateway/board. Widget
// content sources are limited to "html" and "plugin": "mcp-app" and
// "canvas-doc" sources depend on deferred surfaces and are rejected here.

type BoardGetRequest struct {
	SessionKey string `json:"sessionKey"`
}

// BoardOpParam is one board layout operation on the wire (tagged by kind).
type BoardOpParam struct {
	Kind       string   `json:"kind"`
	TabID      string   `json:"tabId,omitempty"`
	Title      string   `json:"title,omitempty"`
	ChatDock   string   `json:"chatDock,omitempty"`
	Position   *int     `json:"position,omitempty"`
	TabIDs     []string `json:"tabIds,omitempty"`
	Name       string   `json:"name,omitempty"`
	After      string   `json:"after,omitempty"`
	SizeW      int      `json:"sizeW,omitempty"`
	SizeH      int      `json:"sizeH,omitempty"`
	HeightMode string   `json:"heightMode,omitempty"`
}

type BoardUpdateRequest struct {
	SessionKey string         `json:"sessionKey"`
	Ops        []BoardOpParam `json:"ops"`
}

// BoardWidgetContentParam carries the widget source union. Exactly one kind is
// accepted per request.
type BoardWidgetContentParam struct {
	Kind       string         `json:"kind"`
	HTML       string         `json:"html,omitempty"`
	PluginKind string         `json:"pluginKind,omitempty"`
	Props      map[string]any `json:"props,omitempty"`
	// Deferred source selectors, decoded so their rejection is explicit.
	ViewID string `json:"viewId,omitempty"`
	DocID  string `json:"docId,omitempty"`
}

type BoardWidgetPlacementParam struct {
	TabID string `json:"tabId,omitempty"`
	Size  string `json:"size,omitempty"`
	After string `json:"after,omitempty"`
}

type BoardWidgetDeclaredParam struct {
	NetOrigins []string `json:"netOrigins,omitempty"`
	Tools      []string `json:"tools,omitempty"`
}

type BoardWidgetPutRequest struct {
	SessionKey   string                     `json:"sessionKey"`
	Name         string                     `json:"name"`
	Title        string                     `json:"title,omitempty"`
	Content      BoardWidgetContentParam    `json:"content"`
	Presentation string                     `json:"presentation,omitempty"`
	HeightMode   string                     `json:"heightMode,omitempty"`
	Placement    *BoardWidgetPlacementParam `json:"placement,omitempty"`
	Declared     *BoardWidgetDeclaredParam  `json:"declared,omitempty"`
}

type BoardWidgetGrantRequest struct {
	SessionKey string `json:"sessionKey"`
	Name       string `json:"name"`
	Decision   string `json:"decision"`
	Revision   int    `json:"revision"`
	InstanceID string `json:"instance_id"`
}

// BoardEventRequest accepts the legacy sessionKey+widget variant. The
// ticket-authorized variant belongs to the deferred board view-ticket surface.
type BoardEventRequest struct {
	SessionKey string          `json:"sessionKey"`
	Widget     string          `json:"widget"`
	Payload    json.RawMessage `json:"payload"`
	Ticket     string          `json:"ticket,omitempty"`
}

func (r BoardGetRequest) Normalize() (BoardGetRequest, error) {
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	if r.SessionKey == "" {
		return r, fmt.Errorf("invalid board.get params: sessionKey is required")
	}
	return r, nil
}

func (r BoardUpdateRequest) Normalize() (BoardUpdateRequest, error) {
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	if r.SessionKey == "" {
		return r, fmt.Errorf("invalid board.update params: sessionKey is required")
	}
	for i, op := range r.Ops {
		if strings.TrimSpace(op.Kind) == "" {
			return r, fmt.Errorf("invalid board.update params: ops[%d].kind is required", i)
		}
	}
	return r, nil
}

var boardPresentations = map[string]bool{"card": true, "full-bleed": true, "frameless": true}
var boardHeightModes = map[string]bool{"auto": true, "fixed": true}

func (r BoardWidgetPutRequest) Normalize() (BoardWidgetPutRequest, error) {
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	r.Name = strings.TrimSpace(r.Name)
	if r.SessionKey == "" {
		return r, fmt.Errorf("invalid board.widget.put params: sessionKey is required")
	}
	if r.Name == "" {
		return r, fmt.Errorf("invalid board.widget.put params: name is required")
	}
	if len(r.Title) > 80 {
		return r, fmt.Errorf("invalid board.widget.put params: title exceeds 80 characters")
	}
	if r.Presentation != "" && !boardPresentations[r.Presentation] {
		return r, fmt.Errorf("invalid board.widget.put params: unknown presentation %q", r.Presentation)
	}
	if r.HeightMode != "" && !boardHeightModes[r.HeightMode] {
		return r, fmt.Errorf("invalid board.widget.put params: unknown heightMode %q", r.HeightMode)
	}
	switch r.Content.Kind {
	case "html":
		if r.Content.HTML == "" {
			return r, fmt.Errorf("invalid board.widget.put params: html content is required")
		}
	case "plugin":
		if strings.TrimSpace(r.Content.PluginKind) == "" {
			return r, fmt.Errorf("invalid board.widget.put params: pluginKind is required")
		}
	case "mcp-app", "canvas-doc":
		// Metiq deviation: MCP-App views and Canvas documents are deferred
		// board sources; reject explicitly instead of decoding to html.
		return r, fmt.Errorf("board widget content kind %q is not supported yet", r.Content.Kind)
	default:
		return r, fmt.Errorf("invalid board.widget.put params: unknown content kind %q", r.Content.Kind)
	}
	return r, nil
}

func (r BoardWidgetGrantRequest) Normalize() (BoardWidgetGrantRequest, error) {
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	r.Name = strings.TrimSpace(r.Name)
	r.Decision = strings.TrimSpace(r.Decision)
	r.InstanceID = strings.TrimSpace(r.InstanceID)
	if r.SessionKey == "" {
		return r, fmt.Errorf("invalid board.widget.grant params: sessionKey is required")
	}
	if r.Name == "" {
		return r, fmt.Errorf("invalid board.widget.grant params: name is required")
	}
	if r.Decision != "granted" && r.Decision != "rejected" {
		return r, fmt.Errorf("invalid board.widget.grant params: decision must be granted or rejected")
	}
	if r.Revision < 1 {
		return r, fmt.Errorf("invalid board.widget.grant params: revision is required")
	}
	if r.InstanceID == "" {
		return r, fmt.Errorf("invalid board.widget.grant params: instanceId is required")
	}
	return r, nil
}

func (r BoardEventRequest) Normalize() (BoardEventRequest, error) {
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	r.Widget = strings.TrimSpace(r.Widget)
	if strings.TrimSpace(r.Ticket) != "" {
		// Metiq deviation: view tickets belong to the deferred board sandbox
		// surface (board.prompt.authorize/data.read/action).
		return r, fmt.Errorf("board view tickets are not supported yet")
	}
	if r.SessionKey == "" {
		return r, fmt.Errorf("invalid board.event params: sessionKey is required")
	}
	if r.Widget == "" {
		return r, fmt.Errorf("invalid board.event params: widget is required")
	}
	return r, nil
}

func DecodeBoardGetParams(params json.RawMessage) (BoardGetRequest, error) {
	return decodeMethodParams[BoardGetRequest](params)
}

func DecodeBoardUpdateParams(params json.RawMessage) (BoardUpdateRequest, error) {
	return decodeMethodParams[BoardUpdateRequest](params)
}

func DecodeBoardWidgetPutParams(params json.RawMessage) (BoardWidgetPutRequest, error) {
	return decodeMethodParams[BoardWidgetPutRequest](params)
}

func DecodeBoardWidgetGrantParams(params json.RawMessage) (BoardWidgetGrantRequest, error) {
	return decodeMethodParams[BoardWidgetGrantRequest](params)
}

func DecodeBoardEventParams(params json.RawMessage) (BoardEventRequest, error) {
	return decodeMethodParams[BoardEventRequest](params)
}
