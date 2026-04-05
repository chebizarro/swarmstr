package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	mcppkg "metiq/internal/mcp"
	secretspkg "metiq/internal/secrets"
	"metiq/internal/store/state"
)

type mcpAuthController struct {
	manager     **mcppkg.Manager
	tools       *agent.ToolRegistry
	secrets     *secretspkg.Store
	configDoc   func() state.ConfigDoc
	httpClient  *http.Client
	refreshSkew time.Duration
}

func newMCPAuthController(manager **mcppkg.Manager, tools *agent.ToolRegistry, secrets *secretspkg.Store, configDoc func() state.ConfigDoc) *mcpAuthController {
	return &mcpAuthController{
		manager:     manager,
		tools:       tools,
		secrets:     secrets,
		configDoc:   configDoc,
		httpClient:  http.DefaultClient,
		refreshSkew: 30 * time.Second,
	}
}

func (c *mcpAuthController) InstallOnManager(mgr *mcppkg.Manager) {
	if mgr == nil {
		return
	}
	mgr.SetRemoteAuthHeaderProvider(c.authorizationHeaders)
}

func (c *mcpAuthController) applyStart(ctx context.Context, req methods.MCPAuthStartRequest) (map[string]any, error) {
	resolved, err := c.serverConfig(req.Server)
	if err != nil {
		return nil, err
	}
	if !supportsRemoteOAuth(resolved.ServerConfig) {
		return nil, fmt.Errorf("server %s does not support remote oauth auth flow", req.Server)
	}
	startedAt := time.Now().UTC()
	started, err := c.startOAuthFlow(req.Server, resolved.ServerConfig, req.ClientSecret, req.TimeoutMS)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":             true,
		"server":         req.Server,
		"authorize_url":  started.AuthorizeURL,
		"callback_url":   started.CallbackURL,
		"started_at_ms":  startedAt.UnixMilli(),
		"expires_at_ms":  started.ExpiresAt.UnixMilli(),
		"pkce":           started.PKCE,
		"transport":      resolved.Type,
		"credential_key": mcppkg.CredentialKey(resolved.ServerConfig),
	}, nil
}

func (c *mcpAuthController) applyRefresh(ctx context.Context, req methods.MCPAuthRefreshRequest) (map[string]any, error) {
	resolved, err := c.serverConfig(req.Server)
	if err != nil {
		return nil, err
	}
	if !supportsRemoteOAuth(resolved.ServerConfig) {
		return nil, fmt.Errorf("server %s does not support remote oauth refresh", req.Server)
	}
	cred, ok := c.currentCredential(resolved.ServerConfig)
	if !ok {
		return nil, fmt.Errorf("auth required: no stored credentials for %s", req.Server)
	}
	updated, err := c.refreshCredential(ctx, req.Server, resolved.ServerConfig, cred, true)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":                true,
		"server":            req.Server,
		"token_type":        tokenType(updated),
		"expires_at_ms":     timeToMillis(updated.Expiry),
		"has_refresh_token": strings.TrimSpace(updated.RefreshToken) != "",
	}, nil
}

func (c *mcpAuthController) applyClear(_ context.Context, req methods.MCPAuthClearRequest) (map[string]any, error) {
	resolved, err := c.serverConfig(req.Server)
	if err != nil {
		return nil, err
	}
	deleted, err := c.clearCredential(req.Server, resolved.ServerConfig)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":      true,
		"server":  req.Server,
		"cleared": deleted,
	}, nil
}

type mcpOAuthFlowStart struct {
	AuthorizeURL string
	CallbackURL  string
	ExpiresAt    time.Time
	PKCE         bool
}

func (c *mcpAuthController) startOAuthFlow(serverName string, cfg mcppkg.ServerConfig, clientSecret string, timeoutMS int) (mcpOAuthFlowStart, error) {
	if c.secrets == nil {
		return mcpOAuthFlowStart{}, fmt.Errorf("secrets store not configured")
	}
	oauthCfg := cfg.OAuth
	if oauthCfg == nil || !oauthCfg.Enabled {
		return mcpOAuthFlowStart{}, fmt.Errorf("oauth is not configured for server %s", serverName)
	}
	if strings.TrimSpace(oauthCfg.ClientID) == "" || strings.TrimSpace(oauthCfg.AuthorizeURL) == "" || strings.TrimSpace(oauthCfg.TokenURL) == "" {
		return mcpOAuthFlowStart{}, fmt.Errorf("server %s oauth config requires client_id, authorize_url, and token_url", serverName)
	}
	host := strings.TrimSpace(oauthCfg.CallbackHost)
	if host == "" {
		host = "127.0.0.1"
	}
	listenAddr := net.JoinHostPort(host, fmt.Sprintf("%d", oauthCfg.CallbackPort))
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return mcpOAuthFlowStart{}, fmt.Errorf("listen oauth callback: %w", err)
	}
	callbackHost := host
	if strings.Contains(callbackHost, ":") && !strings.HasPrefix(callbackHost, "[") {
		callbackHost = "[" + callbackHost + "]"
	}
	callbackURL := fmt.Sprintf("http://%s/callback", net.JoinHostPort(callbackHost, fmt.Sprintf("%d", ln.Addr().(*net.TCPAddr).Port)))
	stateToken, err := randomURLToken(24)
	if err != nil {
		_ = ln.Close()
		return mcpOAuthFlowStart{}, err
	}
	pkceEnabled := oauthCfg.UsePKCE || strings.TrimSpace(oauthCfg.ClientSecretRef) == "" || strings.TrimSpace(clientSecret) == ""
	verifier := ""
	challenge := ""
	if pkceEnabled {
		verifier, err = randomURLToken(48)
		if err != nil {
			_ = ln.Close()
			return mcpOAuthFlowStart{}, err
		}
		challenge = pkceChallenge(verifier)
	}
	resolvedSecret := c.resolveClientSecret(cfg, clientSecret)
	oauthClient := &oauth2.Config{
		ClientID:     oauthCfg.ClientID,
		ClientSecret: resolvedSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  oauthCfg.AuthorizeURL,
			TokenURL: oauthCfg.TokenURL,
		},
		RedirectURL: callbackURL,
		Scopes:      append([]string(nil), oauthCfg.Scopes...),
	}
	authOpts := []oauth2.AuthCodeOption{oauth2.AccessTypeOffline}
	if pkceEnabled {
		authOpts = append(authOpts,
			oauth2.SetAuthURLParam("code_challenge", challenge),
			oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		)
	}
	authorizeURL := oauthClient.AuthCodeURL(stateToken, authOpts...)
	flowTimeout := 10 * time.Minute
	if timeoutMS > 0 {
		flowTimeout = time.Duration(timeoutMS) * time.Millisecond
	}
	if flowTimeout < time.Minute {
		flowTimeout = time.Minute
	}
	flowCtx, cancel := context.WithTimeout(context.Background(), flowTimeout)
	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux}
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		defer cancel()
		query := r.URL.Query()
		if errText := strings.TrimSpace(query.Get("error")); errText != "" {
			writeOAuthHTML(w, http.StatusBadRequest, "MCP auth failed", errText)
			return
		}
		if strings.TrimSpace(query.Get("state")) != stateToken {
			writeOAuthHTML(w, http.StatusBadRequest, "MCP auth failed", "state mismatch")
			return
		}
		code := strings.TrimSpace(query.Get("code"))
		if code == "" {
			writeOAuthHTML(w, http.StatusBadRequest, "MCP auth failed", "missing authorization code")
			return
		}
		exchangeCtx, exchangeCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer exchangeCancel()
		exchangeOpts := []oauth2.AuthCodeOption{}
		if verifier != "" {
			exchangeOpts = append(exchangeOpts, oauth2.SetAuthURLParam("code_verifier", verifier))
		}
		ok := false
		defer func() {
			go func() {
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer shutdownCancel()
				_ = srv.Shutdown(shutdownCtx)
			}()
			if !ok {
				log.Printf("[mcp] oauth flow failed for %s", serverName)
			}
		}()
		tok, err := oauthClient.Exchange(exchangeCtx, code, exchangeOpts...)
		if err != nil {
			writeOAuthHTML(w, http.StatusBadGateway, "MCP auth failed", err.Error())
			return
		}
		current, _ := c.currentCredential(cfg)
		cred := credentialFromOAuthToken(tok, current, oauthCfg.Scopes)
		if strings.TrimSpace(clientSecret) != "" {
			cred.ClientSecret = strings.TrimSpace(clientSecret)
		} else if current.ClientSecret != "" {
			cred.ClientSecret = current.ClientSecret
		}
		if err := c.secrets.PutMCPCredential(mcppkg.CredentialKey(cfg), cred); err != nil {
			writeOAuthHTML(w, http.StatusInternalServerError, "MCP auth failed", err.Error())
			return
		}
		go c.reconcile(serverName, "mcp auth callback", true)
		writeOAuthHTML(w, http.StatusOK, "MCP auth complete", fmt.Sprintf("Credentials stored for %s. You can close this window.", serverName))
		ok = true
	})
	go func() {
		<-flowCtx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("[mcp] oauth callback server for %s failed: %v", serverName, err)
		}
	}()
	return mcpOAuthFlowStart{
		AuthorizeURL: authorizeURL,
		CallbackURL:  callbackURL,
		ExpiresAt:    time.Now().UTC().Add(flowTimeout),
		PKCE:         pkceEnabled,
	}, nil
}

func (c *mcpAuthController) authorizationHeaders(ctx context.Context, serverName string, cfg mcppkg.ServerConfig) (map[string]string, error) {
	if !supportsRemoteOAuth(cfg) {
		return nil, nil
	}
	cred, ok := c.currentCredential(cfg)
	if !ok || strings.TrimSpace(cred.AccessToken) == "" {
		return nil, nil
	}
	if credentialExpiredSoon(cred, c.refreshSkew) {
		var err error
		cred, err = c.refreshCredential(ctx, serverName, cfg, cred, false)
		if err != nil {
			return nil, fmt.Errorf("auth required: %w", err)
		}
	}
	if strings.TrimSpace(cred.AccessToken) == "" {
		return nil, fmt.Errorf("auth required: missing access token")
	}
	return map[string]string{"Authorization": tokenType(cred) + " " + strings.TrimSpace(cred.AccessToken)}, nil
}

func (c *mcpAuthController) refreshCredential(ctx context.Context, serverName string, cfg mcppkg.ServerConfig, current secretspkg.MCPAuthCredential, reconcile bool) (secretspkg.MCPAuthCredential, error) {
	if c.secrets == nil {
		return current, fmt.Errorf("secrets store not configured")
	}
	oauthCfg := cfg.OAuth
	if oauthCfg == nil || !oauthCfg.Enabled {
		return current, fmt.Errorf("oauth is not configured for server %s", serverName)
	}
	if strings.TrimSpace(current.RefreshToken) == "" {
		return current, fmt.Errorf("refresh token unavailable")
	}
	token := &oauth2.Token{
		AccessToken:  strings.TrimSpace(current.AccessToken),
		RefreshToken: strings.TrimSpace(current.RefreshToken),
		TokenType:    tokenType(current),
		Expiry:       current.Expiry,
	}
	tok, err := c.exchangeRefreshToken(ctx, cfg, token)
	if err != nil {
		return current, err
	}
	updated := credentialFromOAuthToken(tok, current, oauthCfg.Scopes)
	if strings.TrimSpace(current.ClientSecret) != "" {
		updated.ClientSecret = strings.TrimSpace(current.ClientSecret)
	}
	if err := c.secrets.PutMCPCredential(mcppkg.CredentialKey(cfg), updated); err != nil {
		return current, err
	}
	if reconcile {
		go c.reconcile(serverName, "mcp auth refresh", true)
	}
	return updated, nil
}

func (c *mcpAuthController) clearCredential(serverName string, cfg mcppkg.ServerConfig) (bool, error) {
	if c.secrets == nil {
		return false, fmt.Errorf("secrets store not configured")
	}
	deleted, err := c.secrets.DeleteMCPCredential(mcppkg.CredentialKey(cfg))
	if err != nil {
		return false, err
	}
	go c.reconcile(serverName, "mcp auth clear", true)
	return deleted, nil
}

func (c *mcpAuthController) currentCredential(cfg mcppkg.ServerConfig) (secretspkg.MCPAuthCredential, bool) {
	if c.secrets == nil {
		return secretspkg.MCPAuthCredential{}, false
	}
	return c.secrets.GetMCPCredential(mcppkg.CredentialKey(cfg))
}

func (c *mcpAuthController) resolveClientSecret(cfg mcppkg.ServerConfig, override string) string {
	override = strings.TrimSpace(override)
	if override != "" {
		return override
	}
	if cred, ok := c.currentCredential(cfg); ok && strings.TrimSpace(cred.ClientSecret) != "" {
		return strings.TrimSpace(cred.ClientSecret)
	}
	if c.secrets == nil || cfg.OAuth == nil {
		return ""
	}
	if ref := strings.TrimSpace(cfg.OAuth.ClientSecretRef); ref != "" {
		if resolved, ok := c.secrets.Resolve(ref); ok {
			return strings.TrimSpace(resolved)
		}
	}
	return ""
}

func (c *mcpAuthController) serverConfig(serverName string) (mcppkg.ResolvedServerConfig, error) {
	if c.configDoc == nil {
		return mcppkg.ResolvedServerConfig{}, fmt.Errorf("config state not configured")
	}
	resolved := mcppkg.ResolveConfigDoc(c.configDoc())
	if server, ok := resolved.Servers[serverName]; ok {
		return server, nil
	}
	if server, ok := resolved.DisabledServers[serverName]; ok {
		return server, nil
	}
	return mcppkg.ResolvedServerConfig{}, fmt.Errorf("mcp server %s not configured", serverName)
}

func (c *mcpAuthController) reconcile(serverName, reason string, forceReconnect bool) {
	if c.manager == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resolved := mcppkg.ResolveConfigDoc(c.configDoc())
	hadManager := *c.manager != nil
	if *c.manager == nil && (len(resolved.Servers) > 0 || len(resolved.DisabledServers) > 0) {
		*c.manager = mcppkg.NewManager()
		c.InstallOnManager(*c.manager)
	}
	applyMCPConfigAndReconcile(ctx, c.manager, c.tools, resolved, reason+": "+serverName)
	if !forceReconnect || !hadManager || *c.manager == nil {
		return
	}
	if _, ok := resolved.Servers[serverName]; !ok {
		return
	}
	if err := (*c.manager).ReconnectServer(ctx, serverName); err != nil {
		log.Printf("[mcp] %s reconnect error for %s: %v", reason, serverName, err)
	}
	reconcileMCPToolRegistry(c.tools, *c.manager)
}

func supportsRemoteOAuth(cfg mcppkg.ServerConfig) bool {
	transport := strings.TrimSpace(cfg.Type)
	if transport == "" {
		if strings.TrimSpace(cfg.URL) != "" {
			transport = "sse"
		} else if strings.TrimSpace(cfg.Command) != "" {
			transport = "stdio"
		}
	}
	return (transport == "sse" || transport == "http") && cfg.OAuth != nil && cfg.OAuth.Enabled
}

func credentialExpiredSoon(cred secretspkg.MCPAuthCredential, skew time.Duration) bool {
	if cred.Expiry.IsZero() {
		return false
	}
	return time.Now().UTC().Add(skew).After(cred.Expiry.UTC())
}

func credentialFromOAuthToken(token *oauth2.Token, previous secretspkg.MCPAuthCredential, scopes []string) secretspkg.MCPAuthCredential {
	if token == nil {
		return previous
	}
	cred := previous
	if accessToken := strings.TrimSpace(token.AccessToken); accessToken != "" {
		cred.AccessToken = accessToken
	}
	if refreshToken := strings.TrimSpace(token.RefreshToken); refreshToken != "" {
		cred.RefreshToken = refreshToken
	}
	if tokenType := strings.TrimSpace(token.TokenType); tokenType != "" {
		cred.TokenType = tokenType
	}
	if !token.Expiry.IsZero() {
		cred.Expiry = token.Expiry.UTC()
	}
	cred.Scopes = append([]string(nil), scopes...)
	cred.UpdatedAt = time.Now().UTC()
	return cred
}

func tokenType(cred secretspkg.MCPAuthCredential) string {
	if strings.TrimSpace(cred.TokenType) == "" {
		return "Bearer"
	}
	return strings.TrimSpace(cred.TokenType)
}

func randomURLToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func timeToMillis(ts time.Time) int64 {
	if ts.IsZero() {
		return 0
	}
	return ts.UTC().UnixMilli()
}

func writeOAuthHTML(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<html><body><h1>%s</h1><p>%s</p></body></html>", html.EscapeString(title), html.EscapeString(message))
}

func (c *mcpAuthController) exchangeRefreshToken(ctx context.Context, cfg mcppkg.ServerConfig, token *oauth2.Token) (*oauth2.Token, error) {
	if token == nil || strings.TrimSpace(token.RefreshToken) == "" {
		return nil, fmt.Errorf("refresh token unavailable")
	}
	if cfg.OAuth == nil || strings.TrimSpace(cfg.OAuth.TokenURL) == "" {
		return nil, fmt.Errorf("oauth token endpoint is not configured")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", strings.TrimSpace(token.RefreshToken))
	form.Set("client_id", strings.TrimSpace(cfg.OAuth.ClientID))
	if clientSecret := c.resolveClientSecret(cfg, ""); clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.OAuth.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	client := c.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("oauth refresh failed: %s", strings.TrimSpace(string(raw)))
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return nil, fmt.Errorf("oauth refresh failed: missing access_token")
	}
	out := &oauth2.Token{
		AccessToken:  strings.TrimSpace(payload.AccessToken),
		TokenType:    strings.TrimSpace(payload.TokenType),
		RefreshToken: strings.TrimSpace(payload.RefreshToken),
	}
	if out.TokenType == "" {
		out.TokenType = tokenType(secretspkg.MCPAuthCredential{TokenType: token.TokenType})
	}
	if out.RefreshToken == "" {
		out.RefreshToken = strings.TrimSpace(token.RefreshToken)
	}
	if payload.ExpiresIn > 0 {
		out.Expiry = time.Now().UTC().Add(time.Duration(payload.ExpiresIn) * time.Second)
	}
	return out, nil
}
