package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"metiq/internal/agent"
	"metiq/internal/permissions"
	"metiq/internal/plugins/sdk"
	"metiq/internal/secrets"
	securitypkg "metiq/internal/security"
	"metiq/internal/security/commandanalysis"
)

// ProviderCredentialStore is the daemon credential persistence surface needed
// by provider-auth plugins. secrets.Store satisfies this interface.
type ProviderCredentialStore interface {
	GetMCPCredential(key string) (secrets.MCPAuthCredential, bool)
	PutMCPCredential(key string, cred secrets.MCPAuthCredential) error
	DeleteMCPCredential(key string) (bool, error)
}

// RuntimeServices connects plugin SDK contracts to live daemon state without
// importing cmd/metiqd implementation types into the plugin manager.
type RuntimeServices struct {
	ExecApprovalEvaluate func(context.Context, map[string]any) (map[string]any, error)
	ExecApprovalRequest  func(context.Context, map[string]any) (map[string]any, error)
	ExecApprovalSnapshot func() map[string]any
	ProviderCredentials  ProviderCredentialStore
}

type securityHostImpl struct{}

func (securityHostImpl) AnalyzeCommand(_ context.Context, request map[string]any) (map[string]any, error) {
	command, _ := request["command"].(string)
	if strings.TrimSpace(command) == "" {
		command, _ = request["command_text"].(string)
	}
	argv, err := runtimeStringSlice(request["argv"])
	if err != nil {
		return nil, fmt.Errorf("security analyze argv: %w", err)
	}
	if strings.TrimSpace(command) == "" && len(argv) == 0 {
		return nil, fmt.Errorf("security analyze requires command or argv")
	}
	return runtimeMap(commandanalysis.Analyze(command, argv))
}

func (securityHostImpl) CheckPath(_ context.Context, request map[string]any) (map[string]any, error) {
	root, _ := request["root"].(string)
	candidate, _ := request["path"].(string)
	root = strings.TrimSpace(root)
	candidate = strings.TrimSpace(candidate)
	if root == "" || candidate == "" {
		return nil, fmt.Errorf("security path check requires root and path")
	}
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return nil, fmt.Errorf("resolve path root: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(rootAbs); resolveErr == nil {
		rootAbs = resolved
	}
	candidateAbs := candidate
	if !filepath.IsAbs(candidateAbs) {
		candidateAbs = filepath.Join(rootAbs, candidateAbs)
	}
	candidateAbs, err = filepath.Abs(filepath.Clean(candidateAbs))
	if err != nil {
		return nil, fmt.Errorf("resolve candidate path: %w", err)
	}
	resolvedCandidate, resolveErr := filepath.EvalSymlinks(candidateAbs)
	if resolveErr == nil {
		candidateAbs = resolvedCandidate
	} else if !os.IsNotExist(resolveErr) {
		return nil, fmt.Errorf("resolve candidate path: %w", resolveErr)
	} else {
		parent := filepath.Dir(candidateAbs)
		if resolvedParent, parentErr := filepath.EvalSymlinks(parent); parentErr == nil {
			candidateAbs = filepath.Join(resolvedParent, filepath.Base(candidateAbs))
		}
	}
	rel, err := filepath.Rel(rootAbs, candidateAbs)
	if err != nil {
		return nil, fmt.Errorf("compare candidate path: %w", err)
	}
	rel = filepath.ToSlash(rel)
	allowed := rel == "." || rel == "" || (rel != ".." && !strings.HasPrefix(rel, "../"))
	result := map[string]any{"allowed": allowed, "resolved_path": candidateAbs, "root": rootAbs}
	if !allowed {
		result["reason"] = "path escapes the declared root"
	}
	return result, nil
}

type execApprovalHostImpl struct {
	evaluate func(context.Context, map[string]any) (map[string]any, error)
	request  func(context.Context, map[string]any) (map[string]any, error)
}

func (h execApprovalHostImpl) Evaluate(ctx context.Context, request map[string]any) (map[string]any, error) {
	if h.evaluate == nil {
		return nil, fmt.Errorf("exec approval evaluator is unavailable")
	}
	return h.evaluate(ctx, cloneRuntimeMap(request))
}

func (h execApprovalHostImpl) Request(ctx context.Context, request map[string]any) (map[string]any, error) {
	if h.request == nil {
		return nil, fmt.Errorf("exec approval request service is unavailable")
	}
	return h.request(ctx, cloneRuntimeMap(request))
}

type doctorHostImpl struct {
	cfg              configStateReader
	approvalSnapshot func() map[string]any
}

func (h doctorHostImpl) Run(_ context.Context, request map[string]any) ([]map[string]any, error) {
	checks := map[string]bool{"security": true, "exec_approvals": true}
	if requested, err := runtimeStringSlice(request["checks"]); err != nil {
		return nil, fmt.Errorf("doctor checks: %w", err)
	} else if len(requested) > 0 {
		checks = map[string]bool{}
		for _, check := range requested {
			checks[strings.ToLower(strings.TrimSpace(check))] = true
		}
	}
	var out []map[string]any
	if checks["security"] && h.cfg != nil {
		doc := h.cfg.Get()
		report := securitypkg.Audit(securitypkg.AuditOptions{ConfigDoc: &doc})
		for _, finding := range report.Findings {
			item, err := runtimeMap(finding)
			if err != nil {
				return nil, err
			}
			item["source"] = "security"
			out = append(out, item)
		}
	}
	if checks["exec_approvals"] && h.approvalSnapshot != nil {
		report := permissions.DoctorExecApprovalPolicy(h.approvalSnapshot())
		for _, finding := range report.Findings {
			item, err := runtimeMap(finding)
			if err != nil {
				return nil, err
			}
			item["source"] = "exec_approvals"
			out = append(out, item)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := fmt.Sprintf("%v:%v:%v", out[i]["source"], out[i]["code"], out[i]["check_id"])
		right := fmt.Sprintf("%v:%v:%v", out[j]["source"], out[j]["code"], out[j]["check_id"])
		return left < right
	})
	return out, nil
}

type providerAuthHostImpl struct {
	store    ProviderCredentialStore
	pluginID string
}

func (h providerAuthHostImpl) Status(_ context.Context, providerID string) (map[string]any, error) {
	providerID, err := validateProviderID(providerID)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"provider_id": providerID, "configured": false}
	if h.store == nil {
		return result, fmt.Errorf("provider credential store is unavailable")
	}
	credential, found := h.store.GetMCPCredential(h.credentialKey(providerID))
	if !found {
		return result, nil
	}
	result["configured"] = credential.AccessToken != "" || credential.RefreshToken != "" || credential.ClientSecret != ""
	if !credential.Expiry.IsZero() {
		result["expires_at"] = credential.Expiry.UTC().Format(time.RFC3339)
		result["expired"] = time.Now().After(credential.Expiry)
	}
	if len(credential.Scopes) > 0 {
		result["scopes"] = append([]string(nil), credential.Scopes...)
	}
	result["updated_at"] = credential.UpdatedAt.UTC().Format(time.RFC3339)
	return result, nil
}

func (h providerAuthHostImpl) Start(ctx context.Context, providerID string, request map[string]any) (map[string]any, error) {
	providerID, err := validateProviderID(providerID)
	if err != nil {
		return nil, err
	}
	if h.store == nil {
		return nil, fmt.Errorf("provider credential store is unavailable")
	}
	credential := secrets.MCPAuthCredential{
		AccessToken:  runtimeString(request["access_token"]),
		RefreshToken: runtimeString(request["refresh_token"]),
		ClientSecret: runtimeString(request["client_secret"]),
		TokenType:    runtimeString(request["token_type"]),
	}
	if scopes, scopeErr := runtimeStringSlice(request["scopes"]); scopeErr != nil {
		return nil, fmt.Errorf("provider auth scopes: %w", scopeErr)
	} else {
		credential.Scopes = scopes
	}
	if raw := runtimeString(request["expires_at"]); raw != "" {
		expiresAt, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			return nil, fmt.Errorf("provider auth expires_at: %w", parseErr)
		}
		credential.Expiry = expiresAt
	} else if seconds, ok := runtimeInt64(request["expires_in"]); ok && seconds > 0 {
		credential.Expiry = time.Now().UTC().Add(time.Duration(seconds) * time.Second)
	}
	if credential.AccessToken == "" && credential.RefreshToken == "" && credential.ClientSecret == "" {
		token, found, fetchErr := agent.FetchOAuthToken(ctx, providerID)
		if fetchErr != nil {
			return nil, fmt.Errorf("provider auth fetch: %w", fetchErr)
		}
		if !found || strings.TrimSpace(token) == "" {
			return nil, fmt.Errorf("provider auth request supplied no credentials and provider has no registered OAuth flow")
		}
		credential.AccessToken = token
		credential.TokenType = "Bearer"
	}
	if err := h.store.PutMCPCredential(h.credentialKey(providerID), credential); err != nil {
		return nil, fmt.Errorf("persist provider auth: %w", err)
	}
	return h.Status(ctx, providerID)
}

func (h providerAuthHostImpl) Clear(_ context.Context, providerID string) error {
	providerID, err := validateProviderID(providerID)
	if err != nil {
		return err
	}
	if h.store == nil {
		return fmt.Errorf("provider credential store is unavailable")
	}
	_, err = h.store.DeleteMCPCredential(h.credentialKey(providerID))
	return err
}

func (h providerAuthHostImpl) credentialKey(providerID string) string {
	pluginID := strings.TrimSpace(h.pluginID)
	if pluginID == "" {
		pluginID = "unknown"
	}
	return "plugin-provider-auth:" + pluginID + ":" + providerID
}

func validateProviderID(providerID string) (string, error) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	if providerID == "" || len(providerID) > 128 {
		return "", fmt.Errorf("provider id is required")
	}
	for _, r := range providerID {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return "", fmt.Errorf("provider id contains invalid characters")
	}
	return providerID, nil
}

func runtimeString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func runtimeStringSlice(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	switch values := value.(type) {
	case []string:
		out := make([]string, 0, len(values))
		for _, item := range values {
			item = strings.TrimSpace(item)
			if item == "" {
				return nil, fmt.Errorf("items must be non-empty strings")
			}
			out = append(out, item)
		}
		return out, nil
	case []any:
		out := make([]string, 0, len(values))
		for _, item := range values {
			text, ok := item.(string)
			text = strings.TrimSpace(text)
			if !ok || text == "" {
				return nil, fmt.Errorf("items must be non-empty strings")
			}
			out = append(out, text)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected an array of strings")
	}
}

func runtimeInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		if typed == float64(int64(typed)) {
			return int64(typed), true
		}
	}
	return 0, false
}

func runtimeMap(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode runtime result: %w", err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode runtime result: %w", err)
	}
	return result, nil
}

func cloneRuntimeMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return map[string]any{}
	}
	return result
}

var (
	_ sdk.SecurityHost     = securityHostImpl{}
	_ sdk.ExecApprovalHost = execApprovalHostImpl{}
	_ sdk.DoctorHost       = doctorHostImpl{}
	_ sdk.ProviderAuthHost = providerAuthHostImpl{}
)
