package methods

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type AuditRunInspectRequest struct {
	RunID           string `json:"run_id,omitempty"`
	ExecutionID     string `json:"execution_id,omitempty"`
	ExecutionCursor string `json:"execution_cursor,omitempty"`
	ExecutionLimit  int    `json:"execution_limit,omitempty"`
	DecisionCursor  string `json:"decision_cursor,omitempty"`
	DecisionLimit   int    `json:"decision_limit,omitempty"`
}

func auditCursor(value, field string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(value)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("audit.run.inspect: %s must be a non-negative integer cursor", field)
	}
	return offset, nil
}

func (r AuditRunInspectRequest) Normalize() (AuditRunInspectRequest, error) {
	r.RunID = strings.TrimSpace(r.RunID)
	r.ExecutionID = strings.TrimSpace(r.ExecutionID)
	r.ExecutionCursor = strings.TrimSpace(r.ExecutionCursor)
	r.DecisionCursor = strings.TrimSpace(r.DecisionCursor)
	if (r.RunID == "") == (r.ExecutionID == "") {
		return r, fmt.Errorf("audit.run.inspect: exactly one of runId or executionId is required")
	}
	for field, value := range map[string]string{"runId": r.RunID, "executionId": r.ExecutionID} {
		if len(value) > 256 {
			return r, fmt.Errorf("audit.run.inspect: %s exceeds 256 bytes", field)
		}
	}
	if r.ExecutionID != "" && (r.ExecutionCursor != "" || r.ExecutionLimit != 0) {
		return r, fmt.Errorf("audit.run.inspect: execution paging is only valid with runId")
	}
	if _, err := auditCursor(r.ExecutionCursor, "executionCursor"); err != nil {
		return r, err
	}
	if _, err := auditCursor(r.DecisionCursor, "decisionCursor"); err != nil {
		return r, err
	}
	if r.RunID != "" {
		if r.ExecutionLimit == 0 {
			r.ExecutionLimit = 50
		}
		if r.ExecutionLimit < 1 || r.ExecutionLimit > 50 {
			return r, fmt.Errorf("audit.run.inspect: executionLimit must be between 1 and 50")
		}
	}
	if r.DecisionLimit == 0 {
		r.DecisionLimit = 50
	}
	if r.DecisionLimit < 1 || r.DecisionLimit > 100 {
		return r, fmt.Errorf("audit.run.inspect: decisionLimit must be between 1 and 100")
	}
	return r, nil
}

func (r AuditRunInspectRequest) DecisionOffset() int {
	offset, _ := auditCursor(r.DecisionCursor, "decisionCursor")
	return offset
}

func DecodeAuditRunInspectParams(params json.RawMessage) (AuditRunInspectRequest, error) {
	return decodeMethodParams[AuditRunInspectRequest](params)
}
