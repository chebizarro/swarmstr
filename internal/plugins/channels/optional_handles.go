package channels

import (
	"context"

	"metiq/internal/plugins/sdk"
)

func wrapHandleByCapabilities(base *PluginChannelHandle, caps sdk.ChannelCapabilities) sdk.ChannelHandle {
	mask := 0
	if caps.Typing {
		mask |= 1
	}
	if caps.Reactions {
		mask |= 2
	}
	if caps.Threads {
		mask |= 4
	}
	if caps.Audio {
		mask |= 8
	}
	if caps.Edit {
		mask |= 16
	}
	switch mask {
	case 0:
		return base
	case 1:
		return &pluginChannelHandleT{PluginChannelHandle: base}
	case 2:
		return &pluginChannelHandleR{PluginChannelHandle: base}
	case 3:
		return &pluginChannelHandleTR{PluginChannelHandle: base}
	case 4:
		return &pluginChannelHandleH{PluginChannelHandle: base}
	case 5:
		return &pluginChannelHandleTH{PluginChannelHandle: base}
	case 6:
		return &pluginChannelHandleRH{PluginChannelHandle: base}
	case 7:
		return &pluginChannelHandleTRH{PluginChannelHandle: base}
	case 8:
		return &pluginChannelHandleA{PluginChannelHandle: base}
	case 9:
		return &pluginChannelHandleTA{PluginChannelHandle: base}
	case 10:
		return &pluginChannelHandleRA{PluginChannelHandle: base}
	case 11:
		return &pluginChannelHandleTRA{PluginChannelHandle: base}
	case 12:
		return &pluginChannelHandleHA{PluginChannelHandle: base}
	case 13:
		return &pluginChannelHandleTHA{PluginChannelHandle: base}
	case 14:
		return &pluginChannelHandleRHA{PluginChannelHandle: base}
	case 15:
		return &pluginChannelHandleTRHA{PluginChannelHandle: base}
	case 16:
		return &pluginChannelHandleE{PluginChannelHandle: base}
	case 17:
		return &pluginChannelHandleTE{PluginChannelHandle: base}
	case 18:
		return &pluginChannelHandleRE{PluginChannelHandle: base}
	case 19:
		return &pluginChannelHandleTRE{PluginChannelHandle: base}
	case 20:
		return &pluginChannelHandleHE{PluginChannelHandle: base}
	case 21:
		return &pluginChannelHandleTHE{PluginChannelHandle: base}
	case 22:
		return &pluginChannelHandleRHE{PluginChannelHandle: base}
	case 23:
		return &pluginChannelHandleTRHE{PluginChannelHandle: base}
	case 24:
		return &pluginChannelHandleAE{PluginChannelHandle: base}
	case 25:
		return &pluginChannelHandleTAE{PluginChannelHandle: base}
	case 26:
		return &pluginChannelHandleRAE{PluginChannelHandle: base}
	case 27:
		return &pluginChannelHandleTRAE{PluginChannelHandle: base}
	case 28:
		return &pluginChannelHandleHAE{PluginChannelHandle: base}
	case 29:
		return &pluginChannelHandleTHAE{PluginChannelHandle: base}
	case 30:
		return &pluginChannelHandleRHAE{PluginChannelHandle: base}
	case 31:
		return &pluginChannelHandleTRHAE{PluginChannelHandle: base}
	default:
		return base
	}
}

type pluginChannelHandleT struct{ *PluginChannelHandle }

func (h *pluginChannelHandleT) SendTyping(ctx context.Context, durationMS int) error {
	return h.PluginChannelHandle.sendTyping(ctx, durationMS)
}

type pluginChannelHandleR struct{ *PluginChannelHandle }

func (h *pluginChannelHandleR) AddReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.addReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleR) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.removeReaction(ctx, eventID, emoji)
}

type pluginChannelHandleTR struct{ *PluginChannelHandle }

func (h *pluginChannelHandleTR) SendTyping(ctx context.Context, durationMS int) error {
	return h.PluginChannelHandle.sendTyping(ctx, durationMS)
}
func (h *pluginChannelHandleTR) AddReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.addReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleTR) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.removeReaction(ctx, eventID, emoji)
}

type pluginChannelHandleH struct{ *PluginChannelHandle }

func (h *pluginChannelHandleH) SendInThread(ctx context.Context, threadID, text string) error {
	return h.PluginChannelHandle.sendInThread(ctx, threadID, text)
}

type pluginChannelHandleTH struct{ *PluginChannelHandle }

func (h *pluginChannelHandleTH) SendTyping(ctx context.Context, durationMS int) error {
	return h.PluginChannelHandle.sendTyping(ctx, durationMS)
}
func (h *pluginChannelHandleTH) SendInThread(ctx context.Context, threadID, text string) error {
	return h.PluginChannelHandle.sendInThread(ctx, threadID, text)
}

type pluginChannelHandleRH struct{ *PluginChannelHandle }

func (h *pluginChannelHandleRH) AddReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.addReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleRH) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.removeReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleRH) SendInThread(ctx context.Context, threadID, text string) error {
	return h.PluginChannelHandle.sendInThread(ctx, threadID, text)
}

type pluginChannelHandleTRH struct{ *PluginChannelHandle }

func (h *pluginChannelHandleTRH) SendTyping(ctx context.Context, durationMS int) error {
	return h.PluginChannelHandle.sendTyping(ctx, durationMS)
}
func (h *pluginChannelHandleTRH) AddReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.addReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleTRH) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.removeReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleTRH) SendInThread(ctx context.Context, threadID, text string) error {
	return h.PluginChannelHandle.sendInThread(ctx, threadID, text)
}

type pluginChannelHandleA struct{ *PluginChannelHandle }

func (h *pluginChannelHandleA) SendAudio(ctx context.Context, audio []byte, format string) error {
	return h.PluginChannelHandle.sendAudio(ctx, audio, format)
}

type pluginChannelHandleTA struct{ *PluginChannelHandle }

func (h *pluginChannelHandleTA) SendTyping(ctx context.Context, durationMS int) error {
	return h.PluginChannelHandle.sendTyping(ctx, durationMS)
}
func (h *pluginChannelHandleTA) SendAudio(ctx context.Context, audio []byte, format string) error {
	return h.PluginChannelHandle.sendAudio(ctx, audio, format)
}

type pluginChannelHandleRA struct{ *PluginChannelHandle }

func (h *pluginChannelHandleRA) AddReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.addReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleRA) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.removeReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleRA) SendAudio(ctx context.Context, audio []byte, format string) error {
	return h.PluginChannelHandle.sendAudio(ctx, audio, format)
}

type pluginChannelHandleTRA struct{ *PluginChannelHandle }

func (h *pluginChannelHandleTRA) SendTyping(ctx context.Context, durationMS int) error {
	return h.PluginChannelHandle.sendTyping(ctx, durationMS)
}
func (h *pluginChannelHandleTRA) AddReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.addReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleTRA) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.removeReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleTRA) SendAudio(ctx context.Context, audio []byte, format string) error {
	return h.PluginChannelHandle.sendAudio(ctx, audio, format)
}

type pluginChannelHandleHA struct{ *PluginChannelHandle }

func (h *pluginChannelHandleHA) SendInThread(ctx context.Context, threadID, text string) error {
	return h.PluginChannelHandle.sendInThread(ctx, threadID, text)
}
func (h *pluginChannelHandleHA) SendAudio(ctx context.Context, audio []byte, format string) error {
	return h.PluginChannelHandle.sendAudio(ctx, audio, format)
}

type pluginChannelHandleTHA struct{ *PluginChannelHandle }

func (h *pluginChannelHandleTHA) SendTyping(ctx context.Context, durationMS int) error {
	return h.PluginChannelHandle.sendTyping(ctx, durationMS)
}
func (h *pluginChannelHandleTHA) SendInThread(ctx context.Context, threadID, text string) error {
	return h.PluginChannelHandle.sendInThread(ctx, threadID, text)
}
func (h *pluginChannelHandleTHA) SendAudio(ctx context.Context, audio []byte, format string) error {
	return h.PluginChannelHandle.sendAudio(ctx, audio, format)
}

type pluginChannelHandleRHA struct{ *PluginChannelHandle }

func (h *pluginChannelHandleRHA) AddReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.addReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleRHA) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.removeReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleRHA) SendInThread(ctx context.Context, threadID, text string) error {
	return h.PluginChannelHandle.sendInThread(ctx, threadID, text)
}
func (h *pluginChannelHandleRHA) SendAudio(ctx context.Context, audio []byte, format string) error {
	return h.PluginChannelHandle.sendAudio(ctx, audio, format)
}

type pluginChannelHandleTRHA struct{ *PluginChannelHandle }

func (h *pluginChannelHandleTRHA) SendTyping(ctx context.Context, durationMS int) error {
	return h.PluginChannelHandle.sendTyping(ctx, durationMS)
}
func (h *pluginChannelHandleTRHA) AddReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.addReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleTRHA) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.removeReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleTRHA) SendInThread(ctx context.Context, threadID, text string) error {
	return h.PluginChannelHandle.sendInThread(ctx, threadID, text)
}
func (h *pluginChannelHandleTRHA) SendAudio(ctx context.Context, audio []byte, format string) error {
	return h.PluginChannelHandle.sendAudio(ctx, audio, format)
}

type pluginChannelHandleE struct{ *PluginChannelHandle }

func (h *pluginChannelHandleE) EditMessage(ctx context.Context, eventID, newText string) error {
	return h.PluginChannelHandle.editMessage(ctx, eventID, newText)
}

type pluginChannelHandleTE struct{ *PluginChannelHandle }

func (h *pluginChannelHandleTE) SendTyping(ctx context.Context, durationMS int) error {
	return h.PluginChannelHandle.sendTyping(ctx, durationMS)
}
func (h *pluginChannelHandleTE) EditMessage(ctx context.Context, eventID, newText string) error {
	return h.PluginChannelHandle.editMessage(ctx, eventID, newText)
}

type pluginChannelHandleRE struct{ *PluginChannelHandle }

func (h *pluginChannelHandleRE) AddReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.addReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleRE) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.removeReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleRE) EditMessage(ctx context.Context, eventID, newText string) error {
	return h.PluginChannelHandle.editMessage(ctx, eventID, newText)
}

type pluginChannelHandleTRE struct{ *PluginChannelHandle }

func (h *pluginChannelHandleTRE) SendTyping(ctx context.Context, durationMS int) error {
	return h.PluginChannelHandle.sendTyping(ctx, durationMS)
}
func (h *pluginChannelHandleTRE) AddReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.addReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleTRE) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.removeReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleTRE) EditMessage(ctx context.Context, eventID, newText string) error {
	return h.PluginChannelHandle.editMessage(ctx, eventID, newText)
}

type pluginChannelHandleHE struct{ *PluginChannelHandle }

func (h *pluginChannelHandleHE) SendInThread(ctx context.Context, threadID, text string) error {
	return h.PluginChannelHandle.sendInThread(ctx, threadID, text)
}
func (h *pluginChannelHandleHE) EditMessage(ctx context.Context, eventID, newText string) error {
	return h.PluginChannelHandle.editMessage(ctx, eventID, newText)
}

type pluginChannelHandleTHE struct{ *PluginChannelHandle }

func (h *pluginChannelHandleTHE) SendTyping(ctx context.Context, durationMS int) error {
	return h.PluginChannelHandle.sendTyping(ctx, durationMS)
}
func (h *pluginChannelHandleTHE) SendInThread(ctx context.Context, threadID, text string) error {
	return h.PluginChannelHandle.sendInThread(ctx, threadID, text)
}
func (h *pluginChannelHandleTHE) EditMessage(ctx context.Context, eventID, newText string) error {
	return h.PluginChannelHandle.editMessage(ctx, eventID, newText)
}

type pluginChannelHandleRHE struct{ *PluginChannelHandle }

func (h *pluginChannelHandleRHE) AddReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.addReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleRHE) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.removeReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleRHE) SendInThread(ctx context.Context, threadID, text string) error {
	return h.PluginChannelHandle.sendInThread(ctx, threadID, text)
}
func (h *pluginChannelHandleRHE) EditMessage(ctx context.Context, eventID, newText string) error {
	return h.PluginChannelHandle.editMessage(ctx, eventID, newText)
}

type pluginChannelHandleTRHE struct{ *PluginChannelHandle }

func (h *pluginChannelHandleTRHE) SendTyping(ctx context.Context, durationMS int) error {
	return h.PluginChannelHandle.sendTyping(ctx, durationMS)
}
func (h *pluginChannelHandleTRHE) AddReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.addReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleTRHE) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.removeReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleTRHE) SendInThread(ctx context.Context, threadID, text string) error {
	return h.PluginChannelHandle.sendInThread(ctx, threadID, text)
}
func (h *pluginChannelHandleTRHE) EditMessage(ctx context.Context, eventID, newText string) error {
	return h.PluginChannelHandle.editMessage(ctx, eventID, newText)
}

type pluginChannelHandleAE struct{ *PluginChannelHandle }

func (h *pluginChannelHandleAE) SendAudio(ctx context.Context, audio []byte, format string) error {
	return h.PluginChannelHandle.sendAudio(ctx, audio, format)
}
func (h *pluginChannelHandleAE) EditMessage(ctx context.Context, eventID, newText string) error {
	return h.PluginChannelHandle.editMessage(ctx, eventID, newText)
}

type pluginChannelHandleTAE struct{ *PluginChannelHandle }

func (h *pluginChannelHandleTAE) SendTyping(ctx context.Context, durationMS int) error {
	return h.PluginChannelHandle.sendTyping(ctx, durationMS)
}
func (h *pluginChannelHandleTAE) SendAudio(ctx context.Context, audio []byte, format string) error {
	return h.PluginChannelHandle.sendAudio(ctx, audio, format)
}
func (h *pluginChannelHandleTAE) EditMessage(ctx context.Context, eventID, newText string) error {
	return h.PluginChannelHandle.editMessage(ctx, eventID, newText)
}

type pluginChannelHandleRAE struct{ *PluginChannelHandle }

func (h *pluginChannelHandleRAE) AddReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.addReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleRAE) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.removeReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleRAE) SendAudio(ctx context.Context, audio []byte, format string) error {
	return h.PluginChannelHandle.sendAudio(ctx, audio, format)
}
func (h *pluginChannelHandleRAE) EditMessage(ctx context.Context, eventID, newText string) error {
	return h.PluginChannelHandle.editMessage(ctx, eventID, newText)
}

type pluginChannelHandleTRAE struct{ *PluginChannelHandle }

func (h *pluginChannelHandleTRAE) SendTyping(ctx context.Context, durationMS int) error {
	return h.PluginChannelHandle.sendTyping(ctx, durationMS)
}
func (h *pluginChannelHandleTRAE) AddReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.addReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleTRAE) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.removeReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleTRAE) SendAudio(ctx context.Context, audio []byte, format string) error {
	return h.PluginChannelHandle.sendAudio(ctx, audio, format)
}
func (h *pluginChannelHandleTRAE) EditMessage(ctx context.Context, eventID, newText string) error {
	return h.PluginChannelHandle.editMessage(ctx, eventID, newText)
}

type pluginChannelHandleHAE struct{ *PluginChannelHandle }

func (h *pluginChannelHandleHAE) SendInThread(ctx context.Context, threadID, text string) error {
	return h.PluginChannelHandle.sendInThread(ctx, threadID, text)
}
func (h *pluginChannelHandleHAE) SendAudio(ctx context.Context, audio []byte, format string) error {
	return h.PluginChannelHandle.sendAudio(ctx, audio, format)
}
func (h *pluginChannelHandleHAE) EditMessage(ctx context.Context, eventID, newText string) error {
	return h.PluginChannelHandle.editMessage(ctx, eventID, newText)
}

type pluginChannelHandleTHAE struct{ *PluginChannelHandle }

func (h *pluginChannelHandleTHAE) SendTyping(ctx context.Context, durationMS int) error {
	return h.PluginChannelHandle.sendTyping(ctx, durationMS)
}
func (h *pluginChannelHandleTHAE) SendInThread(ctx context.Context, threadID, text string) error {
	return h.PluginChannelHandle.sendInThread(ctx, threadID, text)
}
func (h *pluginChannelHandleTHAE) SendAudio(ctx context.Context, audio []byte, format string) error {
	return h.PluginChannelHandle.sendAudio(ctx, audio, format)
}
func (h *pluginChannelHandleTHAE) EditMessage(ctx context.Context, eventID, newText string) error {
	return h.PluginChannelHandle.editMessage(ctx, eventID, newText)
}

type pluginChannelHandleRHAE struct{ *PluginChannelHandle }

func (h *pluginChannelHandleRHAE) AddReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.addReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleRHAE) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.removeReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleRHAE) SendInThread(ctx context.Context, threadID, text string) error {
	return h.PluginChannelHandle.sendInThread(ctx, threadID, text)
}
func (h *pluginChannelHandleRHAE) SendAudio(ctx context.Context, audio []byte, format string) error {
	return h.PluginChannelHandle.sendAudio(ctx, audio, format)
}
func (h *pluginChannelHandleRHAE) EditMessage(ctx context.Context, eventID, newText string) error {
	return h.PluginChannelHandle.editMessage(ctx, eventID, newText)
}

type pluginChannelHandleTRHAE struct{ *PluginChannelHandle }

func (h *pluginChannelHandleTRHAE) SendTyping(ctx context.Context, durationMS int) error {
	return h.PluginChannelHandle.sendTyping(ctx, durationMS)
}
func (h *pluginChannelHandleTRHAE) AddReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.addReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleTRHAE) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	return h.PluginChannelHandle.removeReaction(ctx, eventID, emoji)
}
func (h *pluginChannelHandleTRHAE) SendInThread(ctx context.Context, threadID, text string) error {
	return h.PluginChannelHandle.sendInThread(ctx, threadID, text)
}
func (h *pluginChannelHandleTRHAE) SendAudio(ctx context.Context, audio []byte, format string) error {
	return h.PluginChannelHandle.sendAudio(ctx, audio, format)
}
func (h *pluginChannelHandleTRHAE) EditMessage(ctx context.Context, eventID, newText string) error {
	return h.PluginChannelHandle.editMessage(ctx, eventID, newText)
}
