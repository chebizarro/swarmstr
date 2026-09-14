package main

import (
	nostr "fiatjaf.com/nostr"

	nostrmeta "metiq/internal/nostr/metadata"
	nostruntime "metiq/internal/nostr/runtime"
)

type inboundTurnMeta struct {
	renderedBlock string
}

func renderInboundBlock(msg nostruntime.InboundDM, botPubkey string) string {
	if msg.Kind == nostr.KindDeletion {
		return ""
	}

	// Convert nostr.Tags ([]Tag where Tag is []string) to [][]string.
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

	payload, ok := nostrmeta.Render(meta, nostrmeta.RenderOptions{})
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