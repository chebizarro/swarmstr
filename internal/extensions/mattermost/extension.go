// Package mattermost implements a Mattermost Bot channel extension for metiq.
//
// Registration: import _ "metiq/internal/extensions/mattermost" in the daemon
// main.go to include this plugin in the binary.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "base_url":        "https://mattermost.example.com",   // required
//	  "bot_token":       "TOKEN",                            // required: personal access token or bot token
//	  "team_name":       "myteam",                           // required: team slug
//	  "channel_name":    "town-square",                      // required: channel slug to listen on
//	  "allowed_senders": [],                                 // optional: allowlist of usernames
//	  "require_mention": false,                              // optional: only respond when mentioned
//	  "allow_polling": false                                 // explicit opt-in REST fallback
//	}
//
// Inbound messages are delivered event-driven via the Mattermost WebSocket
// events API (/api/v4/websocket, "posted" events). REST /posts?since polling is
// disabled by default and requires allow_polling=true. Outbound sends use POST
// /api/v4/posts.
//
// To add a Mattermost channel to your metiq config:
//
//	"nostr_channels": {
//	  "mm-general": {
//	    "kind": "mattermost",
//	    "config": {
//	      "base_url":   "https://mm.example.com",
//	      "bot_token":  "your-personal-access-token",
//	      "team_name":  "myteam",
//	      "channel_name": "town-square"
//	    }
//	  }
//	}
package mattermost

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
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
)

func init() {
	sdk.RegisterChannelConstructor("mattermost", func() sdk.ChannelPlugin { return &MattermostPlugin{} })
}

// MattermostPlugin is the factory for Mattermost channel instances.
type MattermostPlugin struct{}

func (p *MattermostPlugin) ID() string   { return "mattermost" }
func (p *MattermostPlugin) Type() string { return "Mattermost" }

func (p *MattermostPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"base_url": map[string]any{
				"type":        "string",
				"description": "Mattermost instance URL, e.g. https://mattermost.example.com.",
			},
			"bot_token": map[string]any{
				"type":        "string",
				"description": "Personal access token or bot token.",
			},
			"team_name": map[string]any{
				"type":        "string",
				"description": "Team slug (name identifier, not display name).",
			},
			"channel_name": map[string]any{
				"type":        "string",
				"description": "Channel slug to listen on and post to.",
			},
			"allowed_senders": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional username allowlist.",
			},
			"require_mention": map[string]any{
				"type":        "boolean",
				"description": "Only process messages that mention the bot.",
			},
			"allow_polling": map[string]any{
				"type":        "boolean",
				"description": "Explicitly allow REST /posts polling if WebSocket events are unavailable. Default false.",
			},
		},
		"required": []string{"base_url", "bot_token", "team_name", "channel_name"},
	}
}

func (p *MattermostPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{
		Reactions:    true,
		Threads:      true,
		Edit:         true,
		MultiAccount: true,
	}
}

func (p *MattermostPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	baseURL, _ := cfg["base_url"].(string)
	token, _ := cfg["bot_token"].(string)
	teamName, _ := cfg["team_name"].(string)
	channelName, _ := cfg["channel_name"].(string)

	for _, req := range []struct{ name, val string }{
		{"base_url", baseURL},
		{"bot_token", token},
		{"team_name", teamName},
		{"channel_name", channelName},
	} {
		if req.val == "" {
			return nil, fmt.Errorf("mattermost channel %q: config.%s is required", channelID, req.name)
		}
	}

	baseURL = strings.TrimRight(baseURL, "/")

	allowedSenders := map[string]bool{}
	switch v := cfg["allowed_senders"].(type) {
	case []interface{}:
		for _, s := range v {
			if u, ok := s.(string); ok && u != "" {
				allowedSenders[u] = true
			}
		}
	}

	requireMention := false
	if v, ok := cfg["require_mention"].(bool); ok {
		requireMention = v
	}

	bot := &mmBot{
		channelID:      channelID,
		baseURL:        baseURL,
		token:          token,
		teamName:       teamName,
		channelName:    channelName,
		allowedSenders: allowedSenders,
		requireMention: requireMention,
		allowPolling:   channels.PollingFallbackEnabled(cfg),
		onMessage:      onMessage,
		done:           make(chan struct{}),
		httpClient:     &http.Client{Timeout: 15 * time.Second},
		userNameByID:   map[string]string{},
	}

	// Resolve team and channel IDs from slugs.
	if err := bot.resolveIDs(ctx); err != nil {
		return nil, fmt.Errorf("mattermost channel %q: resolve IDs: %w", channelID, err)
	}

	// Fetch the bot's own user ID so we can skip our own messages.
	if err := bot.fetchSelfID(ctx); err != nil {
		log.Printf("mattermost: could not fetch bot user ID for channel %s: %v", channelID, err)
	}

	// Prefer the event-driven WebSocket events API. REST /posts polling is used
	// only when the WebSocket cannot be reached and allow_polling is enabled.
	runCtx, cancel := context.WithCancel(ctx)
	bot.cancel = cancel
	go bot.run(runCtx)
	return bot, nil
}

// ─── Bot implementation ───────────────────────────────────────────────────────

type mmBot struct {
	mu             sync.Mutex
	channelID      string // metiq channel ID
	baseURL        string
	token          string
	teamName       string
	channelName    string
	teamID         string
	mmChannelID    string // resolved Mattermost channel ID
	selfUserID     string
	selfUsername   string
	allowedSenders map[string]bool
	requireMention bool
	allowPolling   bool
	onMessage      func(sdk.InboundChannelMessage)
	userNameByID   map[string]string
	// lastSince is the cursor for polling (Unix ms).
	lastSince  int64
	done       chan struct{}
	cancel     context.CancelFunc
	httpClient *http.Client
}

func (b *mmBot) ID() string { return b.channelID }

func (b *mmBot) Close() {
	if b.cancel != nil {
		b.cancel()
	}
	select {
	case <-b.done:
	default:
		close(b.done)
	}
}

// ─── REST helpers ─────────────────────────────────────────────────────────────

func (b *mmBot) apiURL(path string) string {
	return b.baseURL + "/api/v4" + path
}

func (b *mmBot) doRequest(ctx context.Context, method, path string, body io.Reader) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, b.apiURL(path), body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return raw, resp.StatusCode, err
}

func (b *mmBot) doJSON(ctx context.Context, method, path string, reqBody, out any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(data)
	}
	raw, status, err := b.doRequest(ctx, method, path, bodyReader)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		var apiErr struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &apiErr)
		if apiErr.Message != "" {
			return fmt.Errorf("mattermost API %s %s: status %d: %s", method, path, status, apiErr.Message)
		}
		return fmt.Errorf("mattermost API %s %s: status %d", method, path, status)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (b *mmBot) resolveIDs(ctx context.Context) error {
	// Resolve team ID from team name.
	var teams []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := b.doJSON(ctx, http.MethodGet, "/teams?per_page=200&page=0", nil, &teams); err != nil {
		return fmt.Errorf("list teams: %w", err)
	}
	for _, t := range teams {
		if t.Name == b.teamName {
			b.teamID = t.ID
			break
		}
	}
	if b.teamID == "" {
		return fmt.Errorf("team %q not found", b.teamName)
	}

	// Resolve channel ID from channel name within the team.
	var channel struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := b.doJSON(ctx, http.MethodGet,
		fmt.Sprintf("/teams/%s/channels/name/%s", b.teamID, url.PathEscape(b.channelName)),
		nil, &channel); err != nil {
		return fmt.Errorf("resolve channel %q: %w", b.channelName, err)
	}
	b.mmChannelID = channel.ID
	return nil
}

func (b *mmBot) fetchSelfID(ctx context.Context) error {
	var me struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	if err := b.doJSON(ctx, http.MethodGet, "/users/me", nil, &me); err != nil {
		return err
	}
	b.selfUserID = me.ID
	b.selfUsername = me.Username
	if b.selfUserID != "" && b.selfUsername != "" {
		b.mu.Lock()
		if b.userNameByID == nil {
			b.userNameByID = map[string]string{}
		}
		b.userNameByID[b.selfUserID] = b.selfUsername
		b.mu.Unlock()
	}
	return nil
}

func (b *mmBot) resolveUsernames(ctx context.Context, userIDs []string) map[string]string {
	out := make(map[string]string, len(userIDs))
	if len(userIDs) == 0 {
		return out
	}

	missing := make([]string, 0, len(userIDs))
	b.mu.Lock()
	if b.userNameByID == nil {
		b.userNameByID = map[string]string{}
	}
	for _, id := range userIDs {
		if id == "" {
			continue
		}
		if username, ok := b.userNameByID[id]; ok {
			out[id] = username
			continue
		}
		missing = append(missing, id)
	}
	b.mu.Unlock()

	if len(missing) == 0 {
		return out
	}

	var users []struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	if err := b.doJSON(ctx, http.MethodPost, "/users/ids", missing, &users); err != nil {
		return out
	}
	b.mu.Lock()
	for _, u := range users {
		if u.ID == "" || u.Username == "" {
			continue
		}
		b.userNameByID[u.ID] = u.Username
		out[u.ID] = u.Username
	}
	b.mu.Unlock()
	return out
}

func messageMentions(message, username string) bool {
	if username == "" {
		return false
	}
	needle := "@" + strings.ToLower(strings.TrimSpace(username))
	if needle == "@" {
		return false
	}
	return strings.Contains(strings.ToLower(message), needle)
}

// ─── Polling ──────────────────────────────────────────────────────────────────

func (b *mmBot) poll(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	// Seed the cursor so we don't replay historical messages.
	b.mu.Lock()
	b.lastSince = time.Now().UnixMilli()
	b.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		case <-ticker.C:
			b.fetchPosts(ctx)
		}
	}
}

func (b *mmBot) fetchPosts(ctx context.Context) {
	b.mu.Lock()
	since := b.lastSince
	b.mu.Unlock()

	path := fmt.Sprintf("/channels/%s/posts?since=%d&per_page=60", b.mmChannelID, since)
	raw, status, err := b.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil || status != 200 {
		return
	}

	var result struct {
		Order []string          `json:"order"`
		Posts map[string]mmPost `json:"posts"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return
	}

	// Process in chronological order (order is newest-first, so reverse).
	for i, j := 0, len(result.Order)-1; i < j; i, j = i+1, j-1 {
		result.Order[i], result.Order[j] = result.Order[j], result.Order[i]
	}

	userIDSet := map[string]struct{}{}
	userIDs := make([]string, 0, len(result.Order))
	for _, postID := range result.Order {
		post := result.Posts[postID]
		if post.UserID == "" {
			continue
		}
		if _, seen := userIDSet[post.UserID]; seen {
			continue
		}
		userIDSet[post.UserID] = struct{}{}
		userIDs = append(userIDs, post.UserID)
	}
	usernameByID := b.resolveUsernames(ctx, userIDs)

	var newSince int64
	for _, postID := range result.Order {
		post := result.Posts[postID]
		if post.DeleteAt == 0 && post.CreateAt > newSince {
			newSince = post.CreateAt
		}
		b.handlePost(post, usernameByID[post.UserID])
	}

	if newSince > 0 {
		b.mu.Lock()
		// +1 so we don't replay the last post.
		b.lastSince = newSince + 1
		b.mu.Unlock()
	}
}

// ─── Event-driven inbound (WebSocket events API) ──────────────────────────────

// mmPost is a Mattermost post as returned by the REST /posts endpoint and
// embedded (JSON-encoded) inside WebSocket "posted" events.
type mmPost struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Message   string `json:"message"`
	ChannelID string `json:"channel_id"`
	CreateAt  int64  `json:"create_at"`
	RootID    string `json:"root_id"`
	DeleteAt  int64  `json:"delete_at"`
}

// handlePost applies sender/mention/self filtering and delivers a post to the
// agent. It is shared by the WebSocket event path and the polling fallback.
func (b *mmBot) handlePost(post mmPost, senderUsername string) {
	if post.DeleteAt > 0 || post.ID == "" {
		return
	}
	if post.UserID == b.selfUserID {
		return
	}
	if post.Message == "" {
		return
	}
	if len(b.allowedSenders) > 0 {
		if senderUsername == "" || !b.allowedSenders[senderUsername] {
			return
		}
	}
	if b.requireMention && b.selfUsername != "" && !messageMentions(post.Message, b.selfUsername) {
		return
	}
	b.onMessage(sdk.InboundChannelMessage{
		ChannelID: b.channelID,
		SenderID:  post.UserID,
		Text:      post.Message,
		EventID:   "mm-" + post.ID,
	})
}

const mmMaxReconnects = 10

// run prefers the event-driven WebSocket events API. REST polling requires
// explicit allow_polling opt-in when the WebSocket endpoint cannot be reached.
func (b *mmBot) run(ctx context.Context) {
	conn, err := b.dialWS(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if !b.allowPolling {
			log.Printf("mattermost: channel=%s WebSocket events API unavailable (%v); REST polling fallback disabled (set allow_polling=true to opt in)", b.channelID, err)
			return
		}
		log.Printf("mattermost: channel=%s WebSocket events API unavailable (%v); using explicitly enabled REST /posts polling fallback (team=%s, channel=%s)", b.channelID, err, b.teamName, b.channelName)
		b.poll(ctx)
		return
	}
	log.Printf("mattermost: channel=%s connected to WebSocket events API (team=%s, channel=%s)", b.channelID, b.teamName, b.channelName)
	b.serveWS(ctx, conn)
}

// mmWSFrame is a frame from the Mattermost WebSocket events API. Frames are
// either events ({"event":...,"data":...,"broadcast":...}) or command replies
// ({"status":...,"seq_reply":...}).
type mmWSFrame struct {
	Event     string          `json:"event"`
	Data      json.RawMessage `json:"data"`
	Broadcast struct {
		ChannelID string `json:"channel_id"`
	} `json:"broadcast"`
	Seq      int    `json:"seq"`
	Status   string `json:"status"`
	SeqReply int    `json:"seq_reply"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (b *mmBot) wsURL() string {
	u := b.baseURL
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	return u + "/api/v4/websocket"
}

// dialWS connects to the WebSocket events API, authenticates with the bot
// token, and returns once the server confirms the connection (a "hello" event
// or an OK status reply). A non-nil error means the WebSocket transport is
// unavailable and callers may use an explicitly enabled fallback.
func (b *mmBot) dialWS(ctx context.Context) (*websocket.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, b.wsURL(), nil)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(1 << 20)

	challenge := map[string]any{
		"seq":    1,
		"action": "authentication_challenge",
		"data":   map[string]any{"token": b.token},
	}
	if err := wsjson.Write(dialCtx, conn, challenge); err != nil {
		conn.Close(websocket.StatusInternalError, "auth write")
		return nil, err
	}

	// Wait for the server to confirm authentication: a "hello" event or an OK
	// reply to seq 1.
	for {
		var frame mmWSFrame
		if err := wsjson.Read(dialCtx, conn, &frame); err != nil {
			conn.Close(websocket.StatusInternalError, "auth read")
			return nil, err
		}
		if frame.Event == "hello" {
			return conn, nil
		}
		if frame.SeqReply == 1 {
			if strings.EqualFold(frame.Status, "OK") {
				return conn, nil
			}
			msg := "authentication failed"
			if frame.Error != nil && frame.Error.Message != "" {
				msg = frame.Error.Message
			}
			conn.Close(websocket.StatusPolicyViolation, "auth failed")
			return nil, fmt.Errorf("websocket auth: %s", msg)
		}
	}
}

// serveWS reads events from conn, reconnecting with backoff on failure. After
// mmMaxReconnects attempts it stops unless REST polling was explicitly enabled.
func (b *mmBot) serveWS(ctx context.Context, conn *websocket.Conn) {
	backoff := time.Second
	attempts := 0
	for {
		err := b.readWS(ctx, conn)
		_ = conn.Close(websocket.StatusNormalClosure, "reconnect")
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		default:
		}
		log.Printf("mattermost: channel=%s WebSocket read ended (%v); reconnecting", b.channelID, err)
		for {
			select {
			case <-ctx.Done():
				return
			case <-b.done:
				return
			case <-time.After(backoff):
			}
			attempts++
			newConn, derr := b.dialWS(ctx)
			if derr == nil {
				conn = newConn
				backoff = time.Second
				attempts = 0
				break
			}
			log.Printf("mattermost: channel=%s WebSocket reconnect failed (%v)", b.channelID, derr)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			if attempts >= mmMaxReconnects {
				if !b.allowPolling {
					log.Printf("mattermost: channel=%s giving up on WebSocket after %d attempts; REST polling fallback disabled", b.channelID, attempts)
					return
				}
				log.Printf("mattermost: channel=%s giving up on WebSocket after %d attempts; using explicitly enabled REST /posts polling fallback", b.channelID, attempts)
				b.poll(ctx)
				return
			}
		}
	}
}

// readWS reads and dispatches WebSocket frames until an error occurs.
func (b *mmBot) readWS(ctx context.Context, conn *websocket.Conn) error {
	for {
		var frame mmWSFrame
		if err := wsjson.Read(ctx, conn, &frame); err != nil {
			return err
		}
		if frame.Event != "posted" {
			continue
		}
		if frame.Broadcast.ChannelID != "" && frame.Broadcast.ChannelID != b.mmChannelID {
			continue
		}
		var data struct {
			Post       string `json:"post"`
			SenderName string `json:"sender_name"`
		}
		if err := json.Unmarshal(frame.Data, &data); err != nil || data.Post == "" {
			continue
		}
		var post mmPost
		if err := json.Unmarshal([]byte(data.Post), &post); err != nil {
			continue
		}
		if post.ChannelID != "" && post.ChannelID != b.mmChannelID {
			continue
		}
		b.handlePost(post, strings.TrimPrefix(data.SenderName, "@"))
	}
}

// ─── Send ─────────────────────────────────────────────────────────────────────

func (b *mmBot) Send(ctx context.Context, text string) error {
	return b.doJSON(ctx, http.MethodPost, "/posts", map[string]any{
		"channel_id": b.mmChannelID,
		"message":    text,
	}, nil)
}

// ─── ReactionHandle ───────────────────────────────────────────────────────────

// AddReaction adds an emoji reaction to a post.
// eventID must be of the form "mm-{post_id}".
func (b *mmBot) AddReaction(ctx context.Context, eventID, emoji string) error {
	postID := strings.TrimPrefix(eventID, "mm-")
	return b.doJSON(ctx, http.MethodPost, "/reactions", map[string]any{
		"user_id":    b.selfUserID,
		"post_id":    postID,
		"emoji_name": emoji,
	}, nil)
}

// RemoveReaction removes an emoji reaction from a post.
func (b *mmBot) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	postID := strings.TrimPrefix(eventID, "mm-")
	path := fmt.Sprintf("/users/%s/posts/%s/reactions/%s", b.selfUserID, postID, url.PathEscape(emoji))
	_, status, err := b.doRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("mattermost: remove reaction: status %d", status)
	}
	return nil
}

// ─── ThreadHandle ─────────────────────────────────────────────────────────────

// SendInThread posts a reply in a Mattermost thread.
// threadID is the post ID of the root message (root_id).
func (b *mmBot) SendInThread(ctx context.Context, threadID, text string) error {
	rootID := strings.TrimPrefix(threadID, "mm-")
	return b.doJSON(ctx, http.MethodPost, "/posts", map[string]any{
		"channel_id": b.mmChannelID,
		"message":    text,
		"root_id":    rootID,
	}, nil)
}

// ─── EditHandle ───────────────────────────────────────────────────────────────

// EditMessage updates the text of a previously sent post.
// eventID must be of the form "mm-{post_id}".
func (b *mmBot) EditMessage(ctx context.Context, eventID, newText string) error {
	postID := strings.TrimPrefix(eventID, "mm-")
	path := fmt.Sprintf("/posts/%s/patch", postID)
	return b.doJSON(ctx, http.MethodPut, path, map[string]any{
		"message": newText,
	}, nil)
}
