package grasp

import (
	"strings"
	"testing"
)

func TestValidateRepoAddr(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		wantErr string // empty = expect nil
	}{
		{
			name:    "valid",
			addr:    "30617:cdee943cbb19c51ab847a66d5d774373aa9f63d287246bb59b0827fa5e637400:swarmstr",
			wantErr: "",
		},
		{
			name:    "empty",
			addr:    "",
			wantErr: "repo_addr is empty",
		},
		{
			name:    "one part",
			addr:    "30617",
			wantErr: "1 colon-separated parts, expected 3",
		},
		{
			name:    "two parts",
			addr:    "30617:cdee943cbb19c51ab847a66d5d774373aa9f63d287246bb59b0827fa5e637400",
			wantErr: "2 colon-separated parts, expected 3",
		},
		{
			name:    "wrong kind",
			addr:    "1:cdee943cbb19c51ab847a66d5d774373aa9f63d287246bb59b0827fa5e637400:swarmstr",
			wantErr: "kind prefix is \"1\"",
		},
		{
			name:    "short pubkey",
			addr:    "30617:abc123:swarmstr",
			wantErr: "6 chars, expected 64",
		},
		{
			name:    "uppercase hex",
			addr:    "30617:CDEE943CBB19C51AB847A66D5D774373AA9F63D287246BB59B0827FA5E637400:swarmstr",
			wantErr: "non-hex character",
		},
		{
			name:    "empty repo id",
			addr:    "30617:cdee943cbb19c51ab847a66d5d774373aa9f63d287246bb59b0827fa5e637400:",
			wantErr: "repo-id (d-tag) is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRepoAddr(tt.addr)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestExtractAddrPubkey(t *testing.T) {
	got := extractAddrPubkey("30617:cdee943cbb19c51ab847a66d5d774373aa9f63d287246bb59b0827fa5e637400:swarmstr")
	if got != "cdee943cbb19c51ab847a66d5d774373aa9f63d287246bb59b0827fa5e637400" {
		t.Errorf("unexpected pubkey: %s", got)
	}
	if extractAddrPubkey("") != "" {
		t.Error("expected empty for empty input")
	}
}
