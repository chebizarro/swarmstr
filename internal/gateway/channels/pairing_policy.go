package channels

import (
	"strings"

	"metiq/internal/plugins/sdk"
	"metiq/internal/policy"
	"metiq/internal/store/state"
)

// PairingInboundDecision is the typed result used by extension ingress. A
// request is created only when the account explicitly opts into pairing and
// the adapter/config proves the conversation is direct.
type PairingInboundDecision struct {
	RequestPairing bool
	Reason         string
}

func EvaluatePairingInbound(account ResolvedChannelAccount, msg sdk.InboundChannelMessage, cfg state.ConfigDoc) PairingInboundDecision {
	dmPolicy, _ := account.Config["dm_policy"].(string)
	if !strings.EqualFold(strings.TrimSpace(dmPolicy), "pairing") {
		return PairingInboundDecision{Reason: "pairing policy disabled"}
	}
	if !isDirectPairingMessage(account.Config, msg) {
		return PairingInboundDecision{Reason: "direct message scope not proven"}
	}
	if decision := policy.EvaluateGroupMessage(msg.SenderID, account.AllowFrom, cfg); decision.Allowed {
		return PairingInboundDecision{Reason: "sender already allowed"}
	}
	return PairingInboundDecision{RequestPairing: true, Reason: "unrecognized direct sender"}
}

func isDirectPairingMessage(config map[string]any, msg sdk.InboundChannelMessage) bool {
	for _, key := range []string{"pairing_scope", "conversation_scope", "message_scope"} {
		value, _ := config[key].(string)
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "dm", "direct", "private":
			return true
		case "group", "channel", "room":
			return false
		}
	}
	if direct, _ := config["dm_only"].(bool); direct {
		return true
	}
	thread := strings.ToLower(strings.TrimSpace(msg.ThreadID))
	if strings.HasPrefix(thread, "dm:") || strings.HasPrefix(thread, "c2c:") || strings.HasPrefix(strings.ToLower(strings.TrimSpace(msg.ChannelID)), "irc-dm:") {
		return true
	}
	return false
}
