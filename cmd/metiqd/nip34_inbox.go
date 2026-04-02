package main

import (
	"encoding/json"
	"fmt"
	"strings"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/agent/toolbuiltin"
	"metiq/internal/grasp"
	"metiq/internal/nostr/events"
	"metiq/internal/store/state"
)

const relayFilterModeNIP34 = "nip34"

func relayFilterMode(chanCfg state.NostrChannelConfig) string {
	kind := strings.TrimSpace(strings.ToLower(chanCfg.Kind))
	if kind == string(state.NostrChannelKindNIP34Inbox) {
		return relayFilterModeNIP34
	}
	if chanCfg.Config == nil {
		return ""
	}
	for _, key := range []string{"mode", "type"} {
		if raw, ok := chanCfg.Config[key].(string); ok {
			switch strings.TrimSpace(strings.ToLower(raw)) {
			case relayFilterModeNIP34, string(state.NostrChannelKindNIP34Inbox):
				return relayFilterModeNIP34
			}
		}
	}
	return ""
}

func buildRelayFilterFilter(chanCfg state.NostrChannelConfig) (nostr.Filter, error) {
	filterArgs := map[string]any{}
	if chanCfg.Config != nil {
		if rawFilter, ok := chanCfg.Config["filter"].(map[string]any); ok {
			for key, value := range rawFilter {
				filterArgs[key] = value
			}
		}
		for _, key := range []string{"kinds", "authors", "ids", "since", "until"} {
			if value, ok := chanCfg.Config[key]; ok {
				filterArgs[key] = value
			}
		}
	}
	for tag, values := range chanCfg.Tags {
		filterArgs["#"+strings.TrimSpace(tag)] = append([]string(nil), values...)
	}
	if relayFilterMode(chanCfg) == relayFilterModeNIP34 {
		if _, ok := filterArgs["kinds"]; !ok {
			filterArgs["kinds"] = []float64{
				float64(events.KindPatch),
				float64(events.KindPR),
				float64(events.KindPRUpdate),
				float64(events.KindIssue),
			}
		}
	}
	return toolbuiltin.BuildNostrFilter(filterArgs, 0)
}

func relayFilterSessionID(channelID, senderPubKey string) string {
	return "ch:" + strings.TrimSpace(channelID) + ":" + strings.TrimSpace(strings.ToLower(senderPubKey))
}

func nip34InboxSessionID(channelName string, event grasp.InboundEvent) string {
	repoKey := canonicalRepoAddr(event.Repo.Addr)
	if repoKey == "" {
		repoKey = strings.TrimSpace(event.EventID)
	}
	return "nip34:" + strings.TrimSpace(channelName) + ":" + repoKey
}

func renderRelayFilterInboxText(channelName string, ev nostr.Event, relay string) string {
	payload := map[string]any{
		"channel":    channelName,
		"relay":      strings.TrimSpace(relay),
		"id":         ev.ID.Hex(),
		"pubkey":     ev.PubKey.Hex(),
		"kind":       int(ev.Kind),
		"content":    ev.Content,
		"created_at": int64(ev.CreatedAt),
		"tags":       relayFilterTags(ev.Tags),
	}
	b, _ := json.Marshal(payload)
	return fmt.Sprintf("[relay-filter:%s] %s", strings.TrimSpace(channelName), string(b))
}

func renderNIP34InboxText(channelName string, event grasp.InboundEvent, relay string) string {
	payload := map[string]any{
		"channel":        strings.TrimSpace(channelName),
		"relay":          strings.TrimSpace(relay),
		"type":           event.Type,
		"kind":           event.Kind,
		"status":         event.Status,
		"repo":           event.Repo,
		"author_pubkey":  event.AuthorPubKey,
		"subject":        event.Subject,
		"body":           event.Body,
		"labels":         event.Labels,
		"commit_id":      event.CommitID,
		"commit_tip":     event.CommitTip,
		"clone_urls":     event.CloneURLs,
		"branch_name":    event.BranchName,
		"merge_base":     event.MergeBase,
		"merge_commit":   event.MergeCommit,
		"root_event_id":  event.RootEventID,
		"reply_event_id": event.ReplyEventID,
		"event_id":       event.EventID,
		"created_at":     event.CreatedAt,
	}
	if len(event.MentionEventIDs) > 0 {
		payload["mention_event_ids"] = event.MentionEventIDs
	}
	if len(event.AppliedCommitIDs) > 0 {
		payload["applied_commit_ids"] = event.AppliedCommitIDs
	}
	b, _ := json.Marshal(payload)
	return fmt.Sprintf("[nip34-inbox:%s] %s", strings.TrimSpace(channelName), string(b))
}

func relayFilterTags(tags nostr.Tags) [][]string {
	if len(tags) == 0 {
		return nil
	}
	out := make([][]string, 0, len(tags))
	for _, tag := range tags {
		out = append(out, append([]string(nil), tag...))
	}
	return out
}
