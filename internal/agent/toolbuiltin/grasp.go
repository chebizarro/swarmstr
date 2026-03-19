// Package toolbuiltin – GRASP NIP-34 git repository tools.
//
// Registers: grasp_repo_announce, grasp_repo_list, grasp_issue_create,
// grasp_issue_list, grasp_patch_submit, grasp_pr_create
//
// GRASP (Git Relays Authorized via Signed-Nostr Proofs) is a git hosting protocol
// built on NIP-34. These tools let the agent interact with git repositories,
// issues, and patches via Nostr events on GRASP-compatible relays.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	nostr "fiatjaf.com/nostr"

	"swarmstr/internal/agent"
	"swarmstr/internal/grasp"
)

// GRASPToolOpts configures GRASP tools.
type GRASPToolOpts struct {
	Keyer      nostr.Keyer
	Relays     []string
}

// RegisterGRASPTools registers NIP-34 / GRASP git repository tools.
func RegisterGRASPTools(tools *agent.ToolRegistry, opts GRASPToolOpts) {
	pool := nostr.NewPool(NostrToolOpts{Keyer: opts.Keyer}.PoolOptsNIP42())

	resolveKeyer := func(ctx context.Context) (nostr.Keyer, error) {
		if opts.Keyer == nil {
			return nil, fmt.Errorf("no signing keyer configured")
		}
		return opts.Keyer, nil
	}

	// grasp_repo_announce: publish a NIP-34 repository announcement (kind 30617).
	tools.RegisterWithDef("grasp_repo_announce", func(ctx context.Context, args map[string]any) (string, error) {
		id, _ := args["id"].(string)
		name, _ := args["name"].(string)
		description, _ := args["description"].(string)
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			relays = opts.Relays
		}

		if id == "" {
			return "", fmt.Errorf("grasp_repo_announce: id (d-tag, kebab-case repo identifier) is required")
		}

		r := grasp.Repo{
			ID:          id,
			Name:        name,
			Description: description,
		}
		if cloneURLs, ok := args["clone_urls"].(string); ok && cloneURLs != "" {
			r.CloneURLs = strings.Split(cloneURLs, ",")
		}
		if webURLs, ok := args["web_urls"].(string); ok && webURLs != "" {
			r.WebURLs = strings.Split(webURLs, ",")
		}
		if repoRelays, ok := args["repo_relays"].(string); ok && repoRelays != "" {
			r.Relays = strings.Split(repoRelays, ",")
		}
		r.EarliestCommit, _ = args["earliest_commit"].(string)
		if labelsStr, ok := args["labels"].(string); ok && labelsStr != "" {
			r.Labels = strings.Split(labelsStr, ",")
		}

		ks, err := resolveKeyer(ctx)
		if err != nil {
			return "", fmt.Errorf("grasp_repo_announce: %w", err)
		}

		evID, err := grasp.AnnounceRepo(ctx, pool, ks, relays, r)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{
			"ok":       true,
			"event_id": evID,
			"id":       id,
			"note":     "Repository announced. Clients can clone via the GRASP relay at /<npub>/<id>.git",
		})
		return string(out), nil
	}, GRASPRepoAnnounceDef)

	// grasp_repo_list: fetch repository announcements (kind 30617) from relays.
	tools.RegisterWithDef("grasp_repo_list", func(ctx context.Context, args map[string]any) (string, error) {
		pubkey, _ := args["pubkey"].(string)
		limit := 20
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			relays = opts.Relays
		}

		repos, err := grasp.ListRepos(ctx, pool, relays, pubkey, limit)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{"repos": repos, "count": len(repos)})
		return string(out), nil
	}, GRASPRepoListDef)

	// grasp_issue_create: publish a NIP-34 issue (kind 1621) on a repository.
	tools.RegisterWithDef("grasp_issue_create", func(ctx context.Context, args map[string]any) (string, error) {
		repoAddr, _ := args["repo_addr"].(string)
		subject, _ := args["subject"].(string)
		content, _ := args["content"].(string)
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			relays = opts.Relays
		}

		if repoAddr == "" {
			return "", fmt.Errorf("grasp_issue_create: repo_addr required (format: 30617:<owner-pubkey>:<repo-id>)")
		}
		if content == "" {
			return "", fmt.Errorf("grasp_issue_create: content is required")
		}

		var labels []string
		if labelsStr, ok := args["labels"].(string); ok && labelsStr != "" {
			labels = strings.Split(labelsStr, ",")
		}

		ks, err := resolveKeyer(ctx)
		if err != nil {
			return "", fmt.Errorf("grasp_issue_create: %w", err)
		}

		issue := grasp.Issue{
			RepoAddr: repoAddr,
			Subject:  subject,
			Content:  content,
			Labels:   labels,
		}
		evID, err := grasp.CreateIssue(ctx, pool, ks, relays, issue)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "event_id": evID, "repo_addr": repoAddr})
		return string(out), nil
	}, GRASPIssueCreateDef)

	// grasp_issue_list: fetch issues for a repository (kind 1621).
	tools.RegisterWithDef("grasp_issue_list", func(ctx context.Context, args map[string]any) (string, error) {
		repoAddr, _ := args["repo_addr"].(string)
		limit := 20
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			relays = opts.Relays
		}

		if repoAddr == "" {
			return "", fmt.Errorf("grasp_issue_list: repo_addr required")
		}

		issues, err := grasp.ListIssues(ctx, pool, relays, repoAddr, limit)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{"issues": issues, "count": len(issues)})
		return string(out), nil
	}, GRASPIssueListDef)

	// grasp_patch_submit: publish a NIP-34 patch (kind 1617).
	tools.RegisterWithDef("grasp_patch_submit", func(ctx context.Context, args map[string]any) (string, error) {
		repoAddr, _ := args["repo_addr"].(string)
		content, _ := args["content"].(string) // git format-patch output
		commitID, _ := args["commit_id"].(string)
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			relays = opts.Relays
		}

		if repoAddr == "" {
			return "", fmt.Errorf("grasp_patch_submit: repo_addr required")
		}
		if content == "" {
			return "", fmt.Errorf("grasp_patch_submit: content (git format-patch output) is required")
		}

		ks, err := resolveKeyer(ctx)
		if err != nil {
			return "", fmt.Errorf("grasp_patch_submit: %w", err)
		}

		patch := grasp.Patch{
			RepoAddr: repoAddr,
			Content:  content,
			CommitID: commitID,
		}
		evID, err := grasp.SubmitPatch(ctx, pool, ks, relays, patch)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "event_id": evID})
		return string(out), nil
	}, GRASPPatchSubmitDef)

	// grasp_pr_create: publish a NIP-34 pull request (kind 1618).
	tools.RegisterWithDef("grasp_pr_create", func(ctx context.Context, args map[string]any) (string, error) {
		repoAddr, _ := args["repo_addr"].(string)
		subject, _ := args["subject"].(string)
		content, _ := args["content"].(string)
		commitTip, _ := args["commit_tip"].(string)
		branchName, _ := args["branch_name"].(string)
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			relays = opts.Relays
		}

		if repoAddr == "" {
			return "", fmt.Errorf("grasp_pr_create: repo_addr required")
		}

		var cloneURLs []string
		if cloneStr, ok := args["clone_urls"].(string); ok && cloneStr != "" {
			cloneURLs = strings.Split(cloneStr, ",")
		}
		var labels []string
		if labelsStr, ok := args["labels"].(string); ok && labelsStr != "" {
			labels = strings.Split(labelsStr, ",")
		}

		ks, err := resolveKeyer(ctx)
		if err != nil {
			return "", fmt.Errorf("grasp_pr_create: %w", err)
		}

		pr := grasp.PR{
			RepoAddr:   repoAddr,
			Subject:    subject,
			Content:    content,
			CommitTip:  commitTip,
			CloneURLs:  cloneURLs,
			BranchName: branchName,
			Labels:     labels,
		}
		evID, err := grasp.CreatePR(ctx, pool, ks, relays, pr)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "event_id": evID, "repo_addr": repoAddr})
		return string(out), nil
	}, GRASPPRCreateDef)
}
