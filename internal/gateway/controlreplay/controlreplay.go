package controlreplay

import "strings"

type Policy int

const (
	None Policy = iota
	EventOnly
	EventAndRequest
)

func MethodPolicy(method string) Policy {
	switch strings.TrimSpace(method) {
	case "secrets.resolve":
		return None
	case "supportedmethods",
		"health",
		"doctor.memory.status",
		"status.get",
		"status",
		"usage.status",
		"usage.cost",
		"channels.status",
		"channels.list",
		"agent.identity.get",
		"gateway.identity.get",
		"agents.list",
		"agents.active",
		"agents.files.list",
		"agents.files.get",
		"models.list",
		"tools.catalog",
		"tools.profile.get",
		"skills.status",
		"skills.bins",
		"plugins.registry.list",
		"plugins.registry.get",
		"plugins.registry.search",
		"node.pair.list",
		"device.pair.list",
		"node.list",
		"node.describe",
		"canvas.get",
		"canvas.list",
		"cron.list",
		"cron.status",
		"cron.runs",
		"exec.approvals.get",
		"exec.approvals.node.get",
		"mcp.list",
		"mcp.get",
		"wizard.status",
		"voicewake.get",
		"tts.status",
		"tts.providers",
		"hooks.list",
		"hooks.info",
		"hooks.check",
		"config.get",
		"config.schema",
		"config.schema.lookup",
		"relay.policy.get",
		"security.audit",
		"list.get",
		"chat.history",
		"session.get",
		"sessions.list",
		"sessions.preview",
		"tasks.get",
		"tasks.list",
		"tasks.doctor",
		"tasks.summary",
		"tasks.audit_export",
		"tasks.trace",
		"acp.peers":
		return EventAndRequest
	default:
		return EventOnly
	}
}
