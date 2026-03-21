// Package mattermost implements a Mattermost Bot channel extension for metiq.
//
// Registration: import _ "metiq/internal/extensions/mattermost" in the daemon
// main.go to register this plugin at startup.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "base_url":        "https://mattermost.example.com",   // required
//	  "bot_token":       "TOKEN",                            // required: personal access token or bot token
//	  "team_name":       "myteam",                           // required: team slug
//	  "channel_name":    "town-square",                      // required: channel slug to listen on
//	  "allowed_senders": [],                                 // optional: allowlist of usernames
//	  "require_mention": false                               // optional: only respond when mentioned
//	}
//
// The plugin uses the Mattermost REST API (GET /api/v4/...) for polling and
// POST /api/v4/posts for sending, with WebSocket for real-time events when
// available.  Polling at 3s intervals serves as fallback.
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

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
)

func init() {
	channels.RegisterChannelPlugin(&MattermostPlugin{})
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

	go bot.poll(ctx)
	log.Printf("mattermost: polling started for channel %s (team=%s, channel=%s)", channelID, teamName, channelName)
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
	onMessage      func(sdk.InboundChannelMessage)
	userNameByID   map[string]string
	// lastSince is the cursor for polling (Unix ms).
	lastSince  int64
	done       chan struct{}
	httpClient *http.Client
}

func (b *mmBot) ID() string { return b.channelID }

func (b *mmBot) Close() {
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
		Order []string `json:"order"`
		Posts map[string]struct {
			ID       string `json:"id"`
			UserID   string `json:"user_id"`
			Message  string `json:"message"`
			CreateAt int64  `json:"create_at"`
			RootID   string `json:"root_id"`
			DeleteAt int64  `json:"delete_at"`
		} `json:"posts"`
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
		senderUsername := usernameByID[post.UserID]
		if post.DeleteAt > 0 {
			continue
		}
		if post.CreateAt > newSince {
			newSince = post.CreateAt
		}
		if post.UserID == b.selfUserID {
			continue
		}
		if post.Message == "" {
			continue
		}
		if len(b.allowedSenders) > 0 {
			if senderUsername == "" || !b.allowedSenders[senderUsername] {
				continue
			}
		}
		if b.requireMention && b.selfUsername != "" && !messageMentions(post.Message, b.selfUsername) {
			continue
		}

		b.onMessage(sdk.InboundChannelMessage{
			ChannelID: b.channelID,
			SenderID:  post.UserID,
			Text:      post.Message,
			EventID:   "mm-" + post.ID,
		})
	}

	if newSince > 0 {
		b.mu.Lock()
		// +1 so we don't replay the last post.
		b.lastSince = newSince + 1
		b.mu.Unlock()
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
