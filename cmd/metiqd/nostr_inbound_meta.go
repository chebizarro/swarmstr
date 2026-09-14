package main

import (
	"context"
	"strings"

	"metiq/internal/gateway/channels"
	"metiq/internal/store/state"
	nostr "fiatjaf.com/nostr"

	"metiq/internal/nostr/refresolve"
	nostrmeta "metiq/internal/nostr/metadata"
	nostruntime "metiq/internal/nostr/runtime"
)

type inboundTurnMeta struct {
	renderedBlock string
}

// buildInboundMeta builds a nostrmeta.Metadata from an InboundDM.
// Returns nil for deletion events.
func buildInboundMeta(msg nostruntime.InboundDM, botPubkey string) *nostrmeta.Metadata {
	if msg.Kind == nostr.KindDeletion {
		return nil
	}

	tags := make([][]string, len(msg.Tags))
	for i, t := range msg.Tags {
		tags[i] = []string(t)
	}

	input := nostrmeta.Input{
		Protocol:     protocolFromScheme(msg.Scheme),
		Tags:         tags,
		Content:      msg.Text,
		SelfPubkey:   botPubkey,
		SenderPubkey: msg.FromPubKey,
		Subject:      msg.Subject,
	}

	meta := nostrmeta.Build(input)

	if msg.Kind == nostr.KindReaction {
		meta.Handling = nil
		meta.Participants = nil
		meta.Attachments = nil
		meta.Subject = ""
		meta.UnknownTags = nil
		if meta.Thread != nil {
			meta.Thread.Quote = nil
			meta.Thread.Root = nil
		}
	}

	if meta.Participants != nil && meta.Participants.IsMultiParty {
		meta.ReplyScope = "all_participants"
	}

	return &meta
}

// renderInboundBlock builds and renders a nostrmeta context block from a DM
// InboundDM. Returns empty string when nothing survives normalization.
func renderInboundBlock(msg nostruntime.InboundDM, botPubkey string) string {
	meta := buildInboundMeta(msg, botPubkey)
	if meta == nil {
		return ""
	}
	return renderMetaBlock(meta)
}

// buildRoomInboundMeta builds a nostrmeta.Metadata from a room InboundMessage.
// Returns nil when protocol is empty.
func buildRoomInboundMeta(msg channels.InboundMessage, botPubkey string) *nostrmeta.Metadata {
	if msg.Protocol == "" {
		return nil
	}

	tags := make([][]string, len(msg.Tags))
	for i, t := range msg.Tags {
		tags[i] = []string(t)
	}

	input := nostrmeta.Input{
		Protocol:     msg.Protocol,
		Tags:         tags,
		Community:    msg.Community,
		Content:      msg.Text,
		SelfPubkey:   botPubkey,
		SenderPubkey: msg.FromPubKey,
	}

	meta := nostrmeta.Build(input)
	return &meta
}

// renderRoomInboundBlock builds and renders a nostrmeta context block from a
// room InboundMessage. Returns empty string when nothing survives normalization.
func renderRoomInboundBlock(msg channels.InboundMessage, botPubkey string) string {
	meta := buildRoomInboundMeta(msg, botPubkey)
	if meta == nil {
		return ""
	}
	return renderMetaBlock(meta)
}

// renderMetaBlock renders a nostrmeta.Metadata into a context block string.
func renderMetaBlock(meta *nostrmeta.Metadata) string {
	payload, ok := nostrmeta.Render(*meta, nostrmeta.RenderOptions{})
	if !ok {
		return ""
	}
	return nostrmeta.ContextBlock(payload)
}

func protocolFromScheme(scheme string) nostrmeta.Protocol {
	switch scheme {
	case "nip04":
		return nostrmeta.ProtocolNIP04
	case "nip17":
		return nostrmeta.ProtocolNIP17
	default:
		return nostrmeta.ProtocolNIP17
	}
}

// referenceContextConfig controls the reference resolution behaviour.
type referenceContextConfig struct {
	Enabled       bool
	MaxRefs       int
	MaxBlockChars int
	RelayFetch    bool
}

// resolveReferenceContextConfig reads Extra["nostr"]["reference_context"]
// from the daemon config and returns a populated config struct with defaults.
func resolveReferenceContextConfig(cfg state.ConfigDoc) referenceContextConfig {
	out := referenceContextConfig{
		Enabled:       true,
		MaxRefs:       3,
		MaxBlockChars: 2000,
		RelayFetch:    true,
	}
	if cfg.Extra == nil {
		return out
	}
	extra, ok := cfg.Extra["nostr"]
	if !ok {
		return out
	}
	nostrCfg, ok := extra.(map[string]any)
	if !ok {
		return out
	}
	rc, ok := nostrCfg["reference_context"]
	if !ok {
		return out
	}
	rcMap, ok := rc.(map[string]any)
	if !ok {
		return out
	}
	if v, ok := rcMap["enabled"].(bool); ok {
		out.Enabled = v
	}
	if v, ok := rcMap["max_refs"].(float64); ok {
		out.MaxRefs = int(v)
	} else if v, ok := rcMap["max_refs"].(int); ok {
		out.MaxRefs = v
	}
	if v, ok := rcMap["max_block_chars"].(float64); ok {
		out.MaxBlockChars = int(v)
	} else if v, ok := rcMap["max_block_chars"].(int); ok {
		out.MaxBlockChars = v
	}
	if v, ok := rcMap["relay_fetch"].(bool); ok {
		out.RelayFetch = v
	}
	return out
}

// buildInboundTurnBlock renders the metadata context block and the referenced
// Nostr events block for a DM, then concatenates them. Resolution happens here
// (at render time), not inside ProcessTurn. Returns "" when nothing survives.
func buildInboundTurnBlock(ctx context.Context, msg nostruntime.InboundDM, botPubkey string, cfg referenceContextConfig, chained, localOnly refresolve.Resolver) string {
	meta := buildInboundMeta(msg, botPubkey)
	if meta == nil {
		return ""
	}
	metaBlock := renderMetaBlock(meta)
	refBlock := renderResolvedRefBlock(ctx, meta.Protocol, meta.Thread, msg.EventID, cfg, chained, localOnly, metaBlock != "")
	if refBlock == "" {
		return metaBlock
	}
	return strings.TrimSpace(metaBlock + "\n" + refBlock)
}

// buildRoomTurnBlock renders the room metadata context block and the
// referenced Nostr events block, then concatenates them. Returns "" when
// nothing survives.
func buildRoomTurnBlock(ctx context.Context, msg channels.InboundMessage, botPubkey string, cfg referenceContextConfig, chained, localOnly refresolve.Resolver) string {
	meta := buildRoomInboundMeta(msg, botPubkey)
	if meta == nil {
		return ""
	}
	metaBlock := renderMetaBlock(meta)
	refBlock := renderResolvedRefBlock(ctx, meta.Protocol, meta.Thread, msg.EventID, cfg, chained, localOnly, metaBlock != "")
	if refBlock == "" {
		return metaBlock
	}
	return strings.TrimSpace(metaBlock + "\n" + refBlock)
}

// renderResolvedRefBlock selects the appropriate resolver for the protocol,
// resolves the thread refs, and renders the referenced block. Returns ""
// when disabled or nothing resolves.
func renderResolvedRefBlock(ctx context.Context, protocol nostrmeta.Protocol, thread *nostrmeta.ThreadRefs, selfEventID string, cfg referenceContextConfig, chained, localOnly refresolve.Resolver, hasMetaBlock bool) string {
	if !cfg.Enabled || cfg.MaxRefs == 0 {
		return ""
	}
	var resolver refresolve.Resolver
	if !cfg.RelayFetch {
		resolver = localOnly
	} else {
		resolver = refresolve.ResolverFor(protocol, chained, localOnly)
	}
	block := renderReferencedBlock(ctx, resolver, thread, selfEventID, cfg)
	if block == "" {
		return ""
	}
	return block
}
