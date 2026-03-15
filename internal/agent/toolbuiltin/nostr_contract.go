package toolbuiltin

import (
	"encoding/json"
	"fmt"
	"strings"
)

func nostrToolErr(tool, code, message string, context map[string]any) error {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		tool = "nostr_tool"
	}
	code = strings.TrimSpace(code)
	if code == "" {
		code = "operation_failed"
	}
	payload := map[string]any{
		"tool":    tool,
		"code":    code,
		"message": strings.TrimSpace(message),
	}
	if len(context) > 0 {
		payload["context"] = context
	}
	raw, _ := json.Marshal(payload)
	return fmt.Errorf("%s_error:%s", tool, string(raw))
}

func mapNostrPublishErr(tool string, err error, context map[string]any) error {
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(err.Error())
	lmsg := strings.ToLower(msg)
	switch {
	case strings.Contains(lmsg, "sign event"), strings.Contains(lmsg, "sign:"):
		return nostrToolErr(tool, "sign_failed", msg, context)
	case strings.Contains(lmsg, "no relays configured"), strings.Contains(lmsg, "no relay"):
		return nostrToolErr(tool, "no_relays", "no relays configured or relay publish/query rejected", context)
	case strings.Contains(lmsg, "required"), strings.Contains(lmsg, "missing"):
		return nostrToolErr(tool, "invalid_input", msg, context)
	default:
		return nostrToolErr(tool, "operation_failed", msg, context)
	}
}

func nostrWriteSuccessEnvelope(tool, eventID string, kind int, targets map[string]any, meta map[string]any, compat map[string]any) string {
	out := map[string]any{
		"ok":       true,
		"tool":     strings.TrimSpace(tool),
		"event_id": strings.TrimSpace(eventID),
		"kind":     kind,
	}
	if len(targets) > 0 {
		out["targets"] = targets
	}
	if len(meta) > 0 {
		out["meta"] = meta
	}
	for k, v := range compat {
		if _, exists := out[k]; exists {
			continue
		}
		out[k] = v
	}
	raw, _ := json.Marshal(out)
	return string(raw)
}

