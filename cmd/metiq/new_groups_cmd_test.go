package main

import (
	"strings"
	"testing"
)

func TestNewGatewayCommandGroupsRegistered(t *testing.T) {
	reg := newCommandRegistry("")
	for _, name := range []string{"acp", "commitments", "sandbox", "message", "send", "transcripts", "system"} {
		cmd, ok := reg.lookup(name)
		if !ok {
			t.Fatalf("registry missing %q", name)
		}
		if cmd.Group != "Gateway Passthrough" {
			t.Fatalf("%s group = %q, want Gateway Passthrough", name, cmd.Group)
		}
		if _, err := cmd.handler(); err != nil {
			t.Fatalf("%s handler: %v", name, err)
		}
	}
}

func TestNewGatewayCommandArgParsing(t *testing.T) {
	tests := []struct {
		name string
		run  func([]string) error
		args []string
		want string
	}{
		{name: "acp dispatch requires target and instructions", run: runACP, args: []string{"dispatch"}, want: "acp dispatch"},
		{name: "acp pipeline requires params", run: runACP, args: []string{"pipeline"}, want: "acp pipeline"},
		{name: "commitments add requires text", run: runCommitments, args: []string{"add"}, want: "commitments add"},
		{name: "sandbox run requires command", run: runSandbox, args: []string{"run"}, want: "sandbox run"},
		{name: "message send requires destination and text", run: runMessage, args: []string{"send"}, want: "message send"},
		{name: "send requires destination and text", run: runSend, args: nil, want: "message send"},
		{name: "transcripts rejects unknown subcommand", run: runTranscripts, args: []string{"delete"}, want: "unknown transcripts sub-command"},
		{name: "system rejects unknown subcommand", run: runSystem, args: []string{"events"}, want: "unknown system sub-command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestNewGatewayCommandParamParser(t *testing.T) {
	params, err := metiqJSONOrKeyValueParams([]string{"target_pubkey=abc", "instructions=do-it"})
	if err != nil {
		t.Fatalf("key=value parse: %v", err)
	}
	if params["target_pubkey"] != "abc" || params["instructions"] != "do-it" {
		t.Fatalf("unexpected key=value params: %#v", params)
	}
	params, err = metiqJSONOrKeyValueParams([]string{"{\"steps\":[{\"instructions\":\"one\"}]}"})
	if err != nil {
		t.Fatalf("json parse: %v", err)
	}
	if _, ok := params["steps"]; !ok {
		t.Fatalf("expected steps param: %#v", params)
	}
}
