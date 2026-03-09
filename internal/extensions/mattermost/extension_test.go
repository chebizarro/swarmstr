package mattermost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"swarmstr/internal/plugins/sdk"
)

// ── Plugin metadata ───────────────────────────────────────────────────────────

func TestMattermostPlugin_ID(t *testing.T) {
	p := &MattermostPlugin{}
	if p.ID() != "mattermost" {
		t.Fatalf("expected id='mattermost', got %q", p.ID())
	}
}

func TestMattermostPlugin_Capabilities(t *testing.T) {
	p := &MattermostPlugin{}
	caps := p.Capabilities()
	if !caps.Reactions || !caps.Threads || !caps.Edit || !caps.MultiAccount {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
}

func TestMattermostPlugin_ConfigSchema(t *testing.T) {
	p := &MattermostPlugin{}
	schema := p.ConfigSchema()
	required, _ := schema["required"].([]string)
	requiredSet := map[string]bool{}
	for _, r := range required {
		requiredSet[r] = true
	}
	for _, field := range []string{"base_url", "bot_token", "team_name", "channel_name"} {
		if !requiredSet[field] {
			t.Fatalf("expected %q in required fields", field)
		}
	}
}

func TestMattermostPlugin_Connect_MissingConfig(t *testing.T) {
	p := &MattermostPlugin{}
	tests := []struct {
		name string
		cfg  map[string]any
	}{
		{"missing base_url", map[string]any{"bot_token": "t", "team_name": "team", "channel_name": "ch"}},
		{"missing bot_token", map[string]any{"base_url": "http://x", "team_name": "team", "channel_name": "ch"}},
		{"missing team_name", map[string]any{"base_url": "http://x", "bot_token": "t", "channel_name": "ch"}},
		{"missing channel_name", map[string]any{"base_url": "http://x", "bot_token": "t", "team_name": "team"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Connect(context.Background(), "c1", tc.cfg, func(sdk.InboundChannelMessage) {})
			if err == nil {
				t.Fatal("expected error for missing config")
			}
		})
	}
}

// ── mmBot REST helpers ────────────────────────────────────────────────────────

func newTestServer(handler http.Handler) (*httptest.Server, *mmBot) {
	srv := httptest.NewServer(handler)
	bot := &mmBot{
		channelID:   "test-ch",
		baseURL:     srv.URL,
		token:       "tok",
		teamName:    "myteam",
		channelName: "general",
		teamID:      "team1",
		mmChannelID: "ch1",
		selfUserID:  "bot1",
		selfUsername: "botname",
		userNameByID: map[string]string{"bot1": "botname"},
		httpClient:  srv.Client(),
		done:        make(chan struct{}),
	}
	return srv, bot
}

func TestMMBot_Send(t *testing.T) {
	var received map[string]any
	srv, bot := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/posts") {
			_ = json.NewDecoder(r.Body).Decode(&received)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"p1"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if err := bot.Send(context.Background(), "hello mattermost"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if received["message"] != "hello mattermost" {
		t.Fatalf("expected message='hello mattermost', got %v", received["message"])
	}
	if received["channel_id"] != "ch1" {
		t.Fatalf("expected channel_id=ch1, got %v", received["channel_id"])
	}
}

func TestMMBot_AddReaction(t *testing.T) {
	var received map[string]any
	srv, bot := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/reactions") {
			_ = json.NewDecoder(r.Body).Decode(&received)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if err := bot.AddReaction(context.Background(), "mm-post123", "thumbsup"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if received["post_id"] != "post123" {
		t.Fatalf("expected post_id=post123, got %v", received["post_id"])
	}
	if received["emoji_name"] != "thumbsup" {
		t.Fatalf("expected emoji_name=thumbsup, got %v", received["emoji_name"])
	}
}

func TestMMBot_RemoveReaction(t *testing.T) {
	called := false
	srv, bot := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/reactions/") {
			called = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if err := bot.RemoveReaction(context.Background(), "mm-post123", "thumbsup"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected DELETE reaction endpoint to be called")
	}
}

func TestMMBot_SendInThread(t *testing.T) {
	var received map[string]any
	srv, bot := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/posts") {
			_ = json.NewDecoder(r.Body).Decode(&received)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"p2"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if err := bot.SendInThread(context.Background(), "mm-root1", "thread reply"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if received["root_id"] != "root1" {
		t.Fatalf("expected root_id=root1, got %v", received["root_id"])
	}
	if received["message"] != "thread reply" {
		t.Fatalf("expected message='thread reply', got %v", received["message"])
	}
}

func TestMMBot_EditMessage(t *testing.T) {
	var received map[string]any
	called := false
	srv, bot := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/patch") {
			called = true
			_ = json.NewDecoder(r.Body).Decode(&received)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if err := bot.EditMessage(context.Background(), "mm-post99", "updated text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected PUT patch endpoint to be called")
	}
	if received["message"] != "updated text" {
		t.Fatalf("expected message='updated text', got %v", received["message"])
	}
}

// ── fetchPosts ────────────────────────────────────────────────────────────────

func TestMMBot_FetchPosts_Delivers(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	srv, bot := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/users/ids") {
			_, _ = w.Write([]byte(`[{"id":"u1","username":"alice"}]`))
			return
		}
		if strings.Contains(r.URL.Path, "/channels/") && strings.Contains(r.URL.Path, "/posts") {
			resp := map[string]any{
				"order": []string{"p1", "p2"},
				"posts": map[string]any{
					"p1": map[string]any{"id": "p1", "user_id": "u1", "message": "hello", "create_at": float64(1000), "delete_at": float64(0)},
					"p2": map[string]any{"id": "p2", "user_id": "bot1", "message": "my own", "create_at": float64(2000), "delete_at": float64(0)},
				},
			}
			raw, _ := json.Marshal(resp)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(raw)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	bot.onMessage = func(m sdk.InboundChannelMessage) {
		delivered = append(delivered, m)
	}
	bot.fetchPosts(context.Background())

	// p2 is from bot1 (self) and should be skipped.
	if len(delivered) != 1 {
		t.Fatalf("expected 1 message (bot's own skipped), got %d", len(delivered))
	}
	if delivered[0].SenderID != "u1" {
		t.Fatalf("expected sender u1, got %q", delivered[0].SenderID)
	}
	if delivered[0].EventID != "mm-p1" {
		t.Fatalf("expected event_id=mm-p1, got %q", delivered[0].EventID)
	}
}

func TestMMBot_FetchPosts_SkipsDeleted(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	srv, bot := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/users/ids") {
			_, _ = w.Write([]byte(`[{"id":"u1","username":"alice"}]`))
			return
		}
		if strings.Contains(r.URL.Path, "/posts") {
			resp := map[string]any{
				"order": []string{"p1"},
				"posts": map[string]any{
					"p1": map[string]any{"id": "p1", "user_id": "u1", "message": "deleted", "create_at": float64(1000), "delete_at": float64(9999)},
				},
			}
			raw, _ := json.Marshal(resp)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(raw)
		}
	}))
	defer srv.Close()

	bot.onMessage = func(m sdk.InboundChannelMessage) { delivered = append(delivered, m) }
	bot.fetchPosts(context.Background())
	if len(delivered) != 0 {
		t.Fatalf("expected 0 messages (deleted post), got %d", len(delivered))
	}
}

func TestMMBot_FetchPosts_AllowedSendersFilter(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	srv, bot := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/users/ids") {
			_, _ = w.Write([]byte(`[{"id":"allowed","username":"alice"},{"id":"blocked","username":"bob"}]`))
			return
		}
		if strings.Contains(r.URL.Path, "/posts") {
			resp := map[string]any{
				"order": []string{"p1", "p2"},
				"posts": map[string]any{
					"p1": map[string]any{"id": "p1", "user_id": "allowed", "message": "ok", "create_at": float64(1000), "delete_at": float64(0)},
					"p2": map[string]any{"id": "p2", "user_id": "blocked", "message": "no", "create_at": float64(2000), "delete_at": float64(0)},
				},
			}
			raw, _ := json.Marshal(resp)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(raw)
		}
	}))
	defer srv.Close()

	bot.allowedSenders = map[string]bool{"alice": true}
	bot.onMessage = func(m sdk.InboundChannelMessage) { delivered = append(delivered, m) }
	bot.fetchPosts(context.Background())
	if len(delivered) != 1 || delivered[0].SenderID != "allowed" {
		t.Fatalf("expected only allowed sender, got %+v", delivered)
	}
}

func TestMMBot_FetchPosts_RequireMention(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	srv, bot := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/users/ids") {
			_, _ = w.Write([]byte(`[{"id":"u1","username":"alice"},{"id":"u2","username":"bob"}]`))
			return
		}
		if strings.Contains(r.URL.Path, "/posts") {
			resp := map[string]any{
				"order": []string{"p1", "p2"},
				"posts": map[string]any{
					"p1": map[string]any{"id": "p1", "user_id": "u1", "message": "hello @botname", "create_at": float64(1000), "delete_at": float64(0)},
					"p2": map[string]any{"id": "p2", "user_id": "u2", "message": "hello everyone", "create_at": float64(2000), "delete_at": float64(0)},
				},
			}
			raw, _ := json.Marshal(resp)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(raw)
		}
	}))
	defer srv.Close()

	bot.requireMention = true
	bot.onMessage = func(m sdk.InboundChannelMessage) { delivered = append(delivered, m) }
	bot.fetchPosts(context.Background())
	if len(delivered) != 1 || delivered[0].EventID != "mm-p1" {
		t.Fatalf("expected only mentioned post, got %+v", delivered)
	}
}

func TestMMBot_APIError(t *testing.T) {
	srv, bot := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid token"}`))
	}))
	defer srv.Close()

	err := bot.Send(context.Background(), "hello")
	if err == nil || !strings.Contains(err.Error(), "invalid token") {
		t.Fatalf("expected API error with message, got: %v", err)
	}
}
