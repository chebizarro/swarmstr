package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	FrameTypeRequest  = "req"
	FrameTypeResponse = "res"
	FrameTypeEvent    = "event"
)

type ConnectClient struct {
	ID              string `json:"id"`
	DisplayName     string `json:"displayName,omitempty"`
	Version         string `json:"version"`
	Platform        string `json:"platform"`
	DeviceFamily    string `json:"deviceFamily,omitempty"`
	ModelIdentifier string `json:"modelIdentifier,omitempty"`
	Mode            string `json:"mode"`
	InstanceID      string `json:"instanceId,omitempty"`
}

type ConnectDevice struct {
	ID        string `json:"id"`
	PublicKey string `json:"publicKey"`
	Signature string `json:"signature"`
	SignedAt  int64  `json:"signedAt"`
	Nonce     string `json:"nonce"`
}

type ConnectAuth struct {
	Token       string `json:"token,omitempty"`
	DeviceToken string `json:"deviceToken,omitempty"`
	Password    string `json:"password,omitempty"`
	Nonce       string `json:"nonce,omitempty"`
}

type ConnectParams struct {
	MinProtocol int             `json:"minProtocol"`
	MaxProtocol int             `json:"maxProtocol"`
	Client      ConnectClient   `json:"client"`
	Caps        []string        `json:"caps,omitempty"`
	Commands    []string        `json:"commands,omitempty"`
	Permissions map[string]bool `json:"permissions,omitempty"`
	PathEnv     string          `json:"pathEnv,omitempty"`
	Role        string          `json:"role,omitempty"`
	Scopes      []string        `json:"scopes,omitempty"`
	Device      *ConnectDevice  `json:"device,omitempty"`
	Auth        *ConnectAuth    `json:"auth,omitempty"`
	Locale      string          `json:"locale,omitempty"`
	UserAgent   string          `json:"userAgent,omitempty"`
}

type ServerInfo struct {
	Version string `json:"version"`
	ConnID  string `json:"connId"`
}

type FeatureSet struct {
	Methods []string `json:"methods"`
	Events  []string `json:"events"`
}

type StateVersion struct {
	Presence int `json:"presence"`
	Health   int `json:"health"`
}

type Snapshot struct {
	Presence     []PresenceEntry `json:"presence"`
	Health       any             `json:"health"`
	StateVersion StateVersion    `json:"stateVersion"`
	UptimeMS     int64           `json:"uptimeMs"`
	ConfigPath   string          `json:"configPath,omitempty"`
	StateDir     string          `json:"stateDir,omitempty"`
	AuthMode     string          `json:"authMode,omitempty"`
}

type PresenceEntry struct {
	Host             string   `json:"host,omitempty"`
	IP               string   `json:"ip,omitempty"`
	Version          string   `json:"version,omitempty"`
	Platform         string   `json:"platform,omitempty"`
	DeviceFamily     string   `json:"deviceFamily,omitempty"`
	ModelIdentifier  string   `json:"modelIdentifier,omitempty"`
	Mode             string   `json:"mode,omitempty"`
	LastInputSeconds int      `json:"lastInputSeconds,omitempty"`
	Reason           string   `json:"reason,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	Text             string   `json:"text,omitempty"`
	TS               int64    `json:"ts"`
	DeviceID         string   `json:"deviceId,omitempty"`
	Roles            []string `json:"roles,omitempty"`
	Scopes           []string `json:"scopes,omitempty"`
	InstanceID       string   `json:"instanceId,omitempty"`
}

type HelloOK struct {
	Type          string      `json:"type"`
	Protocol      int         `json:"protocol"`
	Server        ServerInfo  `json:"server"`
	Features      FeatureSet  `json:"features"`
	Snapshot      Snapshot    `json:"snapshot"`
	CanvasHostURL string      `json:"canvasHostUrl,omitempty"`
	Auth          *HelloAuth  `json:"auth,omitempty"`
	Policy        HelloPolicy `json:"policy"`
}

type HelloAuth struct {
	DeviceToken string   `json:"deviceToken"`
	Role        string   `json:"role"`
	Scopes      []string `json:"scopes"`
	IssuedAtMS  int64    `json:"issuedAtMs,omitempty"`
}

type HelloPolicy struct {
	MaxPayload       int `json:"maxPayload"`
	MaxBufferedBytes int `json:"maxBufferedBytes"`
	TickIntervalMS   int `json:"tickIntervalMs"`
}

type ErrorShape struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	Details      any    `json:"details,omitempty"`
	Retryable    bool   `json:"retryable,omitempty"`
	RetryAfterMS int64  `json:"retryAfterMs,omitempty"`
}

type RequestFrame struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type ResponseFrame struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	OK      bool            `json:"ok"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   *ErrorShape     `json:"error,omitempty"`
}

type EventFrame struct {
	Type         string          `json:"type"`
	Event        string          `json:"event"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	Seq          *int64          `json:"seq,omitempty"`
	StateVersion *StateVersion   `json:"stateVersion,omitempty"`
}

type frameEnvelope struct {
	Type string `json:"type"`
}

func ParseGatewayFrame(raw []byte) (any, error) {
	var env frameEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("invalid frame envelope")
	}
	switch env.Type {
	case FrameTypeRequest:
		var f RequestFrame
		if err := decodeStrict(raw, &f); err != nil {
			return nil, fmt.Errorf("invalid request frame")
		}
		if err := f.Validate(); err != nil {
			return nil, err
		}
		return f, nil
	case FrameTypeResponse:
		var f ResponseFrame
		if err := decodeStrict(raw, &f); err != nil {
			return nil, fmt.Errorf("invalid response frame")
		}
		if err := f.Validate(); err != nil {
			return nil, err
		}
		return f, nil
	case FrameTypeEvent:
		var f EventFrame
		if err := decodeStrict(raw, &f); err != nil {
			return nil, fmt.Errorf("invalid event frame")
		}
		if err := f.Validate(); err != nil {
			return nil, err
		}
		return f, nil
	default:
		return nil, fmt.Errorf("unknown frame type %q", env.Type)
	}
}

func (r RequestFrame) Validate() error {
	if strings.TrimSpace(r.Type) != FrameTypeRequest {
		return fmt.Errorf("request frame type must be %q", FrameTypeRequest)
	}
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("request frame id is required")
	}
	if strings.TrimSpace(r.Method) == "" {
		return fmt.Errorf("request frame method is required")
	}
	return nil
}

func (r ResponseFrame) Validate() error {
	if strings.TrimSpace(r.Type) != FrameTypeResponse {
		return fmt.Errorf("response frame type must be %q", FrameTypeResponse)
	}
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("response frame id is required")
	}
	if !r.OK && r.Error == nil {
		return fmt.Errorf("response frame error is required when ok=false")
	}
	if r.Error != nil {
		if strings.TrimSpace(r.Error.Code) == "" || strings.TrimSpace(r.Error.Message) == "" {
			return fmt.Errorf("response frame error code/message are required")
		}
	}
	return nil
}

func (e EventFrame) Validate() error {
	if strings.TrimSpace(e.Type) != FrameTypeEvent {
		return fmt.Errorf("event frame type must be %q", FrameTypeEvent)
	}
	if strings.TrimSpace(e.Event) == "" {
		return fmt.Errorf("event frame event is required")
	}
	return nil
}

func (c ConnectParams) Validate() error {
	if _, err := NegotiateProtocol(c.MinProtocol, c.MaxProtocol); err != nil {
		return err
	}
	if strings.TrimSpace(c.Client.ID) == "" {
		return fmt.Errorf("connect.client.id is required")
	}
	if strings.TrimSpace(c.Client.Version) == "" {
		return fmt.Errorf("connect.client.version is required")
	}
	if strings.TrimSpace(c.Client.Platform) == "" {
		return fmt.Errorf("connect.client.platform is required")
	}
	if strings.TrimSpace(c.Client.Mode) == "" {
		return fmt.Errorf("connect.client.mode is required")
	}
	return nil
}

func decodeStrict(raw []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing data")
	}
	return nil
}
