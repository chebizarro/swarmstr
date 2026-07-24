package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

type CronListRequest struct {
	Limit int `json:"limit,omitempty"`
}

type CronStatusRequest struct {
	ID string `json:"id"`
}

type CronJobSelectorRequest struct {
	ID    string `json:"id,omitempty"`
	JobID string `json:"jobId,omitempty"`
}

type CronScratchSetRequest struct {
	ID               string          `json:"id,omitempty"`
	JobID            string          `json:"jobId,omitempty"`
	Content          json.RawMessage `json:"content"`
	ExpectedRevision *int            `json:"expectedRevision,omitempty"`
	ContentValue     string          `json:"-"`
	Clear            bool            `json:"-"`
}

type CronAddRequest struct {
	ID       string          `json:"id,omitempty"`
	Schedule string          `json:"schedule"`
	Method   string          `json:"method"`
	Params   json.RawMessage `json:"params,omitempty"`
	Enabled  *bool           `json:"enabled,omitempty"`
}

type CronUpdateRequest struct {
	ID       string          `json:"id"`
	Schedule string          `json:"schedule,omitempty"`
	Method   string          `json:"method,omitempty"`
	Params   json.RawMessage `json:"params,omitempty"`
	Enabled  *bool           `json:"enabled,omitempty"`
}

type CronRemoveRequest struct {
	ID string `json:"id"`
}

type CronRunRequest struct {
	ID string `json:"id"`
}

type CronRunsRequest struct {
	ID    string `json:"id,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

func (r CronListRequest) Normalize() (CronListRequest, error) {
	r.Limit = normalizeLimit(r.Limit, 100, 500)
	return r, nil
}

func (r CronStatusRequest) Normalize() (CronStatusRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		return r, fmt.Errorf("id is required")
	}
	return r, nil
}

func (r CronJobSelectorRequest) Normalize() (CronJobSelectorRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	r.JobID = strings.TrimSpace(r.JobID)
	if r.ID == "" {
		r.ID = r.JobID
	}
	if r.ID == "" {
		return r, fmt.Errorf("id or jobId is required")
	}
	if r.JobID != "" && r.JobID != r.ID {
		return r, fmt.Errorf("id and jobId must match")
	}
	return r, nil
}

func (r CronScratchSetRequest) Normalize() (CronScratchSetRequest, error) {
	selector, err := (CronJobSelectorRequest{ID: r.ID, JobID: r.JobID}).Normalize()
	if err != nil {
		return r, err
	}
	r.ID = selector.ID
	r.JobID = selector.JobID
	if r.ExpectedRevision != nil && *r.ExpectedRevision < 0 {
		return r, fmt.Errorf("expectedRevision must be non-negative")
	}
	raw := bytes.TrimSpace(r.Content)
	if len(raw) == 0 {
		return r, fmt.Errorf("content is required")
	}
	if bytes.Equal(raw, []byte("null")) {
		r.Clear = true
		return r, nil
	}
	if err := json.Unmarshal(raw, &r.ContentValue); err != nil {
		return r, fmt.Errorf("content must be a string or null")
	}
	if len([]byte(r.ContentValue)) > 262144 {
		return r, fmt.Errorf("content exceeds 262144 bytes")
	}
	return r, nil
}

func (r CronAddRequest) Normalize() (CronAddRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	r.Schedule = strings.TrimSpace(r.Schedule)
	r.Method = strings.TrimSpace(r.Method)
	if r.Schedule == "" || r.Method == "" {
		return r, fmt.Errorf("schedule and method are required")
	}
	return r, nil
}

func (r CronUpdateRequest) Normalize() (CronUpdateRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	r.Schedule = strings.TrimSpace(r.Schedule)
	r.Method = strings.TrimSpace(r.Method)
	if r.ID == "" {
		return r, fmt.Errorf("id is required")
	}
	if r.Schedule == "" && r.Method == "" && len(r.Params) == 0 && r.Enabled == nil {
		return r, fmt.Errorf("at least one update field is required")
	}
	return r, nil
}

func (r CronRemoveRequest) Normalize() (CronRemoveRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		return r, fmt.Errorf("id is required")
	}
	return r, nil
}

func (r CronRunRequest) Normalize() (CronRunRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		return r, fmt.Errorf("id is required")
	}
	return r, nil
}

func (r CronRunsRequest) Normalize() (CronRunsRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	r.Limit = normalizeLimit(r.Limit, 50, 500)
	return r, nil
}

func DecodeCronListParams(params json.RawMessage) (CronListRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return CronListRequest{}, nil
	}
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return CronListRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 1 {
			return CronListRequest{}, fmt.Errorf("invalid params")
		}
		req := CronListRequest{}
		if len(arr) == 1 {
			switch v := arr[0].(type) {
			case float64:
				if math.Trunc(v) != v {
					return CronListRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			default:
				return CronListRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[CronListRequest](params)
}

func DecodeCronStatusParams(params json.RawMessage) (CronStatusRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return CronStatusRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return CronStatusRequest{}, fmt.Errorf("invalid params")
		}
		id, ok := arr[0].(string)
		if !ok {
			return CronStatusRequest{}, fmt.Errorf("invalid params")
		}
		return CronStatusRequest{ID: id}, nil
	}
	return decodeMethodParams[CronStatusRequest](params)
}

func DecodeCronGetParams(params json.RawMessage) (CronJobSelectorRequest, error) {
	return decodeMethodParams[CronJobSelectorRequest](params)
}

func DecodeCronScratchGetParams(params json.RawMessage) (CronJobSelectorRequest, error) {
	return decodeMethodParams[CronJobSelectorRequest](params)
}

func DecodeCronScratchSetParams(params json.RawMessage) (CronScratchSetRequest, error) {
	return decodeMethodParams[CronScratchSetRequest](params)
}

func DecodeCronAddParams(params json.RawMessage) (CronAddRequest, error) {
	return decodeMethodParams[CronAddRequest](params)
}

func DecodeCronUpdateParams(params json.RawMessage) (CronUpdateRequest, error) {
	return decodeMethodParams[CronUpdateRequest](params)
}

func DecodeCronRemoveParams(params json.RawMessage) (CronRemoveRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return CronRemoveRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return CronRemoveRequest{}, fmt.Errorf("invalid params")
		}
		id, ok := arr[0].(string)
		if !ok {
			return CronRemoveRequest{}, fmt.Errorf("invalid params")
		}
		return CronRemoveRequest{ID: id}, nil
	}
	return decodeMethodParams[CronRemoveRequest](params)
}

func DecodeCronRunParams(params json.RawMessage) (CronRunRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return CronRunRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return CronRunRequest{}, fmt.Errorf("invalid params")
		}
		id, ok := arr[0].(string)
		if !ok {
			return CronRunRequest{}, fmt.Errorf("invalid params")
		}
		return CronRunRequest{ID: id}, nil
	}
	return decodeMethodParams[CronRunRequest](params)
}

func DecodeCronRunsParams(params json.RawMessage) (CronRunsRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return CronRunsRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 2 {
			return CronRunsRequest{}, fmt.Errorf("invalid params")
		}
		req := CronRunsRequest{}
		if len(arr) > 0 {
			id, ok := arr[0].(string)
			if !ok {
				return CronRunsRequest{}, fmt.Errorf("invalid params")
			}
			req.ID = id
		}
		if len(arr) > 1 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return CronRunsRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			default:
				return CronRunsRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	if len(bytes.TrimSpace(params)) == 0 {
		return CronRunsRequest{}, nil
	}
	return decodeMethodParams[CronRunsRequest](params)
}
