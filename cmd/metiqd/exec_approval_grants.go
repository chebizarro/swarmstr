package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

type execApprovalGrant struct {
	GrantID string `json:"grantId"`
	Kind    string `json:"kind"`
}

func execApprovalGrantID(signature string) string {
	digest := sha256.Sum256([]byte(signature))
	return "command-signature-" + hex.EncodeToString(digest[:12])
}

func allowAlwaysSignatures(raw any) []string {
	var out []string
	switch values := raw.(type) {
	case []string:
		out = append(out, values...)
	case []any:
		for _, value := range values {
			if signature, ok := value.(string); ok && signature != "" {
				out = append(out, signature)
			}
		}
	}
	return out
}

func listExecApprovalGrants(reg *execApprovalsRegistry, limit int) ([]execApprovalGrant, error) {
	if reg == nil {
		return nil, fmt.Errorf("exec approvals runtime not configured")
	}
	reg.mu.Lock()
	signatures := allowAlwaysSignatures(reg.global["allow_always_signatures"])
	reg.mu.Unlock()
	grants := make([]execApprovalGrant, 0, len(signatures))
	for _, signature := range signatures {
		grants = append(grants, execApprovalGrant{GrantID: execApprovalGrantID(signature), Kind: "command-signature"})
	}
	sort.Slice(grants, func(i, j int) bool { return grants[i].GrantID < grants[j].GrantID })
	if limit > 0 && len(grants) > limit {
		grants = grants[:limit]
	}
	return grants, nil
}

func revokeExecApprovalGrant(reg *execApprovalsRegistry, grantID string) (string, error) {
	if reg == nil {
		return "", fmt.Errorf("exec approvals runtime not configured")
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	signatures := allowAlwaysSignatures(reg.global["allow_always_signatures"])
	kept := make([]any, 0, len(signatures))
	found := false
	for _, signature := range signatures {
		if execApprovalGrantID(signature) == grantID {
			found = true
			continue
		}
		kept = append(kept, signature)
	}
	if !found {
		return "not-found", nil
	}
	previous := cloneMapAny(reg.global)
	next := cloneMapAny(reg.global)
	next["allow_always_signatures"] = kept
	reg.global = next
	if err := reg.persistApprovalsLocked(reg.pending); err != nil {
		reg.global = previous
		return "", err
	}
	return "revoked", nil
}
