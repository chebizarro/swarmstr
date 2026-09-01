package methods

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type SessionsUsageRequest struct {
	Key           string `json:"key,omitempty"`
	AgentID       string `json:"agent_id,omitempty"`
	AgentScope    string `json:"agentScope,omitempty"`
	StartDate     string `json:"startDate,omitempty"`
	EndDate       string `json:"endDate,omitempty"`
	Mode          string `json:"mode,omitempty"`
	Range         string `json:"range,omitempty"`
	GroupBy       string `json:"groupBy,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	IncludeWeight bool   `json:"includeContextWeight,omitempty"`
}

type SessionsUsageTimeseriesRequest struct {
	Key string `json:"key"`
}

type SessionsUsageLogsRequest struct {
	Key   string `json:"key"`
	Limit int    `json:"limit,omitempty"`
}

func validateUsageDate(value, field string) error {
	if value == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return fmt.Errorf("%s must use YYYY-MM-DD", field)
	}
	return nil
}

func (r SessionsUsageRequest) Normalize() (SessionsUsageRequest, error) {
	r.Key = strings.TrimSpace(r.Key)
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.AgentScope = strings.TrimSpace(strings.ToLower(r.AgentScope))
	r.StartDate = strings.TrimSpace(r.StartDate)
	r.EndDate = strings.TrimSpace(r.EndDate)
	r.Mode = strings.TrimSpace(strings.ToLower(r.Mode))
	r.Range = strings.TrimSpace(strings.ToLower(r.Range))
	r.GroupBy = strings.TrimSpace(strings.ToLower(r.GroupBy))
	if r.AgentScope != "" && r.AgentScope != "all" {
		return r, fmt.Errorf("sessions.usage: agentScope must be all when set")
	}
	if r.AgentScope == "all" && (r.Key != "" || r.AgentID != "") {
		return r, fmt.Errorf("sessions.usage: agentScope=all cannot be combined with key or agentId")
	}
	if err := validateUsageDate(r.StartDate, "startDate"); err != nil {
		return r, fmt.Errorf("sessions.usage: %w", err)
	}
	if err := validateUsageDate(r.EndDate, "endDate"); err != nil {
		return r, fmt.Errorf("sessions.usage: %w", err)
	}
	if r.Mode != "" && r.Mode != "utc" && r.Mode != "gateway" {
		return r, fmt.Errorf("sessions.usage: mode must be utc or gateway")
	}
	if r.Range != "" {
		switch r.Range {
		case "7d", "30d", "90d", "1y", "all":
		default:
			return r, fmt.Errorf("sessions.usage: unsupported range %q", r.Range)
		}
	}
	if r.GroupBy != "" && r.GroupBy != "instance" {
		return r, fmt.Errorf("sessions.usage: only instance grouping is supported by Metiq transcript history")
	}
	if r.Limit <= 0 {
		r.Limit = 50
	}
	if r.Limit > 500 {
		r.Limit = 500
	}
	return r, nil
}

func (r SessionsUsageTimeseriesRequest) Normalize() (SessionsUsageTimeseriesRequest, error) {
	r.Key = strings.TrimSpace(r.Key)
	if r.Key == "" {
		return r, fmt.Errorf("sessions.usage.timeseries: key is required")
	}
	return r, nil
}

func (r SessionsUsageLogsRequest) Normalize() (SessionsUsageLogsRequest, error) {
	r.Key = strings.TrimSpace(r.Key)
	if r.Key == "" {
		return r, fmt.Errorf("sessions.usage.logs: key is required")
	}
	if r.Limit <= 0 {
		r.Limit = 200
	}
	if r.Limit > 1000 {
		r.Limit = 1000
	}
	return r, nil
}

func DecodeSessionsUsageParams(params json.RawMessage) (SessionsUsageRequest, error) {
	return decodeMethodParams[SessionsUsageRequest](params)
}

func DecodeSessionsUsageTimeseriesParams(params json.RawMessage) (SessionsUsageTimeseriesRequest, error) {
	return decodeMethodParams[SessionsUsageTimeseriesRequest](params)
}

func DecodeSessionsUsageLogsParams(params json.RawMessage) (SessionsUsageLogsRequest, error) {
	return decodeMethodParams[SessionsUsageLogsRequest](params)
}
