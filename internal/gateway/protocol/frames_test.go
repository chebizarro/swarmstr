package protocol

import (
	"strings"
	"testing"
)

func TestParseGatewayFrame_Request(t *testing.T) {
	raw := []byte(`{"type":"req","id":"1","method":"status.get","params":{"x":1}}`)
	frame, err := ParseGatewayFrame(raw)
	if err != nil {
		t.Fatalf("parse frame: %v", err)
	}
	req, ok := frame.(RequestFrame)
	if !ok {
		t.Fatalf("unexpected frame type %T", frame)
	}
	if req.Method != "status.get" {
		t.Fatalf("method = %q, want status.get", req.Method)
	}
}

func TestParseGatewayFrame_RejectsUnknownFields(t *testing.T) {
	raw := []byte(`{"type":"req","id":"1","method":"status.get","extra":true}`)
	_, err := ParseGatewayFrame(raw)
	if err == nil {
		t.Fatal("expected strict decode error")
	}
}

func TestParseGatewayFrame_ResponseRequiresErrorWhenNotOK(t *testing.T) {
	raw := []byte(`{"type":"res","id":"1","ok":false}`)
	_, err := ParseGatewayFrame(raw)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "error is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseGatewayFrame_Event(t *testing.T) {
	raw := []byte(`{"type":"event","event":"presence.updated","seq":2}`)
	frame, err := ParseGatewayFrame(raw)
	if err != nil {
		t.Fatalf("parse frame: %v", err)
	}
	event, ok := frame.(EventFrame)
	if !ok {
		t.Fatalf("unexpected frame type %T", frame)
	}
	if event.Event != "presence.updated" {
		t.Fatalf("event = %q", event.Event)
	}
}

func TestConnectParamsValidate(t *testing.T) {
	params := ConnectParams{
		MinProtocol: 1,
		MaxProtocol: 3,
		Client: ConnectClient{
			ID:       "metiq-cli",
			Version:  "0.1.0",
			Platform: "darwin",
			Mode:     "local",
		},
	}
	if err := params.Validate(); err != nil {
		t.Fatalf("validate error: %v", err)
	}

	params.Client.ID = ""
	if err := params.Validate(); err == nil {
		t.Fatal("expected required id validation error")
	}
}
