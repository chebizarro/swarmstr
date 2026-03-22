// Package toolbuiltin nostr_batch.go — batch event signing and publishing.
//
// Lets the agent sign and publish multiple Nostr events in a single tool call,
// reducing round-trips for multi-event workflows (e.g. note + reaction + list update).
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/agent"
)

// ─── Tool Definition ─────────────────────────────────────────────────────────

// NostrPublishBatchDef is the ToolDefinition for nostr_publish_batch.
var NostrPublishBatchDef = agent.ToolDefinition{
	Name:        "nostr_publish_batch",
	Description: "Sign and publish multiple Nostr events in one call. Each event spec has kind, content, and optional tags. All events are signed and published in parallel. Returns per-event results.",
	Parameters: agent.ToolParameters{
		Type:     "object",
		Required: []string{"events"},
		Properties: map[string]agent.ToolParamProp{
			"events": {
				Type:        "array",
				Description: "Array of event specs. Each spec: {\"kind\": int, \"content\": string, \"tags\": [[\"e\",\"...\"], ...]}. Max 20 events per batch.",
				Items: &agent.ToolParamProp{
					Type: "object",
				},
			},
			"relays": {
				Type:        "array",
				Description: "Optional relay URLs (overrides defaults). Applied to all events in the batch.",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
	},
}

// ─── Tool Implementation ─────────────────────────────────────────────────────

const maxBatchSize = 20

// NostrPublishBatchTool returns an agent tool that signs and publishes
// multiple events in parallel.
func NostrPublishBatchTool(opts NostrToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		signFn, err := opts.signerFunc()
		if err != nil {
			return "", nostrToolErr("nostr_publish_batch", "no_keyer", err.Error(), nil)
		}

		eventsRaw, ok := args["events"].([]any)
		if !ok || len(eventsRaw) == 0 {
			return "", nostrToolErr("nostr_publish_batch", "invalid_input", "events array is required and must be non-empty", nil)
		}
		if len(eventsRaw) > maxBatchSize {
			return "", nostrToolErr("nostr_publish_batch", "invalid_input",
				fmt.Sprintf("max %d events per batch, got %d", maxBatchSize, len(eventsRaw)), nil)
		}

		relays := opts.resolveRelays(toStringSlice(args["relays"]))
		if len(relays) == 0 {
			return "", nostrToolErr("nostr_publish_batch", "no_relays", "no relays configured", nil)
		}

		// Parse and sign all events first (fail fast on bad input).
		type signedEvent struct {
			evt   nostr.Event
			index int
		}
		signed := make([]signedEvent, 0, len(eventsRaw))

		for i, raw := range eventsRaw {
			spec, ok := raw.(map[string]any)
			if !ok {
				return "", nostrToolErr("nostr_publish_batch", "invalid_input",
					fmt.Sprintf("events[%d] must be an object", i), nil)
			}

			kindVal, ok := spec["kind"].(float64)
			if !ok {
				return "", nostrToolErr("nostr_publish_batch", "invalid_input",
					fmt.Sprintf("events[%d]: kind (int) is required", i), nil)
			}

			content, _ := spec["content"].(string)

			var tags nostr.Tags
			if tagsRaw, ok := spec["tags"]; ok {
				var parseErr error
				tags, parseErr = parseTagsArg(tagsRaw)
				if parseErr != nil {
					return "", nostrToolErr("nostr_publish_batch", "invalid_input",
						fmt.Sprintf("events[%d]: invalid tags: %v", i, parseErr), nil)
				}
			}

			evt := nostr.Event{
				Kind:      nostr.Kind(int(kindVal)),
				Content:   content,
				Tags:      tags,
				CreatedAt: nostr.Timestamp(time.Now().Unix()),
			}
			if err := opts.checkOutboundEvent(&evt); err != nil {
				return "", nostrToolErr("nostr_publish_batch", "content_blocked",
					fmt.Sprintf("events[%d]: %v", i, err), nil)
			}
			if err := signFn(ctx, &evt); err != nil {
				return "", nostrToolErr("nostr_publish_batch", "sign_failed",
					fmt.Sprintf("events[%d]: %v", i, err), nil)
			}

			signed = append(signed, signedEvent{evt: evt, index: i})
		}

		// Publish all events in parallel.
		pool, releasePool := opts.AcquirePool("publish_batch done")
		defer releasePool()

		pubCtx, pubCancel := context.WithTimeout(ctx, 20*time.Second)
		defer pubCancel()

		type publishResult struct {
			index     int
			eventID   string
			kind      int
			published int
			err       error
		}

		results := make([]publishResult, len(signed))
		var wg sync.WaitGroup
		for idx, se := range signed {
			wg.Add(1)
			go func(idx int, evt nostr.Event) {
				defer wg.Done()
				pr := publishResult{
					index:   idx,
					eventID: evt.ID.Hex(),
					kind:    int(evt.Kind),
				}
				var lastErr error
				for _, relayURL := range relays {
					r, rErr := pool.EnsureRelay(relayURL)
					if rErr != nil {
						lastErr = rErr
						continue
					}
					if pErr := r.Publish(pubCtx, evt); pErr != nil {
						lastErr = pErr
						continue
					}
					pr.published++
				}
				if pr.published == 0 && lastErr != nil {
					pr.err = lastErr
				}
				results[idx] = pr
			}(idx, se.evt)
		}
		wg.Wait()

		// Build response.
		totalPublished := 0
		totalFailed := 0
		eventResults := make([]map[string]any, len(results))
		for i, pr := range results {
			entry := map[string]any{
				"index":     pr.index,
				"event_id":  pr.eventID,
				"kind":      pr.kind,
				"published": pr.published,
			}
			if pr.err != nil {
				entry["error"] = pr.err.Error()
				totalFailed++
			} else {
				totalPublished++
			}
			eventResults[i] = entry
		}

		out, _ := json.Marshal(map[string]any{
			"total":     len(signed),
			"published": totalPublished,
			"failed":    totalFailed,
			"events":    eventResults,
		})
		return string(out), nil
	}
}
