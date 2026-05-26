// Package matrix implements a Matrix channel extension for metiq using the
// Matrix Client-Server API.
//
// Registration: import _ "metiq/internal/extensions/matrix" in the daemon
// main.go to include this plugin in the binary.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "homeserver_url": "https://matrix.example.com",   // required
//	  "access_token":   "syt_...",                       // required (or use username+password)
//	  "username":       "@bot:example.com",              // used if access_token not set
//	  "password":       "s3cr3t",                        // used with username for login
//	  "device_id":      "SWARMBOT",                      // optional
//	  "allowed_senders": [],                             // optional: allowlist of MXIDs
//	  "auto_join_rooms": true                            // auto-accept invitations
//	}
//
// The extension uses /sync long-polling for real-time message delivery.
//
// Room configuration: the channel_id field in the metiq config must be the
// fully-qualified Matrix room ID (e.g. !roomid:server.com) or a room alias
// (#alias:server.com).  On connect the alias is resolved to a room ID.
//
// To add a Matrix channel to your metiq config:
//
//	"nostr_channels": {
//	  "matrix-main": {
//	    "kind": "matrix",
//	    "channel_id": "!roomid:server.com",
//	    "config": {
//	      "homeserver_url": "https://matrix.example.com",
//	      "access_token": "syt_your_token"
//	    }
//	  }
//	}
package matrix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
)

func init() {
	sdk.RegisterChannelConstructor("matrix", func() sdk.ChannelPlugin { return &MatrixPlugin{} })
}

// MatrixPlugin is the factory for Matrix channel instances.
type MatrixPlugin struct{}

func (p *MatrixPlugin) ID() string   { return "matrix" }
func (p *MatrixPlugin) Type() string { return "Matrix" }

func (p *MatrixPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"homeserver_url":  map[string]any{"type": "string", "description": "Matrix homeserver URL."},
			"access_token":    map[string]any{"type": "string", "description": "Matrix access token (preferred over username+password)."},
			"username":        map[string]any{"type": "string", "description": "Matrix user ID (e.g. @bot:example.com)."},
			"password":        map[string]any{"type": "string", "description": "Password for username+password login."},
			"device_id":       map[string]any{"type": "string", "description": "Device ID for login."},
			"allowed_senders": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional MXID allowlist."},
			"auto_join_rooms": map[string]any{"type": "boolean", "description": "Auto-accept room invitations."},
		},
		"required": []string{"homeserver_url"},
	}
}

func (p *MatrixPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{
		Typing:       true,
		Reactions:    true,
		Threads:      true,
		Edit:         true,
		MultiAccount: true,
	}
}

func (p *MatrixPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	hsURL, _ := cfg["homeserver_url"].(string)
	if hsURL == "" {
		return nil, fmt.Errorf("matrix channel %q: homeserver_url is required", channelID)
	}
	hsURL = strings.TrimRight(hsURL, "/")

	accessToken, _ := cfg["access_token"].(string)
	username, _ := cfg["username"].(string)
	password, _ := cfg["password"].(string)
	deviceID, _ := cfg["device_id"].(string)
	targetRoom, _ := cfg["channel_id"].(string)
	if strings.TrimSpace(targetRoom) == "" {
		targetRoom = channelID
	}

	if accessToken == "" && (username == "" || password == "") {
		return nil, fmt.Errorf("matrix channel %q: access_token or username+password is required", channelID)
	}

	allowedSenders := map[string]bool{}
	switch v := cfg["allowed_senders"].(type) {
	case []interface{}:
		for _, s := range v {
			if mxid, ok := s.(string); ok && mxid != "" {
				allowedSenders[mxid] = true
			}
		}
	}

	autoJoin := true
	if v, ok := cfg["auto_join_rooms"].(bool); ok {
		autoJoin = v
	}

	bot := &matrixBot{
		channelID:      channelID,
		hsURL:          hsURL,
		accessToken:    accessToken,
		allowedSenders: allowedSenders,
		autoJoin:       autoJoin,
		onMessage:      onMessage,
		done:           make(chan struct{}),
		httpClient:     &http.Client{Timeout: 60 * time.Second},
	}

	// Login if no access token provided.
	if accessToken == "" {
		token, selfID, err := bot.login(ctx, username, password, deviceID)
		if err != nil {
			return nil, fmt.Errorf("matrix channel %q: login: %w", channelID, err)
		}
		bot.accessToken = token
		bot.selfUserID = selfID
	} else {
		if err := bot.fetchSelfID(ctx); err != nil {
			log.Printf("matrix: could not fetch self user ID for channel %s: %v", channelID, err)
		}
	}

	// Resolve room alias to room ID if needed.
	roomID := targetRoom
	if strings.HasPrefix(targetRoom, "#") {
		resolved, err := bot.resolveRoomAlias(ctx, targetRoom)
		if err != nil {
			return nil, fmt.Errorf("matrix channel %q: resolve alias %q: %w", channelID, targetRoom, err)
		}
		roomID = resolved
	}
	bot.roomID = roomID

	go bot.syncLoop(ctx)
	log.Printf("matrix: sync loop started for channel %s (room=%s)", channelID, roomID)
	return bot, nil
}

// ─── Bot implementation ───────────────────────────────────────────────────────

type matrixBot struct {
	channelID      string
	hsURL          string
	accessToken    string
	selfUserID     string
	roomID         string
	allowedSenders map[string]bool
	autoJoin       bool
	onMessage      func(sdk.InboundChannelMessage)
	nextBatch      string
	mu             sync.Mutex
	done           chan struct{}
	httpClient     *http.Client
	txnCounter     atomic.Uint64
}

func (b *matrixBot) ID() string { return b.channelID }

func (b *matrixBot) Close() {
	select {
	case <-b.done:
	default:
		close(b.done)
	}
}

// ─── REST helpers ─────────────────────────────────────────────────────────────

func (b *matrixBot) csURL(path string) string {
	return b.hsURL + "/_matrix/client/v3" + path
}

func (b *matrixBot) mediaURL(path string) string {
	return b.hsURL + "/_matrix/media/v3" + path
}

func (b *matrixBot) doRequest(ctx context.Context, method, rawURL string, body io.Reader) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+b.accessToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return raw, resp.StatusCode, err
}

func (b *matrixBot) doJSON(ctx context.Context, method, path string, reqBody, out any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(data)
	}
	raw, status, err := b.doRequest(ctx, method, b.csURL(path), bodyReader)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		var apiErr struct {
			Errcode string `json:"errcode"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal(raw, &apiErr)
		return fmt.Errorf("matrix %s %s: status %d %s: %s", method, path, status, apiErr.Errcode, apiErr.Error)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (b *matrixBot) login(ctx context.Context, userID, password, deviceID string) (token, selfID string, err error) {
	req := map[string]any{
		"type": "m.login.password",
		"identifier": map[string]any{
			"type": "m.id.user",
			"user": userID,
		},
		"password": password,
	}
	if deviceID != "" {
		req["device_id"] = deviceID
	}
	var resp struct {
		AccessToken string `json:"access_token"`
		UserID      string `json:"user_id"`
	}
	if err := b.doJSON(ctx, http.MethodPost, "/login", req, &resp); err != nil {
		return "", "", err
	}
	return resp.AccessToken, resp.UserID, nil
}

func (b *matrixBot) fetchSelfID(ctx context.Context) error {
	var me struct {
		UserID string `json:"user_id"`
	}
	if err := b.doJSON(ctx, http.MethodGet, "/account/whoami", nil, &me); err != nil {
		return err
	}
	b.selfUserID = me.UserID
	return nil
}

func (b *matrixBot) resolveRoomAlias(ctx context.Context, alias string) (string, error) {
	path := "/directory/room/" + url.PathEscape(alias)
	var resp struct {
		RoomID string `json:"room_id"`
	}
	if err := b.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return "", err
	}
	return resp.RoomID, nil
}

// ─── /sync loop ───────────────────────────────────────────────────────────────

func (b *matrixBot) syncLoop(ctx context.Context) {
	backoff := 250 * time.Millisecond
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		default:
		}
		if err := b.doSync(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("matrix: sync error channel=%s: %v", b.channelID, err)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			case <-b.done:
				return
			}
			if backoff < 60*time.Second {
				backoff *= 2
				if backoff > 60*time.Second {
					backoff = 60 * time.Second
				}
			}
			continue
		}
		backoff = 250 * time.Millisecond
	}
}

func (b *matrixBot) doSync(ctx context.Context) error {
	b.mu.Lock()
	since := b.nextBatch
	b.mu.Unlock()

	params := url.Values{}
	params.Set("timeout", "30000")
	if since != "" {
		params.Set("since", since)
	}
	syncURL := b.csURL("/sync?" + params.Encode())

	syncCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	raw, status, err := b.doRequest(syncCtx, http.MethodGet, syncURL, nil)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("sync: status %d", status)
	}

	var resp struct {
		NextBatch string `json:"next_batch"`
		Rooms     struct {
			Join   map[string]roomTimeline `json:"join"`
			Invite map[string]any          `json:"invite"`
		} `json:"rooms"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return err
	}

	b.mu.Lock()
	b.nextBatch = resp.NextBatch
	b.mu.Unlock()

	// Handle invites.
	if b.autoJoin {
		for roomID := range resp.Rooms.Invite {
			if joinErr := b.joinRoom(ctx, roomID); joinErr != nil {
				log.Printf("matrix: auto-join room %s failed: %v", roomID, joinErr)
			}
		}
	}

	// Process events in our room.
	if timeline, ok := resp.Rooms.Join[b.roomID]; ok {
		for _, ev := range timeline.Timeline.Events {
			b.handleEvent(ev)
		}
	}
	return nil
}

type roomTimeline struct {
	Timeline struct {
		Events []matrixEvent `json:"events"`
	} `json:"timeline"`
}

type matrixEvent struct {
	EventID string          `json:"event_id"`
	Type    string          `json:"type"`
	Sender  string          `json:"sender"`
	Content json.RawMessage `json:"content"`
	RoomID  string          `json:"room_id"`
}

func (b *matrixBot) handleEvent(ev matrixEvent) {
	if ev.Type != "m.room.message" {
		return
	}
	if ev.Sender == b.selfUserID {
		return
	}
	if len(b.allowedSenders) > 0 && !b.allowedSenders[ev.Sender] {
		return
	}

	var content struct {
		MsgType string `json:"msgtype"`
		Body    string `json:"body"`
		URL     string `json:"url,omitempty"`
		Info    *struct {
			MimeType string `json:"mimetype,omitempty"`
		} `json:"info,omitempty"`
		// Relation for threaded/edited messages.
		RelatesTo *struct {
			RelType string `json:"rel_type"`
			EventID string `json:"event_id"`
		} `json:"m.relates_to"`
	}
	if err := json.Unmarshal(ev.Content, &content); err != nil {
		return
	}
	mediaURL := ""
	mediaMIME := ""
	switch content.MsgType {
	case "m.text", "m.notice", "m.emote":
	case "m.image", "m.file", "m.audio", "m.video":
		if strings.TrimSpace(content.URL) == "" {
			return
		}
		mediaURL = content.URL
		if content.Info != nil {
			mediaMIME = content.Info.MimeType
		}
	default:
		return
	}
	if content.Body == "" && mediaURL == "" {
		return
	}
	// Skip edits (m.replace relation).
	if content.RelatesTo != nil && content.RelatesTo.RelType == "m.replace" {
		return
	}

	b.onMessage(sdk.InboundChannelMessage{
		ChannelID: b.channelID,
		SenderID:  ev.Sender,
		Text:      content.Body,
		EventID:   ev.EventID,
		MediaURL:  mediaURL,
		MediaMIME: mediaMIME,
	})
}

func (b *matrixBot) joinRoom(ctx context.Context, roomID string) error {
	return b.doJSON(ctx, http.MethodPost, "/join/"+url.PathEscape(roomID), map[string]any{}, nil)
}

// ─── Send ─────────────────────────────────────────────────────────────────────

func (b *matrixBot) Send(ctx context.Context, text string) error {
	_, err := b.SendWithReceipt(ctx, text)
	return err
}

func (b *matrixBot) SendWithReceipt(ctx context.Context, text string) (channels.DeliveryReceipt, error) {
	return b.sendRoomMessage(ctx, map[string]any{"msgtype": "m.text", "body": text})
}

func (b *matrixBot) SendMedia(ctx context.Context, text string, data []byte, mimeType, filename string) (channels.DeliveryReceipt, error) {
	mxcURL, err := b.uploadMedia(ctx, data, mimeType, filename)
	if err != nil {
		receipt := channels.DeliveryReceipt{ChannelID: b.channelID, Provider: "matrix", Attempts: 1, CreatedAt: time.Now(), Status: channels.DeliveryFailed, Error: err.Error()}
		return receipt, err
	}
	msgType := matrixMsgTypeForMIME(mimeType)
	body := strings.TrimSpace(text)
	if body == "" {
		body = strings.TrimSpace(filename)
	}
	if body == "" {
		body = "attachment"
	}
	content := map[string]any{
		"msgtype": msgType,
		"body":    body,
		"url":     mxcURL,
	}
	if strings.TrimSpace(mimeType) != "" {
		content["info"] = map[string]any{"mimetype": mimeType, "size": len(data)}
	}
	return b.sendRoomMessage(ctx, content)
}

func (b *matrixBot) sendRoomMessage(ctx context.Context, content map[string]any) (channels.DeliveryReceipt, error) {
	txn := b.txnCounter.Add(1)
	path := fmt.Sprintf("/rooms/%s/send/m.room.message/%d", url.PathEscape(b.roomID), txn)
	var resp struct {
		EventID string `json:"event_id"`
	}
	err := b.doJSON(ctx, http.MethodPut, path, content, &resp)
	receipt := channels.DeliveryReceipt{ChannelID: b.channelID, Provider: "matrix", Attempts: 1, CreatedAt: time.Now(), MessageID: resp.EventID}
	if err != nil {
		receipt.Status = channels.DeliveryFailed
		receipt.Error = err.Error()
		return receipt, err
	}
	receipt.Status = channels.DeliveryDelivered
	receipt.DeliveredAt = time.Now()
	return receipt, nil
}

func (b *matrixBot) uploadMedia(ctx context.Context, data []byte, mimeType, filename string) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("matrix media: empty attachment")
	}
	params := url.Values{}
	if strings.TrimSpace(filename) != "" {
		params.Set("filename", filename)
	}
	endpoint := b.mediaURL("/upload")
	if encoded := params.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+b.accessToken)
	if strings.TrimSpace(mimeType) != "" {
		req.Header.Set("Content-Type", mimeType)
	} else {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("matrix media upload: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var result struct {
		ContentURI string `json:"content_uri"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	if strings.TrimSpace(result.ContentURI) == "" {
		return "", fmt.Errorf("matrix media upload: missing content_uri")
	}
	return result.ContentURI, nil
}

func matrixMsgTypeForMIME(mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "m.image"
	case strings.HasPrefix(mimeType, "audio/"):
		return "m.audio"
	case strings.HasPrefix(mimeType, "video/"):
		return "m.video"
	default:
		return "m.file"
	}
}

func (b *matrixBot) DeleteMessage(ctx context.Context, eventID string) error {
	txn := b.txnCounter.Add(1)
	path := fmt.Sprintf("/rooms/%s/redact/%s/%d", url.PathEscape(b.roomID), url.PathEscape(eventID), txn)
	return b.doJSON(ctx, http.MethodPut, path, map[string]any{}, nil)
}

func (b *matrixBot) SendTyping(ctx context.Context, durationMS int) error {
	if durationMS <= 0 {
		durationMS = 5000
	}
	userID := b.selfUserID
	if strings.TrimSpace(userID) == "" {
		if err := b.fetchSelfID(ctx); err != nil {
			return err
		}
		userID = b.selfUserID
	}
	path := fmt.Sprintf("/rooms/%s/typing/%s", url.PathEscape(b.roomID), url.PathEscape(userID))
	return b.doJSON(ctx, http.MethodPut, path, map[string]any{"typing": true, "timeout": durationMS}, nil)
}

// ─── ReactionHandle ───────────────────────────────────────────────────────────

// AddReaction sends an m.reaction event to the room.
// eventID is the Matrix event ID of the target message.
func (b *matrixBot) AddReaction(ctx context.Context, eventID, emoji string) error {
	txn := b.txnCounter.Add(1)
	path := fmt.Sprintf("/rooms/%s/send/m.reaction/%d",
		url.PathEscape(b.roomID), txn)
	return b.doJSON(ctx, http.MethodPut, path, map[string]any{
		"m.relates_to": map[string]any{
			"rel_type": "m.annotation",
			"event_id": eventID,
			"key":      emoji,
		},
	}, nil)
}

// RemoveReaction sends a redaction for the agent's reaction event.
// eventID should be the ID of the m.reaction event to redact.
func (b *matrixBot) RemoveReaction(ctx context.Context, eventID, _ string) error {
	txn := b.txnCounter.Add(1)
	path := fmt.Sprintf("/rooms/%s/redact/%s/%d",
		url.PathEscape(b.roomID), url.PathEscape(eventID), txn)
	return b.doJSON(ctx, http.MethodPut, path, map[string]any{}, nil)
}

// ─── ThreadHandle ─────────────────────────────────────────────────────────────

// SendInThread posts a threaded reply by setting m.relates_to with rel_type m.thread.
// threadID is the Matrix event ID of the thread root.
func (b *matrixBot) SendInThread(ctx context.Context, threadID, text string) error {
	txn := b.txnCounter.Add(1)
	path := fmt.Sprintf("/rooms/%s/send/m.room.message/%d",
		url.PathEscape(b.roomID), txn)
	return b.doJSON(ctx, http.MethodPut, path, map[string]any{
		"msgtype": "m.text",
		"body":    text,
		"m.relates_to": map[string]any{
			"rel_type": "m.thread",
			"event_id": threadID,
		},
	}, nil)
}

// ─── EditHandle ───────────────────────────────────────────────────────────────

// EditMessage sends an m.replace event to update a previous message.
// eventID is the Matrix event ID of the message to edit.
func (b *matrixBot) EditMessage(ctx context.Context, eventID, newText string) error {
	txn := b.txnCounter.Add(1)
	path := fmt.Sprintf("/rooms/%s/send/m.room.message/%d",
		url.PathEscape(b.roomID), txn)
	return b.doJSON(ctx, http.MethodPut, path, map[string]any{
		"msgtype": "m.text",
		"body":    "* " + newText,
		"m.new_content": map[string]any{
			"msgtype": "m.text",
			"body":    newText,
		},
		"m.relates_to": map[string]any{
			"rel_type": "m.replace",
			"event_id": eventID,
		},
	}, nil)
}
