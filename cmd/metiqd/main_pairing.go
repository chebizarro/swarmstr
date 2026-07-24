package main

// main_pairing.go — Node and device pairing operations: pair requests,
// approvals, rejections, token management, and node lifecycle.
//
// Extracted from main.go to reduce god-file size. All functions remain in
// package main and reference the same globals/helpers as before.

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"metiq/internal/config"
	"metiq/internal/gateway/channels"
	"metiq/internal/gateway/methods"
	gatewayprotocol "metiq/internal/gateway/protocol"
	gatewayws "metiq/internal/gateway/ws"
	"metiq/internal/plugins/sdk"
	"metiq/internal/policy"
	"metiq/internal/store/state"
)

// ---------------------------------------------------------------------------
// Pairing data helpers
// ---------------------------------------------------------------------------

// gatewayDeviceTokenValidator binds WebSocket device credentials to the
// persisted pairing catalog. The signed connect payload still proves key
// possession in the WS runtime; this callback proves the token/role/scopes were
// approved and have not been revoked.
func gatewayDeviceTokenValidator(configState *runtimeConfigStore) gatewayws.DeviceTokenValidator {
	return func(connect gatewayprotocol.ConnectParams, token string) gatewayws.DeviceTokenDecision {
		if configState == nil || connect.Device == nil {
			return gatewayws.DeviceTokenDecision{Reason: "device_not_paired", Code: "DEVICE_AUTH_NOT_PAIRED"}
		}
		deviceID := strings.TrimSpace(connect.Device.ID)
		role := strings.ToLower(strings.TrimSpace(connect.Role))
		if role == "" {
			role = "operator"
		}
		pairing := pairingData(configState.Get())
		if role == "node" {
			for _, record := range toRecordSlice(pairing["node_paired"]) {
				if getString(record, "node_id") != deviceID {
					continue
				}
				if !constantTokenEqual(token, getString(record, "token")) {
					return gatewayws.DeviceTokenDecision{Reason: "device_token_mismatch", Code: "DEVICE_AUTH_TOKEN_MISMATCH"}
				}
				return gatewayws.DeviceTokenDecision{OK: true, Role: "node", Subject: deviceID}
			}
			return gatewayws.DeviceTokenDecision{Reason: "device_not_paired", Code: "DEVICE_AUTH_NOT_PAIRED"}
		}

		for _, record := range toRecordSlice(pairing["device_paired"]) {
			if getString(record, "device_id") != deviceID {
				continue
			}
			tokens, _ := record["tokens"].(map[string]any)
			entry, _ := tokens[role].(map[string]any)
			if entry == nil || !constantTokenEqual(token, getString(entry, "token")) {
				return gatewayws.DeviceTokenDecision{Reason: "device_token_mismatch", Code: "DEVICE_AUTH_TOKEN_MISMATCH"}
			}
			approved := getStringSlice(entry, "scopes")
			if len(approved) == 0 {
				approved = getStringSlice(record, "approved_scopes")
			}
			if !scopesAllow(connect.Scopes, approved) {
				return gatewayws.DeviceTokenDecision{Reason: "device_scope_escalation", Code: "DEVICE_AUTH_SCOPE_MISMATCH"}
			}
			granted := append([]string{}, connect.Scopes...)
			if len(granted) == 0 {
				granted = append([]string{}, approved...)
			}
			return gatewayws.DeviceTokenDecision{OK: true, Role: role, Scopes: granted, Subject: deviceID}
		}
		return gatewayws.DeviceTokenDecision{Reason: "device_not_paired", Code: "DEVICE_AUTH_NOT_PAIRED"}
	}
}

func constantTokenEqual(presented, expected string) bool {
	presented = strings.TrimSpace(presented)
	expected = strings.TrimSpace(expected)
	return presented != "" && expected != "" && subtle.ConstantTimeCompare([]byte(presented), []byte(expected)) == 1
}

func pairingData(cfg state.ConfigDoc) map[string]any {
	if cfg.Extra == nil {
		cfg.Extra = map[string]any{}
	}
	pairing, _ := cfg.Extra["pairing"].(map[string]any)
	if pairing == nil {
		pairing = map[string]any{}
	}
	return pairing
}

func toRecordSlice(raw any) []map[string]any {
	out := []map[string]any{}
	switch arr := raw.(type) {
	case []map[string]any:
		for _, item := range arr {
			out = append(out, item)
		}
		return out
	case []any:
		for _, item := range arr {
			m, ok := item.(map[string]any)
			if ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return out
	}
}

func applyPairingConfigUpdate(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, mutator func(map[string]any) (map[string]any, map[string]any, error)) (map[string]any, error) {
	if controlServices != nil {
		return controlServices.applyPairingConfigUpdate(ctx, docsRepo, configState, mutator)
	}
	// Fallback: use the package-level mutex when controlServices is nil
	// (common in tests that call pairing helpers directly).
	controlPairingConfigMu.Lock()
	defer controlPairingConfigMu.Unlock()

	cfg := configState.Get()
	pairing := pairingData(cfg)
	nextPairing, result, err := mutator(pairing)
	if err != nil {
		return nil, err
	}
	if cfg.Extra == nil {
		cfg.Extra = map[string]any{}
	}
	cfg.Extra["pairing"] = nextPairing
	if err := persistRuntimeConfigFile(cfg); err != nil {
		return nil, err
	}
	if _, err := docsRepo.PutConfig(ctx, cfg); err != nil {
		return nil, err
	}
	configState.Set(cfg)
	return result, nil
}

func (s *daemonServices) applyPairingConfigUpdate(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, mutator func(map[string]any) (map[string]any, map[string]any, error)) (map[string]any, error) {
	s.handlers.pairingConfigMu.Lock()
	defer s.handlers.pairingConfigMu.Unlock()

	cfg := configState.Get()
	pairing := pairingData(cfg)
	nextPairing, result, err := mutator(pairing)
	if err != nil {
		return nil, err
	}
	if cfg.Extra == nil {
		cfg.Extra = map[string]any{}
	}
	cfg.Extra["pairing"] = nextPairing
	if err := persistRuntimeConfigFile(cfg); err != nil {
		return nil, err
	}
	if _, err := docsRepo.PutConfig(ctx, cfg); err != nil {
		return nil, err
	}
	configState.Set(cfg)
	return result, nil
}

// applyChannelPairingAllow durably grants one observed sender access to the
// selected configured channel account. The pending request is removed only
// after this commit succeeds.
func observeChannelPairing(configState *runtimeConfigStore, store *channels.PairingStore, msg sdk.InboundChannelMessage, emit func(string, any)) (bool, error) {
	if configState == nil || store == nil {
		return false, fmt.Errorf("channel pairing runtime not configured")
	}
	initialCfg := configState.Get()
	chanCfg, ok := initialCfg.NostrChannels[msg.ChannelID]
	if !ok {
		return false, nil
	}
	if access := policy.EvaluateGroupMessage(msg.SenderID, chanCfg.AllowFrom, initialCfg); access.Allowed {
		return false, nil
	}
	account, err := channels.ResolveConfiguredChannelAccount(chanCfg.Kind, msg.ChannelID)
	if err != nil {
		return false, err
	}
	if !channels.EvaluatePairingInbound(account, msg, initialCfg).RequestPairing {
		return false, nil
	}
	// Re-read and re-resolve immediately before persistence. Approval updates the
	// config state before returning, so a racing late callback observes allowlist
	// membership and cannot recreate the request.
	latestCfg := configState.Get()
	latestAccount, err := channels.ResolveConfiguredChannelAccount(chanCfg.Kind, msg.ChannelID)
	if err != nil || !channels.EvaluatePairingInbound(latestAccount, msg, latestCfg).RequestPairing {
		return false, err
	}
	now := time.Now()
	observedAt := now
	if msg.CreatedAt > 0 {
		observedAt = time.Unix(msg.CreatedAt, 0)
	}
	pairing, created, err := store.UpsertObservedAt(latestAccount.Provider, latestAccount.ID, msg.SenderID, observedAt, now)
	if err != nil {
		return false, err
	}
	if created && emit != nil {
		emit(gatewayws.EventChannelPairingRequested, map[string]any{"ts_ms": now.UnixMilli(), "request_id": pairing.RequestID, "channel": pairing.Channel, "account_id": pairing.AccountID, "sender_id": pairing.SenderID})
	}
	return created, nil
}

func applyChannelPairingAllow(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req channels.PairingRequest) error {
	if docsRepo == nil || configState == nil {
		return fmt.Errorf("channel pairing persistence is not configured")
	}
	controlPairingConfigMu.Lock()
	defer controlPairingConfigMu.Unlock()

	resolved, err := channels.ResolveConfiguredChannelAccount(req.Channel, req.AccountID)
	if err != nil {
		return err
	}
	cfg := configState.Get()
	nextChannels := make(state.NostrChannelsConfig, len(cfg.NostrChannels))
	for id, account := range cfg.NostrChannels {
		copy := account
		copy.AllowFrom = append([]string(nil), account.AllowFrom...)
		copy.Config = cloneMapAny(account.Config)
		nextChannels[id] = copy
	}
	account, ok := nextChannels[resolved.ID]
	if !ok {
		return fmt.Errorf("channel account %q is no longer configured", resolved.ID)
	}
	for _, sender := range account.AllowFrom {
		if strings.EqualFold(strings.TrimSpace(sender), strings.TrimSpace(req.SenderID)) {
			return nil
		}
	}
	account.AllowFrom = append(account.AllowFrom, strings.TrimSpace(req.SenderID))
	nextChannels[resolved.ID] = account
	cfg.NostrChannels = nextChannels
	if err := persistRuntimeConfigFile(cfg); err != nil {
		return fmt.Errorf("persist channel pairing config: %w", err)
	}
	if _, err := docsRepo.PutConfig(ctx, cfg); err != nil {
		return fmt.Errorf("persist channel pairing state: %w", err)
	}
	configState.Set(cfg)
	channels.ConfigureChannelAccounts(cfg.NostrChannels)
	return nil
}

func randomToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func randomRequestID(prefix string) (string, error) {
	tok, err := randomToken()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s", prefix, tok), nil
}

func redactDeviceForList(record map[string]any) map[string]any {
	out := copyRecord(record)
	if tokens, ok := record["tokens"].(map[string]any); ok {
		summaries := make([]map[string]any, 0, len(tokens))
		for _, raw := range tokens {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			summary := map[string]any{
				"role":          getString(entry, "role"),
				"scopes":        getStringSlice(entry, "scopes"),
				"created_at_ms": getInt64(entry, "created_at_ms"),
			}
			if ts := getInt64(entry, "rotated_at_ms"); ts > 0 {
				summary["rotated_at_ms"] = ts
			}
			if ts := getInt64(entry, "revoked_at_ms"); ts > 0 {
				summary["revoked_at_ms"] = ts
			}
			if ts := getInt64(entry, "last_used_at_ms"); ts > 0 {
				summary["last_used_at_ms"] = ts
			}
			summaries = append(summaries, summary)
		}
		sort.Slice(summaries, func(i, j int) bool {
			return fmt.Sprintf("%v", summaries[i]["role"]) < fmt.Sprintf("%v", summaries[j]["role"])
		})
		out["tokens"] = summaries
	}
	delete(out, "approved_scopes")
	return out
}

func redactNodeForList(record map[string]any) map[string]any {
	return config.RedactMap(record)
}

func buildNodePendingRecord(req methods.NodePairRequest, isRepair bool, requestID string, ts int64) map[string]any {
	record := map[string]any{
		"request_id": requestID,
		"node_id":    req.NodeID,
		"silent":     req.Silent,
		"is_repair":  isRepair,
		"ts":         ts,
	}
	if req.DisplayName != "" {
		record["display_name"] = req.DisplayName
	}
	if req.Platform != "" {
		record["platform"] = req.Platform
	}
	if req.Version != "" {
		record["version"] = req.Version
	}
	if req.CoreVersion != "" {
		record["core_version"] = req.CoreVersion
	}
	if req.UIVersion != "" {
		record["ui_version"] = req.UIVersion
	}
	if req.DeviceFamily != "" {
		record["device_family"] = req.DeviceFamily
	}
	if req.ModelIdentifier != "" {
		record["model_identifier"] = req.ModelIdentifier
	}
	if len(req.Caps) > 0 {
		record["caps"] = req.Caps
	}
	if len(req.Commands) > 0 {
		record["commands"] = req.Commands
	}
	if len(req.Permissions) > 0 {
		record["permissions"] = req.Permissions
	}
	if req.RemoteIP != "" {
		record["remote_ip"] = req.RemoteIP
	}
	return record
}

func applyNodePairRequest(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.NodePairRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		now := time.Now().UnixMilli()
		pending := toRecordSlice(pairing["node_pending"])
		paired := toRecordSlice(pairing["node_paired"])
		isRepair := false
		for _, p := range paired {
			if getString(p, "node_id") == req.NodeID {
				isRepair = true
				break
			}
		}
		for i, item := range pending {
			if getString(item, "node_id") != req.NodeID {
				continue
			}
			requestID := getString(item, "request_id")
			if requestID == "" {
				var err error
				requestID, err = randomRequestID("node")
				if err != nil {
					return nil, nil, err
				}
			}
			record := buildNodePendingRecord(req, isRepair, requestID, now)
			pending[i] = record
			pairing["node_pending"] = pending
			return pairing, map[string]any{"status": "pending", "created": false, "request": record}, nil
		}
		requestID, err := randomRequestID("node")
		if err != nil {
			return nil, nil, err
		}
		record := buildNodePendingRecord(req, isRepair, requestID, now)
		pending = append(pending, record)
		sortRecordsByKeyDesc(pending, "ts")
		pairing["node_pending"] = pending
		return pairing, map[string]any{"status": "pending", "created": true, "request": record}, nil
	})
}

func applyNodePairList(_ context.Context, configState *runtimeConfigStore, _ methods.NodePairListRequest) (map[string]any, error) {
	pairing := pairingData(configState.Get())
	pending := toRecordSlice(pairing["node_pending"])
	paired := toRecordSlice(pairing["node_paired"])
	sortRecordsByKeyDesc(pending, "ts")
	sortRecordsByKeyDesc(paired, "approved_at_ms")
	return map[string]any{"pending": pending, "paired": paired}, nil
}

func applyNodePairApprove(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.NodePairApproveRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		now := time.Now().UnixMilli()
		pending := toRecordSlice(pairing["node_pending"])
		paired := toRecordSlice(pairing["node_paired"])
		remaining := make([]map[string]any, 0, len(pending))
		var approved map[string]any
		for _, item := range pending {
			if getString(item, "request_id") == req.RequestID {
				approved = item
				continue
			}
			remaining = append(remaining, item)
		}
		if approved == nil {
			return nil, nil, state.ErrNotFound
		}
		token, err := randomToken()
		if err != nil {
			return nil, nil, err
		}
		nodeID := getString(approved, "node_id")
		createdAt := now
		filtered := make([]map[string]any, 0, len(paired))
		for _, node := range paired {
			if getString(node, "node_id") == nodeID {
				if prior := getInt64(node, "created_at_ms"); prior > 0 {
					createdAt = prior
				}
				continue
			}
			filtered = append(filtered, node)
		}
		node := map[string]any{
			"node_id":        nodeID,
			"token":          token,
			"created_at_ms":  createdAt,
			"approved_at_ms": now,
		}
		for _, key := range []string{"display_name", "platform", "version", "core_version", "ui_version", "device_family", "model_identifier", "caps", "commands", "permissions", "remote_ip"} {
			if value, ok := approved[key]; ok {
				node[key] = value
			}
		}
		filtered = append(filtered, node)
		sortRecordsByKeyDesc(filtered, "approved_at_ms")
		pairing["node_pending"] = remaining
		pairing["node_paired"] = filtered
		return pairing, map[string]any{"request_id": req.RequestID, "node": node}, nil
	})
}

func applyNodePairReject(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.NodePairRejectRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		pending := toRecordSlice(pairing["node_pending"])
		remaining := make([]map[string]any, 0, len(pending))
		var nodeID string
		for _, item := range pending {
			if getString(item, "request_id") == req.RequestID {
				nodeID = getString(item, "node_id")
				continue
			}
			remaining = append(remaining, item)
		}
		if nodeID == "" {
			return nil, nil, state.ErrNotFound
		}
		pairing["node_pending"] = remaining
		return pairing, map[string]any{"request_id": req.RequestID, "node_id": nodeID}, nil
	})
}

func applyNodePairRemove(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.NodePairRemoveRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		paired := toRecordSlice(pairing["node_paired"])
		remaining := make([]map[string]any, 0, len(paired))
		removed := false
		for _, node := range paired {
			if getString(node, "node_id") == req.NodeID {
				removed = true
				continue
			}
			remaining = append(remaining, node)
		}
		if !removed {
			return nil, nil, state.ErrNotFound
		}
		pairing["node_paired"] = remaining
		return pairing, map[string]any{"node_id": req.NodeID}, nil
	})
}

func applyNodePairVerify(_ context.Context, configState *runtimeConfigStore, req methods.NodePairVerifyRequest) (map[string]any, error) {
	pairing := pairingData(configState.Get())
	for _, item := range toRecordSlice(pairing["node_paired"]) {
		if getString(item, "node_id") == req.NodeID && getString(item, "token") == req.Token {
			return map[string]any{"ok": true, "node": item}, nil
		}
	}
	return map[string]any{"ok": false}, nil
}

func applyDevicePairList(_ context.Context, configState *runtimeConfigStore, _ methods.DevicePairListRequest) (map[string]any, error) {
	pairing := pairingData(configState.Get())
	pending := toRecordSlice(pairing["device_pending"])
	paired := toRecordSlice(pairing["device_paired"])
	sortRecordsByKeyDesc(pending, "ts")
	sortRecordsByKeyDesc(paired, "approved_at_ms")
	redacted := make([]map[string]any, 0, len(paired))
	for _, device := range paired {
		redacted = append(redacted, redactDeviceForList(device))
	}
	return map[string]any{"pending": pending, "paired": redacted}, nil
}

func applyDevicePairApprove(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.DevicePairApproveRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		now := time.Now().UnixMilli()
		pending := toRecordSlice(pairing["device_pending"])
		paired := toRecordSlice(pairing["device_paired"])
		remaining := make([]map[string]any, 0, len(pending))
		var approved map[string]any
		for _, item := range pending {
			if getString(item, "request_id") == req.RequestID {
				approved = item
				continue
			}
			remaining = append(remaining, item)
		}
		if approved == nil {
			return nil, nil, state.ErrNotFound
		}
		deviceID := getString(approved, "device_id")
		if deviceID == "" {
			return nil, nil, fmt.Errorf("invalid pending pairing record")
		}
		device := copyRecord(approved)
		createdAt := now
		approvedScopes := getStringSlice(approved, "scopes")
		tokens := map[string]any{}
		filtered := make([]map[string]any, 0, len(paired))
		for _, item := range paired {
			if getString(item, "device_id") != deviceID {
				filtered = append(filtered, item)
				continue
			}
			if prior := getInt64(item, "created_at_ms"); prior > 0 {
				createdAt = prior
			}
			approvedScopes = mergeUniqueStrings(getStringSlice(item, "approved_scopes"), approvedScopes)
			if existingTokens, ok := item["tokens"].(map[string]any); ok {
				for key, value := range existingTokens {
					tokens[key] = value
				}
			}
		}
		role := getString(approved, "role")
		if role != "" {
			existing, _ := tokens[role].(map[string]any)
			scopes := getStringSlice(approved, "scopes")
			if len(scopes) == 0 {
				scopes = getStringSlice(existing, "scopes")
			}
			if len(scopes) == 0 {
				scopes = approvedScopes
			}
			tok, err := randomToken()
			if err != nil {
				return nil, nil, err
			}
			entry := map[string]any{
				"token":         tok,
				"role":          role,
				"scopes":        scopes,
				"created_at_ms": now,
			}
			if existing != nil {
				if created := getInt64(existing, "created_at_ms"); created > 0 {
					entry["created_at_ms"] = created
				}
				entry["rotated_at_ms"] = now
				if last := getInt64(existing, "last_used_at_ms"); last > 0 {
					entry["last_used_at_ms"] = last
				}
			}
			tokens[role] = entry
		}
		device["approved_scopes"] = approvedScopes
		device["scopes"] = approvedScopes
		device["tokens"] = tokens
		device["created_at_ms"] = createdAt
		device["approved_at_ms"] = now
		delete(device, "request_id")
		delete(device, "ts")
		filtered = append(filtered, device)
		sortRecordsByKeyDesc(filtered, "approved_at_ms")
		pairing["device_pending"] = remaining
		pairing["device_paired"] = filtered
		return pairing, map[string]any{"request_id": req.RequestID, "device": redactDeviceForList(device)}, nil
	})
}

func applyDevicePairReject(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.DevicePairRejectRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		pending := toRecordSlice(pairing["device_pending"])
		remaining := make([]map[string]any, 0, len(pending))
		var deviceID string
		for _, item := range pending {
			if getString(item, "request_id") == req.RequestID {
				deviceID = getString(item, "device_id")
				continue
			}
			remaining = append(remaining, item)
		}
		if deviceID == "" {
			return nil, nil, state.ErrNotFound
		}
		pairing["device_pending"] = remaining
		return pairing, map[string]any{"request_id": req.RequestID, "device_id": deviceID}, nil
	})
}

func applyDevicePairRemove(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.DevicePairRemoveRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		paired := toRecordSlice(pairing["device_paired"])
		remaining := make([]map[string]any, 0, len(paired))
		removed := false
		for _, item := range paired {
			if getString(item, "device_id") == req.DeviceID {
				removed = true
				continue
			}
			remaining = append(remaining, item)
		}
		if !removed {
			return nil, nil, state.ErrNotFound
		}
		pairing["device_paired"] = remaining
		return pairing, map[string]any{"device_id": req.DeviceID}, nil
	})
}

func applyDevicePairRename(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.DevicePairRenameRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		paired := toRecordSlice(pairing["device_paired"])
		for _, device := range paired {
			if getString(device, "device_id") != req.DeviceID {
				continue
			}
			device["label"] = req.Label
			pairing["device_paired"] = paired
			return pairing, map[string]any{"device_id": req.DeviceID, "label": req.Label}, nil
		}
		return nil, nil, state.ErrNotFound
	})
}

func applyDeviceTokenRotate(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.DeviceTokenRotateRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		now := time.Now().UnixMilli()
		paired := toRecordSlice(pairing["device_paired"])
		for _, item := range paired {
			if getString(item, "device_id") != req.DeviceID {
				continue
			}
			tokens, _ := item["tokens"].(map[string]any)
			if tokens == nil {
				tokens = map[string]any{}
			}
			existing, _ := tokens[req.Role].(map[string]any)
			scopes := req.Scopes
			if len(scopes) == 0 {
				scopes = getStringSlice(existing, "scopes")
			}
			if len(scopes) == 0 {
				scopes = getStringSlice(item, "approved_scopes")
			}
			if !scopesAllow(scopes, getStringSlice(item, "approved_scopes")) {
				return nil, nil, fmt.Errorf("invalid scopes for role")
			}
			tok, err := randomToken()
			if err != nil {
				return nil, nil, err
			}
			entry := map[string]any{
				"token":         tok,
				"role":          req.Role,
				"scopes":        scopes,
				"created_at_ms": now,
				"rotated_at_ms": now,
			}
			if existing != nil {
				if created := getInt64(existing, "created_at_ms"); created > 0 {
					entry["created_at_ms"] = created
				}
				if last := getInt64(existing, "last_used_at_ms"); last > 0 {
					entry["last_used_at_ms"] = last
				}
			}
			tokens[req.Role] = entry
			item["tokens"] = tokens
			pairing["device_paired"] = paired
			return pairing, map[string]any{"device_id": req.DeviceID, "role": req.Role, "token": tok, "scopes": scopes, "rotated_at_ms": now}, nil
		}
		return nil, nil, state.ErrNotFound
	})
}

func applyDeviceTokenRevoke(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.DeviceTokenRevokeRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		now := time.Now().UnixMilli()
		paired := toRecordSlice(pairing["device_paired"])
		for _, item := range paired {
			if getString(item, "device_id") != req.DeviceID {
				continue
			}
			tokens, _ := item["tokens"].(map[string]any)
			if tokens == nil {
				return nil, nil, state.ErrNotFound
			}
			tok, ok := tokens[req.Role].(map[string]any)
			if !ok {
				return nil, nil, state.ErrNotFound
			}
			tok["revoked_at_ms"] = now
			tokens[req.Role] = tok
			item["tokens"] = tokens
			pairing["device_paired"] = paired
			return pairing, map[string]any{"device_id": req.DeviceID, "role": req.Role, "revoked_at_ms": now}, nil
		}
		return nil, nil, state.ErrNotFound
	})
}

func applyNodeList(configState *runtimeConfigStore, req methods.NodeListRequest) (map[string]any, error) {
	pairing := pairingData(configState.Get())
	nodes := toRecordSlice(pairing["node_paired"])
	sortRecordsByKeyDesc(nodes, "approved_at_ms")
	if req.Limit > 0 && len(nodes) > req.Limit {
		nodes = nodes[:req.Limit]
	}
	redacted := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		redacted = append(redacted, redactNodeForList(node))
	}
	return map[string]any{"nodes": redacted, "count": len(redacted)}, nil
}

func applyNodeDescribe(configState *runtimeConfigStore, req methods.NodeDescribeRequest) (map[string]any, error) {
	pairing := pairingData(configState.Get())
	for _, node := range toRecordSlice(pairing["node_paired"]) {
		if getString(node, "node_id") == req.NodeID {
			return map[string]any{"node": redactNodeForList(node), "status": "paired"}, nil
		}
	}
	for _, node := range toRecordSlice(pairing["node_pending"]) {
		if getString(node, "node_id") == req.NodeID {
			return map[string]any{"node": redactNodeForList(node), "status": "pending"}, nil
		}
	}
	return nil, state.ErrNotFound
}

func applyNodeRename(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.NodeRenameRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		updated := false
		paired := toRecordSlice(pairing["node_paired"])
		for _, node := range paired {
			if getString(node, "node_id") == req.NodeID {
				node["display_name"] = req.Name
				updated = true
			}
		}
		pending := toRecordSlice(pairing["node_pending"])
		for _, node := range pending {
			if getString(node, "node_id") == req.NodeID {
				node["display_name"] = req.Name
				updated = true
			}
		}
		if !updated {
			return nil, nil, state.ErrNotFound
		}
		pairing["node_paired"] = paired
		pairing["node_pending"] = pending
		return pairing, map[string]any{"ok": true, "node_id": req.NodeID, "name": req.Name}, nil
	})
}

func applyNodeCanvasCapabilityRefresh(configState *runtimeConfigStore, req methods.NodeCanvasCapabilityRefreshRequest) (map[string]any, error) {
	pairing := pairingData(configState.Get())
	for _, node := range toRecordSlice(pairing["node_paired"]) {
		if getString(node, "node_id") == req.NodeID {
			caps := getStringSlice(node, "caps")
			return map[string]any{"ok": true, "node_id": req.NodeID, "caps": caps, "refreshed_at_ms": time.Now().UnixMilli()}, nil
		}
	}
	return nil, state.ErrNotFound
}
