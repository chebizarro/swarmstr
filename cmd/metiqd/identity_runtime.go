package main

import (
	"os"
	"strings"

	"metiq/internal/agent"
	toolbuiltin "metiq/internal/agent/toolbuiltin"
	"metiq/internal/store/state"
	"metiq/internal/workspace"
)

func setRuntimeIdentityInfo(cfg state.ConfigDoc, pubkey string) {
	toolbuiltin.SetIdentityInfo(resolveRuntimeIdentityInfo(cfg, pubkey))
}

func resolveRuntimeIdentityInfo(cfg state.ConfigDoc, pubkey string) toolbuiltin.IdentityInfo {
	identityName := ""
	identityModel := strings.TrimSpace(os.Getenv("METIQ_AGENT_PROVIDER"))
	for _, ag := range cfg.Agents {
		id := defaultAgentID(ag.ID)
		if id != "main" {
			continue
		}
		if name := strings.TrimSpace(ag.Name); name != "" {
			identityName = name
		}
		if model := strings.TrimSpace(ag.Model); model != "" {
			identityModel = model
		}
		break
	}
	if identityName == "" {
		wsDir := workspace.ResolveWorkspaceDir(cfg, "main")
		identityName = strings.TrimSpace(agent.ResolveWorkspaceIdentityName(wsDir))
	}
	if identityName == "" {
		identityName = "main"
	}
	if identityModel == "" {
		identityModel = strings.TrimSpace(cfg.Agent.DefaultModel)
	}
	return toolbuiltin.IdentityInfo{
		Name:   identityName,
		Pubkey: pubkey,
		NPub:   toolbuiltin.NostrNPubFromHex(pubkey),
		Model:  identityModel,
	}
}
