package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/agent"
	"metiq/internal/nostr/nip51"
	nostruntime "metiq/internal/nostr/runtime"
)

var NostrListGetDef = agent.ToolDefinition{
	Name:        "nostr_list_get",
	Description: "Fetch a Nostr list by list_type/kind (supports follows, mute, pins, relay, bookmarks, categorized lists).",
	Parameters: agent.ToolParameters{Type: "object", Properties: map[string]agent.ToolParamProp{
		"list_type": {Type: "string", Description: "follows|mute|pins|relay|bookmarks|people|categorized_bookmarks|allow|block"},
		"kind":      {Type: "integer", Description: "Optional explicit kind override (e.g. 3, 10000, 30000)."},
		"d_tag":     {Type: "string", Description: "Optional d-tag for parameterized replaceable lists (e.g. allowlist)."},
		"pubkey":    {Type: "string", Description: "Owner pubkey (hex or npub). Defaults to caller pubkey."},
	}},
}

var NostrListPutDef = agent.ToolDefinition{
	Name:        "nostr_list_put",
	Description: "Create/replace a Nostr list with provided values. Values are mapped to default tag type for the list kind.",
	Parameters: agent.ToolParameters{Type: "object", Properties: map[string]agent.ToolParamProp{
		"list_type": {Type: "string", Description: "follows|mute|pins|relay|bookmarks|people|categorized_bookmarks|allow|block"},
		"kind":      {Type: "integer", Description: "Optional explicit kind override."},
		"d_tag":     {Type: "string", Description: "Optional d-tag (required for custom kind 30000/30001 lists)."},
		"title":     {Type: "string", Description: "Optional list title tag."},
		"values":    {Type: "array", Description: "List entry values to set.", Items: &agent.ToolParamProp{Type: "string"}},
		"tag":       {Type: "string", Description: "Optional explicit tag type override (p|e|r|a|t)."},
	}, Required: []string{"values"}},
}

var NostrListRemoveDef = agent.ToolDefinition{
	Name:        "nostr_list_remove",
	Description: "Remove a single value from a Nostr list.",
	Parameters: agent.ToolParameters{Type: "object", Properties: map[string]agent.ToolParamProp{
		"list_type": {Type: "string", Description: "follows|mute|pins|relay|bookmarks|people|categorized_bookmarks|allow|block"},
		"kind":      {Type: "integer", Description: "Optional explicit kind override."},
		"d_tag":     {Type: "string", Description: "Optional d-tag for kind 30000/30001 lists."},
		"value":     {Type: "string", Description: "Entry value to remove."},
		"tag":       {Type: "string", Description: "Optional explicit tag type override (p|e|r|a|t)."},
	}, Required: []string{"value"}},
}

var NostrListDeleteDef = agent.ToolDefinition{
	Name:        "nostr_list_delete",
	Description: "Delete/clear a Nostr list by publishing an empty replacement event (and optional delete signal).",
	Parameters: agent.ToolParameters{Type: "object", Properties: map[string]agent.ToolParamProp{
		"list_type": {Type: "string", Description: "follows|mute|pins|relay|bookmarks|people|categorized_bookmarks|allow|block"},
		"kind":      {Type: "integer", Description: "Optional explicit kind override."},
		"d_tag":     {Type: "string", Description: "Optional d-tag for kind 30000/30001 lists."},
	}},
}

func RegisterNostrListSemanticTools(tools *agent.ToolRegistry, opts NostrListToolOpts) {
	var (
		fallbackPool *nostr.Pool
		poolOnce     sync.Once
	)
	getPool := func() *nostr.Pool {
		if opts.HubFunc != nil {
			if h := opts.HubFunc(); h != nil {
				return h.Pool()
			}
		}
		poolOnce.Do(func() {
			fallbackPool = nostr.NewPool(nostruntime.PoolOptsNIP42(opts.Keyer))
		})
		return fallbackPool
	}

	tools.RegisterWithDef("nostr_list_get", func(ctx context.Context, args map[string]any) (string, error) {
		kind, dtag, _, err := resolveSemanticListTarget(args)
		if err != nil {
			return "", semanticListErr("nostr_list_get", "invalid_target", err.Error())
		}
		pubkeyHex := strings.TrimSpace(argString(args, "pubkey"))
		if pubkeyHex == "" {
			ks, err := resolveListKeyer(ctx, opts)
			if err != nil {
				return "", semanticListErr("nostr_list_get", "no_keyer", "pubkey required and no signer configured")
			}
			pk, err := ks.GetPublicKey(ctx)
			if err != nil {
				return "", semanticListErr("nostr_list_get", "signer_failure", "failed to derive caller pubkey")
			}
			pubkeyHex = pk.Hex()
		} else {
			if resolved, err := resolveNostrPubkey(pubkeyHex); err == nil {
				pubkeyHex = resolved
			}
		}
		list, err := nip51.Fetch(ctx, getPool(), opts.Relays, pubkeyHex, kind, dtag)
		if err != nil {
			return "", mapSemanticListErr("nostr_list_get", err)
		}
		out, _ := nip51.MarshalList(list)
		return out, nil
	}, NostrListGetDef)

	tools.RegisterWithDef("nostr_list_put", func(ctx context.Context, args map[string]any) (string, error) {
		kind, dtag, tag, err := resolveSemanticListTarget(args)
		if err != nil {
			return "", semanticListErr("nostr_list_put", "invalid_target", err.Error())
		}
		ks, err := resolveListKeyer(ctx, opts)
		if err != nil {
			return "", semanticListErr("nostr_list_put", "no_keyer", "signing keyer not configured")
		}
		pk, err := ks.GetPublicKey(ctx)
		if err != nil {
			return "", semanticListErr("nostr_list_put", "signer_failure", "failed to derive caller pubkey")
		}
		values := toStringSlice(args["values"])
		entries := make([]nip51.ListEntry, 0, len(values))
		for _, v := range values {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			entries = append(entries, nip51.ListEntry{Tag: tag, Value: v})
		}
		list := &nip51.List{Kind: kind, DTag: dtag, PubKey: pk.Hex(), Title: strings.TrimSpace(argString(args, "title")), Entries: entries}
		evID, err := nip51.Publish(ctx, getPool(), ks, opts.Relays, list)
		if err != nil {
			return "", mapSemanticListErr("nostr_list_put", err)
		}
		return nostrWriteSuccessEnvelope("nostr_list_put", evID, kind, map[string]any{
			"d_tag":  dtag,
			"values": values,
		}, map[string]any{
			"count": len(entries),
		}, map[string]any{
			"d_tag": dtag,
			"count": len(entries),
		}), nil
	}, NostrListPutDef)

	tools.RegisterWithDef("nostr_list_remove", func(ctx context.Context, args map[string]any) (string, error) {
		kind, dtag, tag, err := resolveSemanticListTarget(args)
		if err != nil {
			return "", semanticListErr("nostr_list_remove", "invalid_target", err.Error())
		}
		value := strings.TrimSpace(argString(args, "value"))
		if value == "" {
			return "", semanticListErr("nostr_list_remove", "missing_value", "value is required")
		}
		ks, err := resolveListKeyer(ctx, opts)
		if err != nil {
			return "", semanticListErr("nostr_list_remove", "no_keyer", "signing keyer not configured")
		}
		pk, err := ks.GetPublicKey(ctx)
		if err != nil {
			return "", semanticListErr("nostr_list_remove", "signer_failure", "failed to derive caller pubkey")
		}
		evID, err := nip51.RemoveEntry(ctx, getPool(), ks, opts.Relays, pk.Hex(), kind, dtag, tag, value)
		if err != nil {
			return "", mapSemanticListErr("nostr_list_remove", err)
		}
		return nostrWriteSuccessEnvelope("nostr_list_remove", evID, kind, map[string]any{
			"d_tag": dtag,
			"value": value,
		}, nil, map[string]any{
			"d_tag": dtag,
			"value": value,
		}), nil
	}, NostrListRemoveDef)

	tools.RegisterWithDef("nostr_list_delete", func(ctx context.Context, args map[string]any) (string, error) {
		kind, dtag, _, err := resolveSemanticListTarget(args)
		if err != nil {
			return "", semanticListErr("nostr_list_delete", "invalid_target", err.Error())
		}
		ks, err := resolveListKeyer(ctx, opts)
		if err != nil {
			return "", semanticListErr("nostr_list_delete", "no_keyer", "signing keyer not configured")
		}
		pk, err := ks.GetPublicKey(ctx)
		if err != nil {
			return "", semanticListErr("nostr_list_delete", "signer_failure", "failed to derive caller pubkey")
		}
		list := &nip51.List{Kind: kind, DTag: dtag, PubKey: pk.Hex(), Entries: nil}
		evID, err := nip51.Publish(ctx, getPool(), ks, opts.Relays, list)
		if err != nil {
			return "", mapSemanticListErr("nostr_list_delete", err)
		}
		return nostrWriteSuccessEnvelope("nostr_list_delete", evID, kind, map[string]any{
			"d_tag": dtag,
		}, nil, map[string]any{
			"d_tag": dtag,
		}), nil
	}, NostrListDeleteDef)
}

func resolveSemanticListTarget(args map[string]any) (kind int, dtag string, tag string, err error) {
	kind = 0
	if v, ok := args["kind"].(float64); ok {
		kind = int(v)
	}
	listType := strings.ToLower(strings.TrimSpace(argString(args, "list_type")))
	dtag = strings.TrimSpace(argString(args, "d_tag"))
	tag = strings.TrimSpace(argString(args, "tag"))
	if kind == 0 {
		switch listType {
		case "follows", "follow":
			kind = 3
		case "mute", "mutes":
			kind = 10000
		case "pins", "pin":
			kind = 10001
		case "relay", "relays":
			kind = 10002
		case "bookmarks", "bookmark":
			kind = 10003
		case "categorized_bookmarks":
			kind = 30001
		case "people", "categorized_people":
			kind = 30000
		case "allow", "allowlist":
			kind = 30000
			if dtag == "" {
				dtag = "allowlist"
			}
		case "block", "blocklist":
			kind = 30000
			if dtag == "" {
				dtag = "blocklist"
			}
		default:
			kind = nip51.KindMuteList
		}
	}
	if tag == "" {
		tag = defaultListTag(kind)
	}
	if (kind == 30000 || kind == 30001) && dtag == "" {
		dtag = listType
	}
	return kind, dtag, tag, nil
}

func defaultListTag(kind int) string {
	switch kind {
	case 10001, 10003:
		return "e"
	case 10002:
		return "r"
	default:
		return "p"
	}
}

func semanticListErr(op, code, message string) error {
	payload, _ := json.Marshal(map[string]any{
		"op":      op,
		"code":    code,
		"message": strings.TrimSpace(message),
	})
	return fmt.Errorf("nostr_list_error:%s", string(payload))
}

func mapSemanticListErr(op string, err error) error {
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(err.Error())
	lmsg := strings.ToLower(msg)
	switch {
	case strings.Contains(lmsg, "no relay"):
		return semanticListErr(op, "no_relays", "no relays configured or relay publish/query rejected")
	case strings.Contains(lmsg, "list not found"):
		return semanticListErr(op, "list_not_found", "list does not exist for the requested owner/kind/d_tag")
	case strings.Contains(lmsg, "no signing keyer"):
		return semanticListErr(op, "no_keyer", "signing keyer not configured")
	default:
		return semanticListErr(op, "operation_failed", msg)
	}
}
