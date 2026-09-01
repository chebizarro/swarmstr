package methods

import (
	"encoding/json"
	"fmt"
	"strings"
)

// HooksStatusRequest selects the agent workspace whose live hook registrations
// should be projected. An empty agent_id resolves to the main agent.
type HooksStatusRequest struct {
	AgentID string `json:"agent_id,omitempty"`
}

func (r HooksStatusRequest) Normalize() (HooksStatusRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	if len(r.AgentID) > 128 {
		return r, fmt.Errorf("hooks.status: agentId exceeds 128 bytes")
	}
	return r, nil
}

func DecodeHooksStatusParams(params json.RawMessage) (HooksStatusRequest, error) {
	return decodeMethodParams[HooksStatusRequest](params)
}
