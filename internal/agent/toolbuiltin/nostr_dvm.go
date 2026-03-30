// Package toolbuiltin nostr_dvm.go — NIP-90 DVM client tools.
//
// Lets the agent submit job requests to other Data Vending Machines and
// optionally poll for results.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/agent"
)

// ─── Tool Definitions ────────────────────────────────────────────────────────

// NostrDVMRequestDef is the ToolDefinition for nostr_dvm_request.
var NostrDVMRequestDef = agent.ToolDefinition{
	Name:        "nostr_dvm_request",
	Description: "Submit a NIP-90 DVM job request (kind 5000-5999) and optionally wait for the result. The job is published to relays and, if wait=true, the tool polls for a kind:6xxx result or kind:7000 status update.",
	Parameters: agent.ToolParameters{
		Type:     "object",
		Required: []string{"kind", "input"},
		Properties: map[string]agent.ToolParamProp{
			"kind": {
				Type:        "integer",
				Description: "DVM request kind (5000-5999). E.g. 5000 for generic text, 5001 for translation.",
			},
			"input": {
				Type:        "string",
				Description: "Input content for the DVM job. Placed in an 'i' tag.",
			},
			"input_type": {
				Type:        "string",
				Description: "Input type hint (e.g. 'text', 'url', 'event'). Placed as 3rd element of 'i' tag. Default: 'text'.",
			},
			"to_pubkey": {
				Type:        "string",
				Description: "Optional: target DVM's pubkey (hex or npub). If provided, adds a 'p' tag to address a specific DVM.",
			},
			"output_type": {
				Type:        "string",
				Description: "Optional: requested output MIME type (placed in 'output' tag).",
			},
			"bid_msats": {
				Type:        "integer",
				Description: "Optional: bid amount in millisats for the job.",
			},
			"extra_tags": {
				Type:        "array",
				Description: "Optional: additional tags as array-of-arrays.",
				Items:       &agent.ToolParamProp{Type: "array"},
			},
			"wait": {
				Type:        "boolean",
				Description: "If true, wait for a result/status event (up to wait_seconds). Default: false.",
			},
			"wait_seconds": {
				Type:        "integer",
				Description: "Max seconds to wait for result when wait=true. Default: 30, max: 120.",
			},
			"relays": {
				Type:        "array",
				Description: "Optional relay URLs (overrides defaults).",
				Items:       &agent.ToolParamProp{Type: "string"},
			},
		},
	},
}

// ─── Tool Implementation ─────────────────────────────────────────────────────

// NostrDVMRequestTool returns an agent tool that submits a NIP-90 DVM job
// request and optionally polls for a result.
func NostrDVMRequestTool(opts NostrToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		signFn, err := opts.signerFunc()
		if err != nil {
			return "", nostrToolErr("nostr_dvm_request", "no_keyer", err.Error(), nil)
		}

		kindVal, ok := args["kind"].(float64)
		if !ok || int(kindVal) < 5000 || int(kindVal) > 5999 {
			return "", nostrToolErr("nostr_dvm_request", "invalid_input", "kind must be 5000-5999", nil)
		}
		kind := int(kindVal)

		input, _ := args["input"].(string)
		if input == "" {
			return "", nostrToolErr("nostr_dvm_request", "invalid_input", "input is required", nil)
		}

		inputType := "text"
		if v, _ := args["input_type"].(string); v != "" {
			inputType = v
		}

		relays := opts.resolveRelays(toStringSlice(args["relays"]))
		if len(relays) == 0 {
			return "", nostrToolErr("nostr_dvm_request", "no_relays", "no relays configured", nil)
		}

		// Build tags.
		tags := nostr.Tags{
			{"i", input, inputType},
		}

		// Optional: target DVM pubkey.
		if toPk, _ := args["to_pubkey"].(string); toPk != "" {
			toPk, err := resolveNostrPubkey(toPk)
			if err != nil {
				return "", nostrToolErr("nostr_dvm_request", "invalid_input", fmt.Sprintf("invalid to_pubkey: %v", err), nil)
			}
			tags = append(tags, nostr.Tag{"p", toPk})
		}

		// Optional: output type.
		if ot, _ := args["output_type"].(string); ot != "" {
			tags = append(tags, nostr.Tag{"output", ot})
		}

		// Optional: bid.
		if bid, ok := args["bid_msats"].(float64); ok && bid > 0 {
			tags = append(tags, nostr.Tag{"bid", fmt.Sprintf("%d", int64(bid))})
		}

		// Optional: extra tags.
		if extra, ok := args["extra_tags"]; ok {
			extraTags, parseErr := parseTagsArg(extra)
			if parseErr != nil {
				return "", nostrToolErr("nostr_dvm_request", "invalid_input", fmt.Sprintf("invalid extra_tags: %v", parseErr), nil)
			}
			tags = append(tags, extraTags...)
		}

		// Build and sign the job request event.
		evt := nostr.Event{
			Kind:      nostr.Kind(kind),
			Content:   "",
			Tags:      tags,
			CreatedAt: nostr.Timestamp(time.Now().Unix()),
		}
		if err := opts.checkOutboundEvent(&evt); err != nil {
			return "", nostrToolErr("nostr_dvm_request", "content_blocked", err.Error(), nil)
		}
		if err := signFn(ctx, &evt); err != nil {
			return "", nostrToolErr("nostr_dvm_request", "sign_failed", err.Error(), nil)
		}

		// Publish to relays.
		pool, releasePool := opts.AcquirePool("dvm_request done")
		defer releasePool()

		pubCtx, pubCancel := context.WithTimeout(ctx, 15*time.Second)
		defer pubCancel()

		published := 0
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
			published++
		}
		if published == 0 && lastErr != nil {
			return "", nostrToolErr("nostr_dvm_request", "publish_failed", lastErr.Error(), map[string]any{"kind": kind})
		}

		jobID := evt.ID.Hex()
		result := map[string]any{
			"job_id":    jobID,
			"kind":      kind,
			"published": published,
		}

		// Optionally wait for result.
		waitForResult, _ := args["wait"].(bool)
		if waitForResult {
			waitSec := 30
			if v, ok := args["wait_seconds"].(float64); ok && v > 0 {
				waitSec = int(v)
				if waitSec > 120 {
					waitSec = 120
				}
			}

			dvmResult, dvmStatus, pollErr := pollDVMResult(ctx, pool, relays, jobID, kind, waitSec)
			if pollErr != nil {
				result["poll_error"] = pollErr.Error()
			}
			if dvmResult != nil {
				result["result"] = eventToMap(*dvmResult)
				result["result_content"] = dvmResult.Content
			}
			if dvmStatus != "" {
				result["status"] = dvmStatus
			}
		}

		out, _ := json.Marshal(result)
		return string(out), nil
	}
}

// pollDVMResult subscribes for kind:6xxx result and kind:7000 status events
// referencing the given job ID, returning the first result event found.
func pollDVMResult(ctx context.Context, pool *nostr.Pool, relays []string, jobID string, requestKind, timeoutSec int) (*nostr.Event, string, error) {
	resultKind := nostr.Kind(requestKind + 1000) // 5000 → 6000
	statusKind := nostr.Kind(7000)

	pollCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	f := nostr.Filter{
		Kinds: []nostr.Kind{resultKind, statusKind},
		Tags:  nostr.TagMap{"e": []string{jobID}},
	}

	// SubscribeMany is correct here: the DVM result event is published
	// asynchronously after the job completes, so we need live event delivery
	// past EOSE (FetchMany would close at EOSE and miss it).  The timeout
	// context provides the hard upper bound.
	sub := pool.SubscribeMany(pollCtx, relays, f, nostr.SubscriptionOptions{})

	var lastStatus string
	for re := range sub {
		ev := re.Event
		if ev.Kind == resultKind {
			return &ev, "success", nil
		}
		if ev.Kind == statusKind {
			// Extract status from "status" tag.
			for _, tag := range ev.Tags {
				if len(tag) >= 2 && tag[0] == "status" {
					lastStatus = tag[1]
					if lastStatus == "error" {
						return nil, "error", fmt.Errorf("DVM returned error: %s", ev.Content)
					}
				}
			}
		}
	}

	if lastStatus != "" {
		return nil, lastStatus, fmt.Errorf("timed out waiting for result (last status: %s)", lastStatus)
	}
	return nil, "", fmt.Errorf("timed out waiting for DVM result after %ds", timeoutSec)
}
