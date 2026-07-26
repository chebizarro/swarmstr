package methods

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Board method schemas. Params mirror the OpenClaw board.* wire contract;
// board state is backed by internal/gateway/board. Widget content sources are
// "html", "plugin", "mcp-app", and "canvas-doc" (the latter references an
// agent-written canvas document by id, swarmstr-5p0v item 1). The ticket-authorized view
// methods (board.prompt.authorize, board.data.read, board.action, the ticket
// variant of board.event) authenticate via short-lived view tickets minted by
// board.get.

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

// BoardEventRequest accepts either the legacy sessionKey+widget variant or
// the ticket-authorized variant (exactly one of the two).
type BoardEventRequest struct {
	SessionKey string          `json:"sessionKey"`
	Widget     string          `json:"widget"`
	Payload    json.RawMessage `json:"payload"`
	Ticket     string          `json:"ticket,omitempty"`
}

// Ticket-scoped wire limits mirroring the OpenClaw board protocol schema.
const (
	maxBoardTicketLength    = 2048
	maxBoardBindingIDLength = 64
	maxBoardCronJobIDLength = 256
	boardCronTriggerAction  = "cron.trigger"
	maxBoardActionLength    = len(boardCronTriggerAction+":") + maxBoardCronJobIDLength
	maxBoardCapabilityProps = 64
	maxBoardCapabilityKey   = 80
)

func validateBoardTicket(method, ticket string) error {
	if strings.TrimSpace(ticket) == "" {
		return fmt.Errorf("invalid %s params: ticket is required", method)
	}
	if len(ticket) > maxBoardTicketLength {
		return fmt.Errorf("invalid %s params: ticket exceeds %d characters", method, maxBoardTicketLength)
	}
	return nil
}

func validateBoardCapabilityParams(method string, params map[string]any) error {
	if len(params) > maxBoardCapabilityProps {
		return fmt.Errorf("invalid %s params: params exceed %d properties", method, maxBoardCapabilityProps)
	}
	for key := range params {
		if key == "" || len(key) > maxBoardCapabilityKey {
			return fmt.Errorf("invalid %s params: param keys must be 1-%d characters", method, maxBoardCapabilityKey)
		}
	}
	return nil
}

// BoardWidgetAppViewRequest re-mints an MCP-App view from a pinned mcp-app
// widget at an exact revision and instance.
type BoardWidgetAppViewRequest struct {
	SessionKey string `json:"sessionKey"`
	Name       string `json:"name"`
	Revision   int    `json:"revision"`
	// Wire name is instanceId; the shared alias normalizer rewrites it to
	// instance_id before strict decoding (same as board.widget.grant).
	InstanceID string `json:"instance_id"`
}

func (r BoardWidgetAppViewRequest) Normalize() (BoardWidgetAppViewRequest, error) {
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	r.Name = strings.TrimSpace(r.Name)
	r.InstanceID = strings.TrimSpace(r.InstanceID)
	if r.SessionKey == "" {
		return r, fmt.Errorf("invalid board.widget.appView params: sessionKey is required")
	}
	if r.Name == "" {
		return r, fmt.Errorf("invalid board.widget.appView params: name is required")
	}
	if r.Revision < 1 {
		return r, fmt.Errorf("invalid board.widget.appView params: revision is required")
	}
	if r.InstanceID == "" {
		return r, fmt.Errorf("invalid board.widget.appView params: instanceId is required")
	}
	return r, nil
}

// BoardPromptAuthorizeRequest asks whether a widget-initiated prompt needs
// operator confirmation (it does unless the "prompt" tool was granted).
type BoardPromptAuthorizeRequest struct {
	Ticket string `json:"ticket"`
}

func (r BoardPromptAuthorizeRequest) Normalize() (BoardPromptAuthorizeRequest, error) {
	if err := validateBoardTicket("board.prompt.authorize", r.Ticket); err != nil {
		return r, err
	}
	return r, nil
}

// BoardDataReadRequest reads one granted data binding on behalf of a widget
// view.
type BoardDataReadRequest struct {
	Ticket    string         `json:"ticket"`
	BindingID string         `json:"bindingId"`
	Params    map[string]any `json:"params,omitempty"`
}

func (r BoardDataReadRequest) Normalize() (BoardDataReadRequest, error) {
	if err := validateBoardTicket("board.data.read", r.Ticket); err != nil {
		return r, err
	}
	if strings.TrimSpace(r.BindingID) == "" {
		return r, fmt.Errorf("invalid board.data.read params: bindingId is required")
	}
	if len(r.BindingID) > maxBoardBindingIDLength {
		return r, fmt.Errorf("invalid board.data.read params: bindingId exceeds %d characters", maxBoardBindingIDLength)
	}
	if err := validateBoardCapabilityParams("board.data.read", r.Params); err != nil {
		return r, err
	}
	return r, nil
}

// BoardActionRequest runs one granted action verb (or triggers a cron job via
// the dedicated cron.trigger variant) on behalf of a widget view. Exactly one
// of the cron variant (action=="cron.trigger" with jobId) or the plugin verb
// variant (any other action, optional params) applies.
type BoardActionRequest struct {
	Ticket string         `json:"ticket"`
	Action string         `json:"action"`
	JobID  string         `json:"jobId,omitempty"`
	Params map[string]any `json:"params,omitempty"`
}

func (r BoardActionRequest) Normalize() (BoardActionRequest, error) {
	if err := validateBoardTicket("board.action", r.Ticket); err != nil {
		return r, err
	}
	if strings.TrimSpace(r.Action) == "" {
		return r, fmt.Errorf("invalid board.action params: action is required")
	}
	if len(r.Action) > maxBoardActionLength {
		return r, fmt.Errorf("invalid board.action params: action exceeds %d characters", maxBoardActionLength)
	}
	if r.Action == boardCronTriggerAction {
		r.JobID = strings.TrimSpace(r.JobID)
		if r.JobID == "" {
			return r, fmt.Errorf("invalid board.action params: jobId is required for cron.trigger")
		}
		if len(r.JobID) > maxBoardCronJobIDLength {
			return r, fmt.Errorf("invalid board.action params: jobId exceeds %d characters", maxBoardCronJobIDLength)
		}
		if len(r.Params) > 0 {
			return r, fmt.Errorf("invalid board.action params: cron.trigger does not accept params")
		}
		return r, nil
	}
	if r.JobID != "" {
		return r, fmt.Errorf("invalid board.action params: jobId is only valid with action cron.trigger")
	}
	if err := validateBoardCapabilityParams("board.action", r.Params); err != nil {
		return r, err
	}
	return r, nil
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
	case "mcp-app":
		if strings.TrimSpace(r.Content.ViewID) == "" {
			return r, fmt.Errorf("invalid board.widget.put params: viewId is required for mcp-app content")
		}
	case "canvas-doc":
		if strings.TrimSpace(r.Content.DocID) == "" {
			return r, fmt.Errorf("invalid board.widget.put params: docId is required for canvas-doc content")
		}
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
	if r.Ticket != "" {
		// Ticket variant: the ticket alone identifies the widget view.
		if r.SessionKey != "" || r.Widget != "" {
			return r, fmt.Errorf("invalid board.event params: ticket and sessionKey/widget are mutually exclusive")
		}
		if len(r.Ticket) > maxBoardTicketLength {
			return r, fmt.Errorf("invalid board.event params: ticket exceeds %d characters", maxBoardTicketLength)
		}
		return r, nil
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

func DecodeBoardWidgetAppViewParams(params json.RawMessage) (BoardWidgetAppViewRequest, error) {
	return decodeMethodParams[BoardWidgetAppViewRequest](params)
}

func DecodeBoardPromptAuthorizeParams(params json.RawMessage) (BoardPromptAuthorizeRequest, error) {
	return decodeMethodParams[BoardPromptAuthorizeRequest](params)
}

func DecodeBoardDataReadParams(params json.RawMessage) (BoardDataReadRequest, error) {
	return decodeMethodParams[BoardDataReadRequest](params)
}

func DecodeBoardActionParams(params json.RawMessage) (BoardActionRequest, error) {
	return decodeMethodParams[BoardActionRequest](params)
}
