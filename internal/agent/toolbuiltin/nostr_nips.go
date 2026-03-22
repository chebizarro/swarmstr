// Package toolbuiltin – additional NIP tools.
//
// Implements agent tools for:
//   - NIP-09:  nostr_delete       (kind 5 deletion events)
//   - NIP-22:  nostr_comment      (kind 1111 threaded comments)
//   - NIP-23:  nostr_article_*    (kind 30023 long-form content)
//   - NIP-25:  nostr_react        (kind 7 reactions)
//   - NIP-50:  nostr_search       (search relay filter)
//   - NIP-78:  nostr_appdata_*    (kind 30078 app-specific data)
//   - NIP-94:  nostr_file_*       (kind 1063 file metadata)
//   - NIP-36:  nostr_publish_sensitive (content-warning support)
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/agent"
	nostruntime "metiq/internal/nostr/runtime"
)

// RegisterNIPTools registers additional NIP protocol tools.
func RegisterNIPTools(tools *agent.ToolRegistry, opts NostrToolOpts) {
	var (
		fallbackPool *nostr.Pool
		poolOnce     sync.Once
	)
	getPool := func() *nostr.Pool {
		if h := opts.hub(); h != nil {
			return h.Pool()
		}
		poolOnce.Do(func() {
			fallbackPool = nostr.NewPool(nostruntime.PoolOptsNIP42(opts.Keyer))
		})
		return fallbackPool
	}

	// Early validation: if no keyer, publishEvent will fail
	// Tools that need signing should check opts.Keyer != nil

	signEvent := func(ctx context.Context, evt *nostr.Event) error {
		signFn, err := opts.signerFunc()
		if err != nil {
			return err
		}
		return signFn(ctx, evt)
	}

	publishEvent := func(ctx context.Context, evt nostr.Event, relays []string) (string, error) {
		// Content guard: scan for secrets before signing and publishing.
		if err := opts.checkOutboundEvent(&evt); err != nil {
			return "", err
		}
		if err := signEvent(ctx, &evt); err != nil {
			return "", fmt.Errorf("sign event: %w", err)
		}
		published := false
		var lastErr error
		for result := range getPool().PublishMany(ctx, relays, evt) {
			if result.Error == nil {
				published = true
			} else {
				lastErr = fmt.Errorf("relay %s: %w", result.RelayURL, result.Error)
			}
		}
		if !published {
			if lastErr == nil {
				lastErr = fmt.Errorf("no relay accepted publish")
			}
			return "", lastErr
		}
		return evt.ID.Hex(), nil
	}

	// ── NIP-09: Event Deletion ─────────────────────────────────────────────

	deleteTool := func(toolName string) agent.ToolFunc {
		return func(ctx context.Context, args map[string]any) (string, error) {
			ids := toStringSlice(args["ids"])
			if len(ids) == 0 {
				return "", nostrToolErr(toolName, "missing_ids", "ids is required", nil)
			}
			reason, _ := args["reason"].(string)
			relays := opts.resolveRelays(toStringSlice(args["relays"]))
			if len(relays) == 0 {
				return "", nostrToolErr(toolName, "no_relays", "no relays configured", nil)
			}

			tags := nostr.Tags{}
			for _, id := range ids {
				tags = append(tags, nostr.Tag{"e", id})
			}
			evt := nostr.Event{
				Kind:      5,
				CreatedAt: nostr.Now(),
				Tags:      tags,
				Content:   reason,
			}
			evID, err := publishEvent(ctx, evt, relays)
			if err != nil {
				return "", mapNostrPublishErr(toolName, err, map[string]any{"kind": 5, "target_count": len(ids)})
			}
			return nostrWriteSuccessEnvelope(toolName, evID, 5, map[string]any{"event_ids": ids}, map[string]any{
				"publish_relays": relays,
			}, map[string]any{
				"deleted_ids": ids,
			}), nil
		}
	}
	tools.RegisterWithDef("nostr_delete", deleteTool("nostr_delete"), NostrDeleteDef)
	tools.RegisterWithDef("nostr_event_delete", deleteTool("nostr_event_delete"), NostrEventDeleteDef)

	// ── NIP-56: Reporting (kind 1984) ──────────────────────────────────────

	tools.RegisterWithDef("nostr_report", func(ctx context.Context, args map[string]any) (string, error) {
		reportType, _ := args["report_type"].(string)
		reason, _ := args["reason"].(string)
		eventIDs := uniqueNonEmpty(toStringSlice(args["target_event_ids"]))
		pubkeys := uniqueNonEmpty(toStringSlice(args["target_pubkeys"]))
		relays := opts.resolveRelays(toStringSlice(args["relays"]))

		if strings.TrimSpace(reportType) == "" {
			return "", nostrToolErr("nostr_report", "missing_report_type", "report_type is required", nil)
		}
		if len(eventIDs) == 0 && len(pubkeys) == 0 {
			return "", nostrToolErr("nostr_report", "missing_targets", "provide target_event_ids and/or target_pubkeys", nil)
		}
		if len(relays) == 0 {
			return "", nostrToolErr("nostr_report", "no_relays", "no relays configured", nil)
		}

		tags := nostr.Tags{{"report", strings.ToLower(strings.TrimSpace(reportType))}}
		for _, id := range eventIDs {
			tags = append(tags, nostr.Tag{"e", id})
		}
		for _, pk := range pubkeys {
			tags = append(tags, nostr.Tag{"p", pk})
		}
		evt := nostr.Event{
			Kind:      1984,
			CreatedAt: nostr.Now(),
			Tags:      tags,
			Content:   reason,
		}
		evID, err := publishEvent(ctx, evt, relays)
		if err != nil {
			return "", mapNostrPublishErr("nostr_report", err, map[string]any{"kind": 1984})
		}
		return nostrWriteSuccessEnvelope("nostr_report", evID, 1984, map[string]any{
			"event_ids": eventIDs,
			"pubkeys":   pubkeys,
		}, map[string]any{
			"report_type":    reportType,
			"publish_relays": relays,
		}, map[string]any{
			"report_type":    reportType,
			"event_targets":  eventIDs,
			"pubkey_targets": pubkeys,
		}), nil
	}, NostrReportDef)

	// ── NIP-25: Reactions ──────────────────────────────────────────────────

	tools.RegisterWithDef("nostr_react", func(ctx context.Context, args map[string]any) (string, error) {
		eventID, _ := args["event_id"].(string)
		relayHint, _ := args["relay_hint"].(string)
		content, _ := args["content"].(string)
		if eventID == "" {
			return "", fmt.Errorf("nostr_react: event_id is required")
		}
		if content == "" {
			content = "+" // default: like
		}
		relays := opts.resolveRelays(toStringSlice(args["relays"]))

		tags := nostr.Tags{{"e", eventID}}
		if relayHint != "" {
			tags[0] = append(tags[0], relayHint)
		}
		evt := nostr.Event{
			Kind:      7,
			CreatedAt: nostr.Now(),
			Tags:      tags,
			Content:   content,
		}
		evID, err := publishEvent(ctx, evt, relays)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "event_id": evID, "reaction": content})
		return string(out), nil
	}, NostrReactDef)

	// ── NIP-22: Comments (kind 1111) ───────────────────────────────────────

	tools.RegisterWithDef("nostr_comment", func(ctx context.Context, args map[string]any) (string, error) {
		rootID, _ := args["root_id"].(string)
		rootKind, _ := args["root_kind"].(float64)
		rootRelay, _ := args["root_relay"].(string)
		parentID, _ := args["parent_id"].(string)
		content, _ := args["content"].(string)
		relays := opts.resolveRelays(toStringSlice(args["relays"]))

		if rootID == "" || content == "" {
			return "", fmt.Errorf("nostr_comment: root_id and content are required")
		}

		tags := nostr.Tags{
			{"E", rootID, rootRelay, fmt.Sprintf("%d", int(rootKind))},
		}
		if parentID != "" && parentID != rootID {
			tags = append(tags, nostr.Tag{"e", parentID, rootRelay})
		}

		evt := nostr.Event{
			Kind:      1111,
			CreatedAt: nostr.Now(),
			Tags:      tags,
			Content:   content,
		}
		evID, err := publishEvent(ctx, evt, relays)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "event_id": evID})
		return string(out), nil
	}, NostrCommentDef)

	// ── NIP-23: Long-form content (kind 30023) ─────────────────────────────

	tools.RegisterWithDef("nostr_article_publish", func(ctx context.Context, args map[string]any) (string, error) {
		title, _ := args["title"].(string)
		summary, _ := args["summary"].(string)
		content, _ := args["content"].(string)
		image, _ := args["image"].(string)
		dTag, _ := args["d_tag"].(string)
		relays := opts.resolveRelays(toStringSlice(args["relays"]))

		if title == "" || content == "" {
			return "", nostrToolErr("nostr_article_publish", "missing_fields", "title and content are required", nil)
		}
		if len(relays) == 0 {
			return "", nostrToolErr("nostr_article_publish", "no_relays", "no relays configured", nil)
		}
		if dTag == "" {
			dTag = slugify(title)
		}
		if strings.TrimSpace(summary) == "" {
			summary = autoArticleSummary(content)
		}
		if strings.TrimSpace(image) == "" {
			image = firstMarkdownImage(content)
		}

		publishedAt := time.Now().Unix()
		if v, ok := args["published_at"].(float64); ok && v > 0 {
			publishedAt = int64(v)
		}

		tags := nostr.Tags{
			{"d", dTag},
			{"title", title},
			{"published_at", fmt.Sprintf("%d", publishedAt)},
		}
		if summary != "" {
			tags = append(tags, nostr.Tag{"summary", summary})
		}
		if image != "" {
			tags = append(tags, nostr.Tag{"image", image})
		}
		articleTags := uniqueNonEmpty(toStringSlice(args["tags"]))
		if len(articleTags) == 0 {
			articleTags = inferHashtags(content)
		}
		for _, t := range articleTags {
			tags = append(tags, nostr.Tag{"t", t})
		}

		evt := nostr.Event{
			Kind:      30023,
			CreatedAt: nostr.Now(),
			Tags:      tags,
			Content:   content,
		}
		evID, err := publishEvent(ctx, evt, relays)
		if err != nil {
			return "", mapNostrPublishErr("nostr_article_publish", err, map[string]any{"kind": 30023, "d_tag": dTag})
		}
		return nostrWriteSuccessEnvelope("nostr_article_publish", evID, 30023, map[string]any{
			"d_tag": dTag,
		}, map[string]any{
			"title":          title,
			"summary":        summary,
			"image":          image,
			"tags":           articleTags,
			"publish_relays": relays,
		}, map[string]any{
			"d_tag":   dTag,
			"summary": summary,
			"image":   image,
		}), nil
	}, NostrArticlePublishDef)

	tools.RegisterWithDef("nostr_article_get", func(ctx context.Context, args map[string]any) (string, error) {
		author, _ := args["author"].(string)
		dTag, _ := args["d_tag"].(string)
		relays := opts.resolveRelays(toStringSlice(args["relays"]))

		if author == "" {
			return "", fmt.Errorf("nostr_article_get: author pubkey is required")
		}
		if len(relays) == 0 {
			return "", fmt.Errorf("nostr_article_get: no relays configured")
		}

		filter := nostr.Filter{
			Kinds: []nostr.Kind{30023},
			Limit: 1,
		}
		pk, err := nostr.PubKeyFromHex(author)
		if err != nil {
			return "", fmt.Errorf("nostr_article_get: invalid pubkey: %w", err)
		}
		filter.Authors = []nostr.PubKey{pk}
		if dTag != "" {
			filter.Tags = nostr.TagMap{"d": []string{dTag}}
		}

		ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		var best *nostr.Event
		for re := range getPool().SubscribeMany(ctx2, relays, filter, nostr.SubscriptionOptions{}) {
			if best == nil || re.Event.CreatedAt > best.CreatedAt {
				ev := re.Event
				best = &ev
			}
		}
		if best == nil {
			return "", fmt.Errorf("nostr_article_get: article not found")
		}
		out, _ := json.Marshal(eventToMap(*best))
		return string(out), nil
	}, NostrArticleGetDef)

	// ── NIP-50: Search ─────────────────────────────────────────────────────

	tools.RegisterWithDef("nostr_search", func(ctx context.Context, args map[string]any) (string, error) {
		query, _ := args["query"].(string)
		if query == "" {
			return "", fmt.Errorf("nostr_search: query is required")
		}
		limit := 20
		if v, ok := args["limit"].(float64); ok && v > 0 {
			limit = min(int(v), 100)
		}
		relays := toStringSlice(args["relays"])
		if len(relays) == 0 {
			// Default to known search-capable relays.
			relays = []string{"wss://relay.primal.net", "wss://nostr.wine"}
		}

		filter := nostr.Filter{
			Limit:  limit,
			Search: query,
		}
		if kv, ok := args["kinds"]; ok {
			if ks, ok := kv.([]any); ok {
				for _, k := range ks {
					if kf, ok := k.(float64); ok {
						filter.Kinds = append(filter.Kinds, nostr.Kind(int(kf)))
					}
				}
			}
		}

		timeoutSec := 10
		if v, ok := args["timeout_seconds"].(float64); ok && v > 0 {
			timeoutSec = int(v)
		}
		ctx2, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()

		var events []map[string]any
		for re := range getPool().SubscribeMany(ctx2, relays, filter, nostr.SubscriptionOptions{}) {
			events = append(events, eventToMap(re.Event))
			if len(events) >= limit {
				break
			}
		}
		out, _ := json.Marshal(map[string]any{"query": query, "events": events, "count": len(events)})
		return string(out), nil
	}, NostrSearchDef)

	// ── NIP-78: App-specific data (kind 30078) ─────────────────────────────

	tools.RegisterWithDef("nostr_appdata_set", func(ctx context.Context, args map[string]any) (string, error) {
		appID, _ := args["app_id"].(string)
		key, _ := args["key"].(string)
		value, _ := args["value"].(string)
		relays := opts.resolveRelays(toStringSlice(args["relays"]))

		if appID == "" || key == "" {
			return "", fmt.Errorf("nostr_appdata_set: app_id and key are required")
		}

		dTag := appID + ":" + key
		evt := nostr.Event{
			Kind:      30078,
			CreatedAt: nostr.Now(),
			Tags:      nostr.Tags{{"d", dTag}},
			Content:   value,
		}
		evID, err := publishEvent(ctx, evt, relays)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "event_id": evID, "app_id": appID, "key": key})
		return string(out), nil
	}, NostrAppDataSetDef)

	tools.RegisterWithDef("nostr_appdata_get", func(ctx context.Context, args map[string]any) (string, error) {
		appID, _ := args["app_id"].(string)
		key, _ := args["key"].(string)
		author, _ := args["author"].(string)
		relays := opts.resolveRelays(toStringSlice(args["relays"]))

		if appID == "" || key == "" {
			return "", fmt.Errorf("nostr_appdata_get: app_id and key are required")
		}
		if author == "" {
			keyer := opts.ResolveKeyer()
			if keyer == nil {
				return "", fmt.Errorf("nostr_appdata_get: author required: signing keyer not configured")
			}
			pk, err := keyer.GetPublicKey(ctx)
			if err != nil {
				return "", fmt.Errorf("nostr_appdata_get: get pubkey: %w", err)
			}
			author = pk.Hex()
		}

		dTag := appID + ":" + key
		filter := nostr.Filter{
			Kinds: []nostr.Kind{30078},
			Tags:  nostr.TagMap{"d": []string{dTag}},
			Limit: 1,
		}
		pk, err := nostr.PubKeyFromHex(author)
		if err != nil {
			return "", fmt.Errorf("nostr_appdata_get: invalid author: %w", err)
		}
		filter.Authors = []nostr.PubKey{pk}

		ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		var best *nostr.Event
		for re := range getPool().SubscribeMany(ctx2, relays, filter, nostr.SubscriptionOptions{}) {
			if best == nil || re.Event.CreatedAt > best.CreatedAt {
				ev := re.Event
				best = &ev
			}
		}
		if best == nil {
			out, _ := json.Marshal(map[string]any{"found": false, "app_id": appID, "key": key})
			return string(out), nil
		}
		out, _ := json.Marshal(map[string]any{
			"found":  true,
			"app_id": appID,
			"key":    key,
			"value":  best.Content,
		})
		return string(out), nil
	}, NostrAppDataGetDef)

	// ── NIP-94: File metadata (kind 1063) ──────────────────────────────────

	tools.RegisterWithDef("nostr_file_announce", func(ctx context.Context, args map[string]any) (string, error) {
		url, _ := args["url"].(string)
		mimeType, _ := args["mime_type"].(string)
		sha256Hex, _ := args["sha256"].(string)
		description, _ := args["description"].(string)
		relays := opts.resolveRelays(toStringSlice(args["relays"]))

		if url == "" || mimeType == "" {
			return "", fmt.Errorf("nostr_file_announce: url and mime_type are required")
		}

		tags := nostr.Tags{
			{"url", url},
			{"m", mimeType},
		}
		if sha256Hex != "" {
			tags = append(tags, nostr.Tag{"x", sha256Hex})
		}
		if description != "" {
			tags = append(tags, nostr.Tag{"alt", description})
		}
		if v, ok := args["size"].(float64); ok {
			tags = append(tags, nostr.Tag{"size", fmt.Sprintf("%d", int64(v))})
		}
		if v, ok := args["dim"].(string); ok && v != "" {
			tags = append(tags, nostr.Tag{"dim", v})
		}

		evt := nostr.Event{
			Kind:      1063,
			CreatedAt: nostr.Now(),
			Tags:      tags,
			Content:   description,
		}
		evID, err := publishEvent(ctx, evt, relays)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "event_id": evID, "url": url})
		return string(out), nil
	}, NostrFileAnnounceDef)

	_ = getPool // ensure getPool is used
}

// slugify converts a title to a URL-friendly d-tag.
func slugify(title string) string {
	title = strings.ToLower(title)
	var buf strings.Builder
	for _, r := range title {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			buf.WriteRune(r)
		} else if r == ' ' || r == '-' || r == '_' {
			buf.WriteByte('-')
		}
	}
	s := strings.Trim(buf.String(), "-")
	if len(s) > 64 {
		s = s[:64]
	}
	if s == "" {
		s = fmt.Sprintf("article-%d", time.Now().Unix())
	}
	return s
}

func autoArticleSummary(content string) string {
	plain := content
	replacer := strings.NewReplacer("*", " ", "_", " ", "`", " ", "#", " ", "[", " ", "]", " ", "(", " ", ")", " ", "!", " ", "\n", " ", "\r", " ", "\t", " ")
	plain = replacer.Replace(plain)
	plain = strings.Join(strings.Fields(plain), " ")
	// Use rune slicing for proper UTF-8 handling
	runes := []rune(plain)
	if len(runes) > 240 {
		plain = strings.TrimSpace(string(runes[:240])) + "…"
	}
	return plain
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func firstMarkdownImage(content string) string {
	needle := "!["
	searchPos := 0
	for {
		i := strings.Index(content[searchPos:], needle)
		if i < 0 {
			return ""
		}
		absPos := searchPos + i
		rest := content[absPos+len(needle):]
		closeAlt := strings.Index(rest, "](")
		if closeAlt < 0 {
			return ""
		}
		after := rest[closeAlt+2:]
		closeURL := strings.Index(after, ")")
		if closeURL < 0 {
			return ""
		}
		url := strings.TrimSpace(after[:closeURL])
		if url != "" {
			return url
		}
		// Move search position past this invalid image
		searchPos = absPos + len(needle) + closeAlt + 2 + closeURL + 1
	}
}

func inferHashtags(content string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 4)
	for _, tok := range strings.Fields(content) {
		if !strings.HasPrefix(tok, "#") || len(tok) < 2 {
			continue
		}
		tag := strings.TrimLeft(tok, "#")
		tag = strings.Trim(tag, ".,;:!?()[]{}\"'")
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}
