// Package toolbuiltin – NIP-51 list management tools.
//
// Registers: list_get, list_add, list_remove, list_create, list_delete,
//            list_check_allowlist
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"

	nostr "fiatjaf.com/nostr"

	"swarmstr/internal/agent"
	"swarmstr/internal/nostr/nip51"
)

// NostrListToolOpts configures the NIP-51 list tools.
type NostrListToolOpts struct {
	Keyer      nostr.Keyer
	Relays     []string
	Store      *nip51.ListStore // shared in-process cache
}

// resolveListKeyer resolves the signing keyer from opts.
func resolveListKeyer(ctx context.Context, opts NostrListToolOpts) (nostr.Keyer, error) {
	if opts.Keyer == nil {
		return nil, fmt.Errorf("no signing keyer configured")
	}
	return opts.Keyer, nil
}

// RegisterListTools registers all NIP-51 list tools into the given registry.
func RegisterListTools(tools *agent.ToolRegistry, opts NostrListToolOpts) {
	pool := nostr.NewPool(NostrToolOpts{Keyer: opts.Keyer}.PoolOptsNIP42())

	// list_get – fetch a NIP-51 list from relays.
	tools.RegisterWithDef("list_get", func(ctx context.Context, args map[string]any) (string, error) {
		kind := 0
		if v, ok := args["kind"].(float64); ok {
			kind = int(v)
		}
		dtag, _ := args["d_tag"].(string)
		pubkeyHex, _ := args["pubkey"].(string)

		if pubkeyHex == "" {
			ks, err := resolveListKeyer(ctx, opts)
			if err != nil {
				return "", fmt.Errorf("list_get: pubkey required: %w", err)
			}
			pk, err := ks.GetPublicKey(ctx)
			if err != nil {
				return "", fmt.Errorf("list_get: get pubkey: %w", err)
			}
			pubkeyHex = pk.Hex()
		}
		if kind == 0 {
			kind = nip51.KindMuteList
		}
		list, err := nip51.Fetch(ctx, pool, opts.Relays, pubkeyHex, kind, dtag)
		if err != nil {
			return "", err
		}
		out, _ := nip51.MarshalList(list)
		return out, nil
	}, ListGetDef)

	// list_add – add an entry to a list.
	tools.RegisterWithDef("list_add", func(ctx context.Context, args map[string]any) (string, error) {
		kind := 0
		if v, ok := args["kind"].(float64); ok {
			kind = int(v)
		}
		dtag, _ := args["d_tag"].(string)
		tag, _ := args["tag"].(string)
		value, _ := args["value"].(string)
		relayHint, _ := args["relay"].(string)
		petname, _ := args["petname"].(string)

		if tag == "" || value == "" {
			return "", fmt.Errorf("list_add: tag and value are required")
		}
		ks, err := resolveListKeyer(ctx, opts)
		if err != nil {
			return "", fmt.Errorf("list_add: %w", err)
		}
		pk, err := ks.GetPublicKey(ctx)
		if err != nil {
			return "", fmt.Errorf("list_add: get pubkey: %w", err)
		}
		if kind == 0 {
			kind = nip51.KindMuteList
		}
		entry := nip51.ListEntry{Tag: tag, Value: value, Relay: relayHint, Petname: petname}
		evID, err := nip51.AddEntry(ctx, pool, ks, opts.Relays, pk.Hex(), kind, dtag, entry)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "event_id": evID})
		return string(out), nil
	}, ListAddDef)

	// list_remove – remove an entry from a list.
	tools.RegisterWithDef("list_remove", func(ctx context.Context, args map[string]any) (string, error) {
		kind := 0
		if v, ok := args["kind"].(float64); ok {
			kind = int(v)
		}
		dtag, _ := args["d_tag"].(string)
		tag, _ := args["tag"].(string)
		value, _ := args["value"].(string)

		if tag == "" || value == "" {
			return "", fmt.Errorf("list_remove: tag and value are required")
		}
		ks, err := resolveListKeyer(ctx, opts)
		if err != nil {
			return "", fmt.Errorf("list_remove: %w", err)
		}
		pk, err := ks.GetPublicKey(ctx)
		if err != nil {
			return "", fmt.Errorf("list_remove: get pubkey: %w", err)
		}
		if kind == 0 {
			kind = nip51.KindMuteList
		}
		evID, err := nip51.RemoveEntry(ctx, pool, ks, opts.Relays, pk.Hex(), kind, dtag, tag, value)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "event_id": evID})
		return string(out), nil
	}, ListRemoveDef)

	// list_create – create a new named list (kind 30000 with d-tag).
	tools.RegisterWithDef("list_create", func(ctx context.Context, args map[string]any) (string, error) {
		name, _ := args["name"].(string)
		title, _ := args["title"].(string)
		kind := 0
		if v, ok := args["kind"].(float64); ok {
			kind = int(v)
		}
		if name == "" {
			return "", fmt.Errorf("list_create: name (d-tag) is required")
		}
		ks, err := resolveListKeyer(ctx, opts)
		if err != nil {
			return "", fmt.Errorf("list_create: %w", err)
		}
		pk, err := ks.GetPublicKey(ctx)
		if err != nil {
			return "", fmt.Errorf("list_create: get pubkey: %w", err)
		}
		if kind == 0 {
			kind = nip51.KindPeopleList
		}
		list := &nip51.List{
			Kind:   kind,
			DTag:   name,
			PubKey: pk.Hex(),
			Title:  title,
		}
		evID, err := nip51.Publish(ctx, pool, ks, opts.Relays, list)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "event_id": evID, "d_tag": name})
		return string(out), nil
	}, ListCreateDef)

	// list_delete – clear a list (publish empty replaceable event + NIP-09 deletion).
	tools.RegisterWithDef("list_delete", func(ctx context.Context, args map[string]any) (string, error) {
		kind := 0
		if v, ok := args["kind"].(float64); ok {
			kind = int(v)
		}
		dtag, _ := args["d_tag"].(string)

		ks, err := resolveListKeyer(ctx, opts)
		if err != nil {
			return "", fmt.Errorf("list_delete: %w", err)
		}
		pk, err := ks.GetPublicKey(ctx)
		if err != nil {
			return "", fmt.Errorf("list_delete: get pubkey: %w", err)
		}
		if kind == 0 {
			kind = nip51.KindPeopleList
		}
		// Publish empty replaceable event to clear the list.
		list := &nip51.List{Kind: kind, DTag: dtag, PubKey: pk.Hex()}
		evID, err := nip51.Publish(ctx, pool, ks, opts.Relays, list)
		if err != nil {
			return "", err
		}
		// Also publish NIP-09 deletion event.
		aTag := fmt.Sprintf("%d:%s:%s", kind, pk.Hex(), dtag)
		delEvt := nostr.Event{
			Kind:      5,
			CreatedAt: nostr.Now(),
			Tags:      nostr.Tags{{"a", aTag}},
			Content:   "list deleted",
		}
		if signErr := ks.SignEvent(ctx, &delEvt); signErr == nil {
			for range pool.PublishMany(ctx, opts.Relays, delEvt) {
			}
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "event_id": evID})
		return string(out), nil
	}, ListDeleteDef)

	// list_check_allowlist – check if a pubkey passes allow/block filtering.
	tools.RegisterWithDef("list_check_allowlist", func(ctx context.Context, args map[string]any) (string, error) {
		pubkeyHex, _ := args["pubkey"].(string)
		ownerHex, _ := args["owner_pubkey"].(string)

		if ownerHex == "" {
			ks, err := resolveListKeyer(ctx, opts)
			if err != nil {
				return "", fmt.Errorf("list_check_allowlist: %w", err)
			}
			pk, err := ks.GetPublicKey(ctx)
			if err != nil {
				return "", fmt.Errorf("list_check_allowlist: get pubkey: %w", err)
			}
			ownerHex = pk.Hex()
		}
		if opts.Store == nil {
			return `{"error":"list store not configured"}`, nil
		}
		muted := opts.Store.IsMuted(ownerHex, pubkeyHex)
		blocked := opts.Store.IsBlocked(ownerHex, pubkeyHex)
		allowed := opts.Store.IsAllowed(ownerHex, pubkeyHex)
		out, _ := json.Marshal(map[string]any{
			"pubkey":  pubkeyHex,
			"muted":   muted,
			"blocked": blocked,
			"allowed": allowed,
			"pass":    !muted && !blocked && allowed,
		})
		return string(out), nil
	}, ListCheckAllowlistDef)

}
