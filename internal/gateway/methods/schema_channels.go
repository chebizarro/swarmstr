package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

type ChannelsStatusRequest struct {
	Probe     bool `json:"probe,omitempty"`
	TimeoutMS int  `json:"timeout_ms,omitempty"`
}

type ChannelsLogoutRequest struct {
	Channel   string `json:"channel"`
	AccountID string `json:"account_id,omitempty"`
}

// ChannelsJoinRequest joins a NIP-29 relay group or other channel.
// For NIP-29, GroupAddress has the form "<relayHost>'<groupID>".
type ChannelsJoinRequest struct {
	Type         string `json:"type"`          // "nip29-group"
	GroupAddress string `json:"group_address"` // relay'groupID
}

// ChannelsLeaveRequest leaves a previously joined channel.
type ChannelsLeaveRequest struct {
	ChannelID string `json:"channel_id"`
}

// ChannelsListRequest requests the list of joined channels.
type ChannelsListRequest struct{}

// ChannelsSendRequest sends a message to a joined channel.
type ChannelsSendRequest struct {
	ChannelID string `json:"channel_id"`
	Text      string `json:"text"`
}

type UsageCostRequest struct {
	StartDate string `json:"startDate,omitempty"`
	EndDate   string `json:"endDate,omitempty"`
	Days      int    `json:"days,omitempty"`
	Mode      string `json:"mode,omitempty"`
	UTCOffset string `json:"utcOffset,omitempty"`
}

type SendRequest struct {
	To             string   `json:"to"`
	Message        string   `json:"message,omitempty"`
	Text           string   `json:"text,omitempty"`
	MediaURL       string   `json:"mediaUrl,omitempty"`
	MediaURLs      []string `json:"mediaUrls,omitempty"`
	GifPlayback    *bool    `json:"gif_playback,omitempty"`
	Channel        string   `json:"channel,omitempty"`
	AccountID      string   `json:"account_id,omitempty"`
	AgentID        string   `json:"agent_id,omitempty"`
	ThreadID       string   `json:"thread_id,omitempty"`
	SessionKey     string   `json:"sessionKey,omitempty"`
	IdempotencyKey string   `json:"idempotencyKey,omitempty"`
}

// PollRequest represents a request to send a poll to a channel.
type PollRequest struct {
	To              string   `json:"to"`
	Question        string   `json:"question"`
	Options         []string `json:"options"`
	MaxSelections   int      `json:"max_selections,omitempty"`
	DurationSeconds int      `json:"duration_seconds,omitempty"`
	DurationHours   int      `json:"duration_hours,omitempty"`
	Silent          *bool    `json:"silent,omitempty"`
	IsAnonymous     *bool    `json:"is_anonymous,omitempty"`
	ThreadID        string   `json:"thread_id,omitempty"`
	Channel         string   `json:"channel,omitempty"`
	AccountID       string   `json:"account_id,omitempty"`
	IdempotencyKey  string   `json:"idempotencyKey,omitempty"`
}

// PollInput is the normalized poll data passed to channel adapters.
type PollInput struct {
	Question        string   `json:"question"`
	Options         []string `json:"options"`
	MaxSelections   int      `json:"max_selections"`
	DurationSeconds int      `json:"duration_seconds,omitempty"`
	DurationHours   int      `json:"duration_hours,omitempty"`
}

// PollResult is the result returned from a channel poll send.
type PollResult struct {
	MessageID      string `json:"messageId,omitempty"`
	Channel        string `json:"channel,omitempty"`
	PollID         string `json:"pollId,omitempty"`
	ChannelID      string `json:"channelId,omitempty"`
	ConversationID string `json:"conversationId,omitempty"`
}

func (r ChannelsStatusRequest) Normalize() (ChannelsStatusRequest, error) {
	r.TimeoutMS = normalizeLimit(r.TimeoutMS, 10_000, 60_000)
	return r, nil
}

func (r ChannelsLogoutRequest) Normalize() (ChannelsLogoutRequest, error) {
	r.Channel = strings.ToLower(strings.TrimSpace(r.Channel))
	if r.Channel == "" {
		return r, fmt.Errorf("channel is required")
	}
	return r, nil
}

func (r ChannelsJoinRequest) Normalize() (ChannelsJoinRequest, error) {
	r.Type = strings.ToLower(strings.TrimSpace(r.Type))
	r.GroupAddress = strings.TrimSpace(r.GroupAddress)
	if r.Type == "" {
		r.Type = "nip29-group"
	}
	if r.Type != "nip29-group" {
		return r, fmt.Errorf("unsupported channel type %q", r.Type)
	}
	if r.GroupAddress == "" {
		return r, fmt.Errorf("group_address is required")
	}
	return r, nil
}

func (r ChannelsLeaveRequest) Normalize() (ChannelsLeaveRequest, error) {
	r.ChannelID = strings.TrimSpace(r.ChannelID)
	if r.ChannelID == "" {
		return r, fmt.Errorf("channel_id is required")
	}
	return r, nil
}

func (r ChannelsListRequest) Normalize() (ChannelsListRequest, error) { return r, nil }

func (r ChannelsSendRequest) Normalize() (ChannelsSendRequest, error) {
	r.ChannelID = strings.TrimSpace(r.ChannelID)
	r.Text = strings.TrimSpace(r.Text)
	if r.ChannelID == "" {
		return r, fmt.Errorf("channel_id is required")
	}
	if r.Text == "" {
		return r, fmt.Errorf("text is required")
	}
	return r, nil
}

func (r UsageCostRequest) Normalize() (UsageCostRequest, error) {
	r.StartDate = strings.TrimSpace(r.StartDate)
	r.EndDate = strings.TrimSpace(r.EndDate)
	r.Mode = strings.TrimSpace(r.Mode)
	r.UTCOffset = strings.TrimSpace(r.UTCOffset)
	if r.Days < 0 {
		return r, fmt.Errorf("days must be >= 0")
	}
	return r, nil
}

func (r PollRequest) Normalize() (PollRequest, error) {
	r.To = strings.TrimSpace(r.To)
	r.Question = strings.TrimSpace(r.Question)
	r.Channel = strings.ToLower(strings.TrimSpace(r.Channel))
	r.AccountID = strings.TrimSpace(r.AccountID)
	r.ThreadID = strings.TrimSpace(r.ThreadID)
	r.IdempotencyKey = strings.TrimSpace(r.IdempotencyKey)
	if r.To == "" {
		return r, fmt.Errorf("to is required")
	}
	if r.Question == "" {
		return r, fmt.Errorf("question is required")
	}
	r.Options = compactStringSlice(r.Options)
	if len(r.Options) < 2 {
		return r, fmt.Errorf("at least 2 options are required")
	}
	if len(r.Options) > 12 {
		return r, fmt.Errorf("at most 12 options are allowed")
	}
	if r.MaxSelections < 0 {
		return r, fmt.Errorf("max_selections must be >= 0")
	}
	if r.MaxSelections > 12 {
		return r, fmt.Errorf("max_selections must be <= 12")
	}
	if r.DurationSeconds < 0 {
		return r, fmt.Errorf("duration_seconds must be >= 0")
	}
	if r.DurationSeconds > 604800 {
		return r, fmt.Errorf("duration_seconds must be <= 604800")
	}
	if r.DurationHours < 0 {
		return r, fmt.Errorf("duration_hours must be >= 0")
	}
	if r.DurationSeconds > 0 && r.DurationHours > 0 {
		return r, fmt.Errorf("duration_seconds and duration_hours are mutually exclusive")
	}
	if r.IdempotencyKey == "" {
		r.IdempotencyKey = fmt.Sprintf("poll-%d", time.Now().UnixNano())
	}
	return r, nil
}

func (r SendRequest) Normalize() (SendRequest, error) {
	r.To = strings.TrimSpace(r.To)
	r.Message = strings.TrimSpace(r.Message)
	r.Text = strings.TrimSpace(r.Text)
	r.MediaURL = strings.TrimSpace(r.MediaURL)
	r.Channel = strings.ToLower(strings.TrimSpace(r.Channel))
	r.IdempotencyKey = strings.TrimSpace(r.IdempotencyKey)
	if r.Message == "" && r.Text != "" {
		r.Message = r.Text
	}
	if r.To == "" {
		return r, fmt.Errorf("to is required")
	}
	if !isValidNostrIdentifier(r.To) {
		return r, fmt.Errorf("to must be a valid npub or hex pubkey")
	}
	if r.Channel != "" && r.Channel != "nostr" {
		return r, fmt.Errorf("unsupported channel: %s", r.Channel)
	}
	r.MediaURLs = compactStringSlice(r.MediaURLs)
	if r.Message == "" && r.MediaURL == "" && len(r.MediaURLs) == 0 {
		return r, fmt.Errorf("text or media is required")
	}
	if r.IdempotencyKey == "" {
		r.IdempotencyKey = fmt.Sprintf("send-%d", time.Now().UnixNano())
	}
	return r, nil
}

func DecodeChannelsStatusParams(params json.RawMessage) (ChannelsStatusRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ChannelsStatusRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 2 {
			return ChannelsStatusRequest{}, fmt.Errorf("invalid params")
		}
		req := ChannelsStatusRequest{}
		if len(arr) >= 1 {
			b, ok := arr[0].(bool)
			if !ok {
				return ChannelsStatusRequest{}, fmt.Errorf("invalid params")
			}
			req.Probe = b
		}
		if len(arr) == 2 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return ChannelsStatusRequest{}, fmt.Errorf("invalid params")
				}
				req.TimeoutMS = int(v)
			case int:
				req.TimeoutMS = v
			default:
				return ChannelsStatusRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[ChannelsStatusRequest](params)
}

func DecodeChannelsLogoutParams(params json.RawMessage) (ChannelsLogoutRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ChannelsLogoutRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return ChannelsLogoutRequest{}, fmt.Errorf("invalid params")
		}
		channel, ok := arr[0].(string)
		if !ok {
			return ChannelsLogoutRequest{}, fmt.Errorf("invalid params")
		}
		return ChannelsLogoutRequest{Channel: channel}, nil
	}
	return decodeMethodParams[ChannelsLogoutRequest](params)
}

func DecodeChannelsJoinParams(params json.RawMessage) (ChannelsJoinRequest, error) {
	return decodeMethodParams[ChannelsJoinRequest](params)
}

func DecodeChannelsLeaveParams(params json.RawMessage) (ChannelsLeaveRequest, error) {
	return decodeMethodParams[ChannelsLeaveRequest](params)
}

func DecodeChannelsListParams(params json.RawMessage) (ChannelsListRequest, error) {
	return ChannelsListRequest{}, nil
}

func DecodeChannelsSendParams(params json.RawMessage) (ChannelsSendRequest, error) {
	return decodeMethodParams[ChannelsSendRequest](params)
}

func DecodeUsageCostParams(params json.RawMessage) (UsageCostRequest, error) {
	if isJSONArray(params) {
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return UsageCostRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 1 {
			return UsageCostRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 {
			return UsageCostRequest{}, nil
		}
		var req UsageCostRequest
		if err := json.Unmarshal(arr[0], &req); err != nil {
			return UsageCostRequest{}, fmt.Errorf("invalid params")
		}
		return req, nil
	}
	if len(bytes.TrimSpace(params)) == 0 {
		return UsageCostRequest{}, nil
	}
	return decodeMethodParams[UsageCostRequest](params)
}

func DecodeSendParams(params json.RawMessage) (SendRequest, error) {
	return decodeMethodParams[SendRequest](params)
}

func DecodePollParams(params json.RawMessage) (PollRequest, error) {
	return decodeMethodParams[PollRequest](params)
}

// NormalizePollInput normalizes a PollInput, applying defaults and
// validating constraints. maxOptions can be 0 to skip the limit check.
func NormalizePollInput(input PollInput, maxOptions int) (PollInput, error) {
	question := strings.TrimSpace(input.Question)
	if question == "" {
		return PollInput{}, fmt.Errorf("poll question is required")
	}
	options := make([]string, 0, len(input.Options))
	for _, opt := range input.Options {
		opt = strings.TrimSpace(opt)
		if opt != "" {
			options = append(options, opt)
		}
	}
	if len(options) < 2 {
		return PollInput{}, fmt.Errorf("poll requires at least 2 options")
	}
	if maxOptions > 0 && len(options) > maxOptions {
		return PollInput{}, fmt.Errorf("poll supports at most %d options", maxOptions)
	}
	maxSel := input.MaxSelections
	if maxSel <= 0 {
		maxSel = 1
	}
	if maxSel > len(options) {
		return PollInput{}, fmt.Errorf("max_selections cannot exceed option count")
	}

	durSec := input.DurationSeconds
	if durSec < 0 {
		return PollInput{}, fmt.Errorf("duration_seconds must be >= 0")
	}
	durHours := input.DurationHours
	if durHours < 0 {
		return PollInput{}, fmt.Errorf("duration_hours must be >= 0")
	}
	if durSec > 0 && durHours > 0 {
		return PollInput{}, fmt.Errorf("duration_seconds and duration_hours are mutually exclusive")
	}

	return PollInput{
		Question:        question,
		Options:         options,
		MaxSelections:   maxSel,
		DurationSeconds: durSec,
		DurationHours:   durHours,
	}, nil
}
