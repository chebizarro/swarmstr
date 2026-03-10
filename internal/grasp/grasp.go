// Package grasp implements a NIP-34 git repository client for GRASP servers.
//
// GRASP (Git Relays Authorized via Signed-Nostr Proofs) is a protocol for
// hosting git repositories on Nostr relays with git smart HTTP access.
// Spec: https://github.com/GRASP-Protocol/grasp
//
// NIP-34 event kinds:
//
//	30617 – Repository announcement (parameterized replaceable)
//	30618 – Repository state announcement (parameterized replaceable)
//	1617  – Patch (git format-patch output)
//	1618  – Pull request
//	1619  – Pull request update
//	1621  – Issue
//	1630  – Status: Open
//	1631  – Status: Applied/Merged/Resolved
//	1632  – Status: Closed
//	1633  – Status: Draft
//	10317 – User GRASP server list
package grasp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"
)

// NIP-34 event kinds.
const (
	KindRepoAnnouncement = 30617
	KindRepoState        = 30618
	KindPatch            = 1617
	KindPR               = 1618
	KindPRUpdate         = 1619
	KindIssue            = 1621
	KindStatusOpen       = 1630
	KindStatusMerged     = 1631
	KindStatusClosed     = 1632
	KindStatusDraft      = 1633
	KindGRASPList        = 10317
)

// Repo represents a NIP-34 repository announcement (kind 30617).
type Repo struct {
	ID             string   `json:"id"`                        // d-tag (kebab-case identifier)
	Name           string   `json:"name,omitempty"`            // human-readable name
	Description    string   `json:"description,omitempty"`     // brief description
	WebURLs        []string `json:"web,omitempty"`             // browsable web URLs
	CloneURLs      []string `json:"clone,omitempty"`           // git clone URLs
	Relays         []string `json:"relays,omitempty"`          // relay URLs for patches/issues
	EarliestCommit string   `json:"earliest_commit,omitempty"` // root commit ID ("euc")
	Maintainers    []string `json:"maintainers,omitempty"`     // additional maintainer pubkeys
	Labels         []string `json:"labels,omitempty"`          // hashtag labels
	PersonalFork   bool     `json:"personal_fork,omitempty"`   // not a maintained repo
	PubKey         string   `json:"pubkey,omitempty"`
	EventID        string   `json:"event_id,omitempty"`
	CreatedAt      int64    `json:"created_at,omitempty"`
}

// Issue represents a NIP-34 issue (kind 1621).
type Issue struct {
	RepoAddr  string   `json:"repo_addr"`         // "30617:<owner-pubkey>:<repo-id>"
	Subject   string   `json:"subject,omitempty"` // optional subject/title
	Content   string   `json:"content"`           // markdown text
	Labels    []string `json:"labels,omitempty"`
	PubKey    string   `json:"pubkey,omitempty"`
	EventID   string   `json:"event_id,omitempty"`
	CreatedAt int64    `json:"created_at,omitempty"`
}

// Patch represents a NIP-34 patch (kind 1617).
type Patch struct {
	RepoAddr  string `json:"repo_addr"`           // "30617:<owner-pubkey>:<repo-id>"
	Content   string `json:"content"`             // git format-patch output
	CommitID  string `json:"commit_id,omitempty"` // current commit id
	PubKey    string `json:"pubkey,omitempty"`
	EventID   string `json:"event_id,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
}

// PR represents a NIP-34 pull request (kind 1618).
type PR struct {
	RepoAddr   string   `json:"repo_addr"`            // "30617:<owner-pubkey>:<repo-id>"
	Subject    string   `json:"subject,omitempty"`
	Content    string   `json:"content"`              // markdown description
	CommitTip  string   `json:"commit_tip,omitempty"` // tip commit (c tag)
	CloneURLs  []string `json:"clone_urls,omitempty"` // where to fetch the commit
	BranchName string   `json:"branch_name,omitempty"`
	MergeBase  string   `json:"merge_base,omitempty"`
	Labels     []string `json:"labels,omitempty"`
	PubKey     string   `json:"pubkey,omitempty"`
	EventID    string   `json:"event_id,omitempty"`
	CreatedAt  int64    `json:"created_at,omitempty"`
}

// AnnounceRepo publishes a NIP-34 repository announcement (kind 30617).
func AnnounceRepo(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, r Repo) (string, error) {
	if r.ID == "" {
		return "", fmt.Errorf("grasp: repo ID (d-tag) is required")
	}

	tags := nostr.Tags{{"d", r.ID}}
	if r.Name != "" {
		tags = append(tags, nostr.Tag{"name", r.Name})
	}
	if r.Description != "" {
		tags = append(tags, nostr.Tag{"description", r.Description})
	}
	for _, u := range r.WebURLs {
		tags = append(tags, nostr.Tag{"web", u})
	}
	for _, u := range r.CloneURLs {
		tags = append(tags, nostr.Tag{"clone", u})
	}
	if len(r.Relays) > 0 {
		relayTag := nostr.Tag{"relays"}
		relayTag = append(relayTag, r.Relays...)
		tags = append(tags, relayTag)
	}
	if r.EarliestCommit != "" {
		tags = append(tags, nostr.Tag{"r", r.EarliestCommit, "euc"})
	}
	if len(r.Maintainers) > 0 {
		maintTag := nostr.Tag{"maintainers"}
		maintTag = append(maintTag, r.Maintainers...)
		tags = append(tags, maintTag)
	}
	if r.PersonalFork {
		tags = append(tags, nostr.Tag{"t", "personal-fork"})
	}
	for _, label := range r.Labels {
		tags = append(tags, nostr.Tag{"t", label})
	}

	evt := nostr.Event{
		Kind:      nostr.Kind(KindRepoAnnouncement),
		CreatedAt: nostr.Now(),
		Tags:      tags,
		Content:   "",
	}
	if err := keyer.SignEvent(ctx, &evt); err != nil {
		return "", fmt.Errorf("grasp: sign repo announcement: %w", err)
	}
	return publishEvent(ctx, pool, relays, evt)
}

// ListRepos fetches repository announcements (kind 30617) from relays.
// If pubkey is empty, fetches from all authors.
func ListRepos(ctx context.Context, pool *nostr.Pool, relays []string, pubkey string, limit int) ([]Repo, error) {
	if limit <= 0 {
		limit = 20
	}
	filter := nostr.Filter{
		Kinds: []nostr.Kind{nostr.Kind(KindRepoAnnouncement)},
		Limit: limit,
	}
	if pubkey != "" {
		pk, err := nostr.PubKeyFromHex(pubkey)
		if err != nil {
			return nil, fmt.Errorf("grasp: invalid pubkey: %w", err)
		}
		filter.Authors = []nostr.PubKey{pk}
	}

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var repos []Repo
	seen := make(map[string]bool)
	for re := range pool.SubscribeMany(ctx2, relays, filter, nostr.SubscriptionOptions{}) {
		id := re.Event.ID.Hex()
		if seen[id] {
			continue
		}
		seen[id] = true
		repos = append(repos, decodeRepoEvent(re.Event))
	}
	sort.Slice(repos, func(i, j int) bool {
		return repos[i].CreatedAt > repos[j].CreatedAt
	})
	return repos, nil
}

// CreateIssue publishes a NIP-34 issue (kind 1621).
func CreateIssue(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, issue Issue) (string, error) {
	if issue.RepoAddr == "" {
		return "", fmt.Errorf("grasp: repo_addr is required for issue")
	}
	if issue.Content == "" {
		return "", fmt.Errorf("grasp: content is required for issue")
	}

	ownerPubkey := extractAddrPubkey(issue.RepoAddr)
	tags := nostr.Tags{{"a", issue.RepoAddr}}
	if ownerPubkey != "" {
		tags = append(tags, nostr.Tag{"p", ownerPubkey})
	}
	if issue.Subject != "" {
		tags = append(tags, nostr.Tag{"subject", issue.Subject})
	}
	for _, label := range issue.Labels {
		tags = append(tags, nostr.Tag{"t", label})
	}

	evt := nostr.Event{
		Kind:      nostr.Kind(KindIssue),
		CreatedAt: nostr.Now(),
		Tags:      tags,
		Content:   issue.Content,
	}
	if err := keyer.SignEvent(ctx, &evt); err != nil {
		return "", fmt.Errorf("grasp: sign issue: %w", err)
	}
	return publishEvent(ctx, pool, relays, evt)
}

// ListIssues fetches issues for a repository (kind 1621).
func ListIssues(ctx context.Context, pool *nostr.Pool, relays []string, repoAddr string, limit int) ([]Issue, error) {
	if limit <= 0 {
		limit = 20
	}
	filter := nostr.Filter{
		Kinds: []nostr.Kind{nostr.Kind(KindIssue)},
		Tags:  nostr.TagMap{"a": []string{repoAddr}},
		Limit: limit,
	}

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var issues []Issue
	seen := make(map[string]bool)
	for re := range pool.SubscribeMany(ctx2, relays, filter, nostr.SubscriptionOptions{}) {
		id := re.Event.ID.Hex()
		if seen[id] {
			continue
		}
		seen[id] = true
		issues = append(issues, decodeIssueEvent(re.Event))
	}
	sort.Slice(issues, func(i, j int) bool {
		return issues[i].CreatedAt > issues[j].CreatedAt
	})
	return issues, nil
}

// SubmitPatch publishes a NIP-34 patch (kind 1617).
func SubmitPatch(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, patch Patch) (string, error) {
	if patch.RepoAddr == "" {
		return "", fmt.Errorf("grasp: repo_addr is required for patch")
	}
	if patch.Content == "" {
		return "", fmt.Errorf("grasp: content (git format-patch output) is required")
	}

	ownerPubkey := extractAddrPubkey(patch.RepoAddr)
	tags := nostr.Tags{
		{"a", patch.RepoAddr},
		{"t", "root"},
	}
	if ownerPubkey != "" {
		tags = append(tags, nostr.Tag{"p", ownerPubkey})
	}
	if patch.CommitID != "" {
		tags = append(tags, nostr.Tag{"commit", patch.CommitID})
		tags = append(tags, nostr.Tag{"r", patch.CommitID})
	}

	evt := nostr.Event{
		Kind:      nostr.Kind(KindPatch),
		CreatedAt: nostr.Now(),
		Tags:      tags,
		Content:   patch.Content,
	}
	if err := keyer.SignEvent(ctx, &evt); err != nil {
		return "", fmt.Errorf("grasp: sign patch: %w", err)
	}
	return publishEvent(ctx, pool, relays, evt)
}

// CreatePR publishes a NIP-34 pull request (kind 1618).
func CreatePR(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, pr PR) (string, error) {
	if pr.RepoAddr == "" {
		return "", fmt.Errorf("grasp: repo_addr is required for PR")
	}

	ownerPubkey := extractAddrPubkey(pr.RepoAddr)
	tags := nostr.Tags{{"a", pr.RepoAddr}}
	if ownerPubkey != "" {
		tags = append(tags, nostr.Tag{"p", ownerPubkey})
	}
	if pr.Subject != "" {
		tags = append(tags, nostr.Tag{"subject", pr.Subject})
	}
	if pr.CommitTip != "" {
		tags = append(tags, nostr.Tag{"c", pr.CommitTip})
	}
	for _, u := range pr.CloneURLs {
		tags = append(tags, nostr.Tag{"clone", u})
	}
	if pr.BranchName != "" {
		tags = append(tags, nostr.Tag{"branch-name", pr.BranchName})
	}
	if pr.MergeBase != "" {
		tags = append(tags, nostr.Tag{"merge-base", pr.MergeBase})
	}
	for _, label := range pr.Labels {
		tags = append(tags, nostr.Tag{"t", label})
	}

	evt := nostr.Event{
		Kind:      nostr.Kind(KindPR),
		CreatedAt: nostr.Now(),
		Tags:      tags,
		Content:   pr.Content,
	}
	if err := keyer.SignEvent(ctx, &evt); err != nil {
		return "", fmt.Errorf("grasp: sign PR: %w", err)
	}
	return publishEvent(ctx, pool, relays, evt)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func decodeRepoEvent(ev nostr.Event) Repo {
	r := Repo{
		PubKey:    ev.PubKey.Hex(),
		EventID:   ev.ID.Hex(),
		CreatedAt: int64(ev.CreatedAt),
	}
	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "d":
			r.ID = tag[1]
		case "name":
			r.Name = tag[1]
		case "description":
			r.Description = tag[1]
		case "web":
			r.WebURLs = append(r.WebURLs, tag[1:]...)
		case "clone":
			r.CloneURLs = append(r.CloneURLs, tag[1:]...)
		case "relays":
			r.Relays = append(r.Relays, tag[1:]...)
		case "maintainers":
			r.Maintainers = append(r.Maintainers, tag[1:]...)
		case "r":
			if len(tag) >= 3 && tag[2] == "euc" {
				r.EarliestCommit = tag[1]
			}
		case "t":
			if tag[1] == "personal-fork" {
				r.PersonalFork = true
			} else {
				r.Labels = append(r.Labels, tag[1])
			}
		}
	}
	return r
}

func decodeIssueEvent(ev nostr.Event) Issue {
	issue := Issue{
		PubKey:    ev.PubKey.Hex(),
		EventID:   ev.ID.Hex(),
		Content:   ev.Content,
		CreatedAt: int64(ev.CreatedAt),
	}
	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "a":
			issue.RepoAddr = tag[1]
		case "subject":
			issue.Subject = tag[1]
		case "t":
			issue.Labels = append(issue.Labels, tag[1])
		}
	}
	return issue
}

// extractAddrPubkey extracts the pubkey from a NIP-34 address "kind:pubkey:d-tag".
func extractAddrPubkey(addr string) string {
	parts := strings.SplitN(addr, ":", 3)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

func publishEvent(ctx context.Context, pool *nostr.Pool, relays []string, evt nostr.Event) (string, error) {
	published := false
	var lastErr error
	for result := range pool.PublishMany(ctx, relays, evt) {
		if result.Error == nil {
			published = true
		} else {
			lastErr = fmt.Errorf("relay %s: %w", result.RelayURL, result.Error)
		}
	}
	if !published {
		if lastErr == nil {
			lastErr = fmt.Errorf("no relay accepted the event")
		}
		return "", lastErr
	}
	return evt.ID.Hex(), nil
}

// MarshalJSON serializes a value as indented JSON string.
func MarshalJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
