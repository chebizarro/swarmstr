package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/agent"
	"metiq/internal/grasp"
	"metiq/internal/nostr/events"
	"metiq/internal/nostr/nip51"
	"metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

const (
	nip34RepoBookmarkDTag     = "git-repo-bookmark"
	nip51KindPrivateBookmarks = 30003
)

var nip34RepoBookmarks = newRepoBookmarkIndex()

type nip34AutoReviewConfig struct {
	Enabled      bool
	AgentID      string
	ToolProfile  string
	EnabledTools []string
	TriggerTypes map[grasp.InboundEventType]struct{}
	FollowedOnly bool
	Instructions string
}

type repoBookmarkIndex struct {
	mu        sync.RWMutex
	ready     bool
	updatedAt int64
	repos     map[string]struct{}
}

func newRepoBookmarkIndex() *repoBookmarkIndex {
	return &repoBookmarkIndex{repos: map[string]struct{}{}}
}

func (i *repoBookmarkIndex) Replace(addrs []string, updatedAt int64, ready bool) {
	if i == nil {
		return
	}
	repos := make(map[string]struct{}, len(addrs))
	for _, addr := range addrs {
		if canonical := canonicalRepoAddr(addr); canonical != "" {
			repos[canonical] = struct{}{}
		}
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if updatedAt > 0 && i.updatedAt > updatedAt {
		return
	}
	i.ready = ready
	i.updatedAt = updatedAt
	i.repos = repos
}

func (i *repoBookmarkIndex) Ready() bool {
	if i == nil {
		return false
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.ready
}

func (i *repoBookmarkIndex) Contains(addr string) bool {
	if i == nil {
		return false
	}
	canonical := canonicalRepoAddr(addr)
	if canonical == "" {
		return false
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	_, ok := i.repos[canonical]
	return ok
}

func canonicalRepoAddr(raw string) string {
	repo, _ := grasp.SplitRepoAddr(strings.TrimSpace(raw))
	if strings.TrimSpace(repo.OwnerPubKey) == "" || strings.TrimSpace(repo.ID) == "" {
		return strings.TrimSpace(raw)
	}
	return fmt.Sprintf("%d:%s:%s", events.KindRepoAnnouncement, strings.ToLower(strings.TrimSpace(repo.OwnerPubKey)), strings.TrimSpace(repo.ID))
}

func parseNIP34AutoReviewConfig(chanCfg state.NostrChannelConfig) (nip34AutoReviewConfig, bool) {
	cfg := nip34AutoReviewConfig{
		FollowedOnly: true,
		TriggerTypes: map[grasp.InboundEventType]struct{}{
			grasp.InboundEventPR:       {},
			grasp.InboundEventPRUpdate: {},
		},
	}
	if chanCfg.Config == nil {
		return cfg, false
	}
	raw, ok := chanCfg.Config["auto_review"]
	if !ok {
		return cfg, false
	}
	switch v := raw.(type) {
	case bool:
		cfg.Enabled = v
	case map[string]any:
		cfg.Enabled = boolValue(v, "enabled")
		cfg.AgentID = strings.TrimSpace(stringValue(v, "agent_id"))
		cfg.ToolProfile = strings.TrimSpace(strings.ToLower(stringValue(v, "tool_profile")))
		cfg.EnabledTools = agent.NormalizeAllowedToolNames(stringSliceValue(v["enabled_tools"]))
		cfg.Instructions = strings.TrimSpace(stringValue(v, "instructions"))
		if rawFollowedOnly, exists := v["followed_only"]; exists {
			if followedOnly, ok := rawFollowedOnly.(bool); ok {
				cfg.FollowedOnly = followedOnly
			}
		}
		if triggerTypes := normalizeNIP34TriggerTypes(v["trigger_types"]); len(triggerTypes) > 0 {
			cfg.TriggerTypes = triggerTypes
		}
	default:
		return cfg, false
	}
	return cfg, cfg.Enabled
}

func normalizeNIP34TriggerTypes(raw any) map[grasp.InboundEventType]struct{} {
	values := stringSliceValue(raw)
	if len(values) == 0 {
		return nil
	}
	out := make(map[grasp.InboundEventType]struct{}, len(values))
	for _, value := range values {
		switch grasp.InboundEventType(strings.ToLower(strings.TrimSpace(value))) {
		case grasp.InboundEventPatch:
			out[grasp.InboundEventPatch] = struct{}{}
		case grasp.InboundEventPR:
			out[grasp.InboundEventPR] = struct{}{}
		case grasp.InboundEventPRUpdate:
			out[grasp.InboundEventPRUpdate] = struct{}{}
		case grasp.InboundEventIssue:
			out[grasp.InboundEventIssue] = struct{}{}
		case grasp.InboundEventStatus:
			out[grasp.InboundEventStatus] = struct{}{}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func shouldAutoReviewNIP34Event(cfg nip34AutoReviewConfig, event grasp.InboundEvent, bookmarks *repoBookmarkIndex) bool {
	if !cfg.Enabled {
		return false
	}
	if len(cfg.TriggerTypes) > 0 {
		if _, ok := cfg.TriggerTypes[event.Type]; !ok {
			return false
		}
	}
	if !cfg.FollowedOnly {
		return true
	}
	if bookmarks == nil || !bookmarks.Ready() {
		return false
	}
	return bookmarks.Contains(event.Repo.Addr)
}

func renderNIP34AutoReviewText(channelName string, event grasp.InboundEvent, relay string, cfg nip34AutoReviewConfig) string {
	base := renderNIP34InboxText(channelName, event, relay)
	instructions := cfg.Instructions
	if instructions == "" {
		instructions = "Review this inbound repository change for bugs, regressions, security issues, and missing tests. If the event does not include enough patch or diff context for a full review, say exactly what additional context is needed."
	}
	return strings.TrimSpace(strings.Join([]string{
		"[nip34-auto-review]",
		instructions,
		"Inbound event:",
		base,
	}, "\n\n"))
}

func anyEnabledNIP34AutoReviewFollowedOnly(cfg state.ConfigDoc) bool {
	for _, chanCfg := range cfg.NostrChannels {
		if strings.TrimSpace(chanCfg.Kind) != string(state.NostrChannelKindNIP34Inbox) && relayFilterMode(chanCfg) != relayFilterModeNIP34 {
			continue
		}
		autoCfg, ok := parseNIP34AutoReviewConfig(chanCfg)
		if ok && autoCfg.Enabled && autoCfg.FollowedOnly {
			return true
		}
	}
	return false
}

func repoBookmarkRelays(cfg state.ConfigDoc) []string {
	return runtime.MergeRelayLists(cfg.Relays.Read, cfg.Relays.Write)
}

func loadInitialRepoBookmarks(ctx context.Context, keyer nostr.Keyer, cfg state.ConfigDoc) {
	relays := repoBookmarkRelays(cfg)
	if keyer == nil || len(relays) == 0 {
		return
	}
	pool := nostr.NewPool(runtime.PoolOptsNIP42(keyer))
	defer pool.Close("repo bookmark bootstrap complete")
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	addrs, updatedAt, err := fetchRepoBookmarkAddrs(fetchCtx, pool, keyer, relays)
	if err != nil {
		log.Printf("nip34 auto-review bookmark bootstrap warning: %v", err)
		return
	}
	nip34RepoBookmarks.Replace(addrs, updatedAt, true)
}

func startRepoBookmarkWatcher(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, cfg state.ConfigDoc) {
	if pool == nil || keyer == nil {
		return
	}
	relays := repoBookmarkRelays(cfg)
	if len(relays) == 0 {
		return
	}
	pk, err := keyer.GetPublicKey(ctx)
	if err != nil {
		log.Printf("nip34 auto-review bookmark watcher: get public key: %v", err)
		return
	}
	filter := nostr.Filter{
		Kinds:   []nostr.Kind{nostr.Kind(nip51KindPrivateBookmarks), nostr.Kind(nip51.KindBookmarkSet)},
		Authors: []nostr.PubKey{pk},
		Tags:    nostr.TagMap{"d": []string{nip34RepoBookmarkDTag}},
	}
	go func() {
		for re := range pool.SubscribeMany(ctx, relays, filter, nostr.SubscriptionOptions{}) {
			decoded := nip51.DecodeEvent(re.Event)
			if strings.TrimSpace(decoded.DTag) != nip34RepoBookmarkDTag {
				continue
			}
			addrs := repoBookmarkAddrs(decoded.Entries)
			if !shouldAcceptRepoBookmarkSnapshot(int(re.Event.Kind), addrs) {
				continue
			}
			nip34RepoBookmarks.Replace(addrs, decoded.CreatedAt, true)
		}
	}()
}

func fetchRepoBookmarkAddrs(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string) ([]string, int64, error) {
	pk, err := keyer.GetPublicKey(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("get public key: %w", err)
	}
	filter := nostr.Filter{
		Kinds:   []nostr.Kind{nostr.Kind(nip51KindPrivateBookmarks), nostr.Kind(nip51.KindBookmarkSet)},
		Authors: []nostr.PubKey{pk},
		Tags:    nostr.TagMap{"d": []string{nip34RepoBookmarkDTag}},
	}
	var latestAny *nostr.Event
	var latestUsable *nostr.Event
	var latestUsableAddrs []string
	for re := range pool.FetchMany(ctx, relays, filter, nostr.SubscriptionOptions{}) {
		ev := re.Event
		if latestAny == nil || ev.CreatedAt > latestAny.CreatedAt {
			copy := ev
			latestAny = &copy
		}
		decoded := nip51.DecodeEvent(ev)
		addrs := repoBookmarkAddrs(decoded.Entries)
		if !shouldAcceptRepoBookmarkSnapshot(int(ev.Kind), addrs) {
			continue
		}
		if latestUsable == nil || ev.CreatedAt > latestUsable.CreatedAt {
			copy := ev
			latestUsable = &copy
			latestUsableAddrs = addrs
		}
	}
	if latestUsable != nil {
		return latestUsableAddrs, int64(latestUsable.CreatedAt), nil
	}
	if latestAny == nil {
		return []string{}, 0, nil
	}
	decoded := nip51.DecodeEvent(*latestAny)
	return repoBookmarkAddrs(decoded.Entries), decoded.CreatedAt, nil
}

func shouldAcceptRepoBookmarkSnapshot(kind int, addrs []string) bool {
	if kind == nip51KindPrivateBookmarks && len(addrs) == 0 {
		return false
	}
	return true
}

func repoBookmarkAddrs(entries []nip51.ListEntry) []string {
	if len(entries) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.Tag != "a" {
			continue
		}
		if canonical := canonicalRepoAddr(entry.Value); canonical != "" {
			set[canonical] = struct{}{}
		}
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for addr := range set {
		out = append(out, addr)
	}
	sort.Strings(out)
	return out
}

func boolValue(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func stringValue(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func stringSliceValue(raw any) []string {
	switch v := raw.(type) {
	case []string:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					out = append(out, trimmed)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func encodeTurnConstraintsForTest(profile string, enabledTools []string) string {
	payload, _ := json.Marshal(map[string]any{
		"tool_profile":  profile,
		"enabled_tools": enabledTools,
	})
	return string(payload)
}
