package channels_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	pluginchannels "metiq/internal/plugins/channels"
	"metiq/internal/plugins/registry"
	"metiq/internal/plugins/runtime"
	"metiq/internal/plugins/sdk"
)

func newTestOpenClawHost(t *testing.T) *runtime.OpenClawPluginHost {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node.js not available")
	}
	h, err := runtime.NewOpenClawPluginHost(context.Background())
	if err != nil {
		t.Fatalf("NewOpenClawPluginHost: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

func writeOpenClawChannelPlugin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := `
'use strict';
let records = [];
module.exports = {
  id: 'openclaw-channel-plugin',
  name: 'OpenClaw Channel Plugin',
  register(api) {
    api.registerChannel({ plugin: {
      id: 'openclaw-test-channel',
      type: 'OpenClaw Test Channel',
      configSchema: { type: 'object', properties: { token: { type: 'string' } } },
      capabilities: { typing: true, reactions: true, threads: true, audio: true, edit: true, multiAccount: true },
      connect(channelId, config, onMessage) {
        records.push({ op: 'connect', channelId, token: config.token });
        onMessage({ channel_id: channelId, sender_id: 'user-1', text: 'hello from node', event_id: 'evt-1', thread_id: 'thread-1', reply_to_event_id: 'evt-0' });
        return {
          send(text, opts) { records.push({ op: 'send', text, replyTarget: opts && opts.reply_target }); },
          sendTyping(duration) { records.push({ op: 'typing', duration }); },
          addReaction(eventId, emoji) { records.push({ op: 'addReaction', eventId, emoji }); },
          removeReaction(eventId, emoji) { records.push({ op: 'removeReaction', eventId, emoji }); },
          sendInThread(threadId, text) { records.push({ op: 'thread', threadId, text }); },
          sendAudio(audio, format) { records.push({ op: 'audio', audioText: Buffer.isBuffer(audio) ? audio.toString('utf8') : audio, format }); },
          editMessage(eventId, text) { records.push({ op: 'edit', eventId, text }); },
          handleWebhook(req) {
            records.push({ op: 'webhook', path: req.path, body: req.body, bodyBufferText: req.bodyBuffer ? req.bodyBuffer.toString('utf8') : '' });
            onMessage({ channel_id: channelId, sender_id: 'webhook', text: 'from webhook', event_id: 'webhook-1' });
            return { status_code: 202, body: 'accepted' };
          },
          close() { records.push({ op: 'close' }); }
        };
      }
    }});
    api.registerChannel({ plugin: {
      id: 'openclaw-basic-channel',
      type: 'OpenClaw Basic Channel',
      configSchema: { type: 'object' },
      connect(channelId, config, onMessage) {
        records.push({ op: 'connect-basic', channelId });
        return { send(text) { records.push({ op: 'send-basic', text }); }, close() { records.push({ op: 'close-basic' }); } };
      }
    }});
    api.registerTool({ name: 'records', execute: async () => ({ records }) });
  }
};
`
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPluginChannelBridgeConnectSendOptionalFeaturesAndWebhook(t *testing.T) {
	host := newTestOpenClawHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := host.LoadPluginResult(ctx, writeOpenClawChannelPlugin(t), nil)
	if err != nil {
		t.Fatalf("LoadPluginResult: %v", err)
	}
	bridges, err := pluginchannels.BridgesFromLoadResult(host, result)
	if err != nil {
		t.Fatalf("BridgesFromLoadResult: %v", err)
	}
	if len(bridges) != 2 {
		t.Fatalf("bridges len=%d, want 2", len(bridges))
	}
	bridge := findBridge(t, bridges, "openclaw-test-channel")
	if bridge.ID() != "openclaw-test-channel" || bridge.Type() != "OpenClaw Test Channel" {
		t.Fatalf("unexpected bridge metadata id=%q type=%q", bridge.ID(), bridge.Type())
	}
	if schema := bridge.ConfigSchema(); schema["type"] != "object" {
		t.Fatalf("unexpected config schema: %#v", schema)
	}
	caps := bridge.(sdk.ChannelPluginWithCapabilities).Capabilities()
	if !caps.Typing || !caps.Reactions || !caps.Threads || !caps.Audio || !caps.Edit || !caps.MultiAccount {
		t.Fatalf("capabilities not parsed: %+v", caps)
	}

	inbound := make(chan sdk.InboundChannelMessage, 2)
	handle, err := bridge.Connect(ctx, "configured-channel", map[string]any{"token": "secret"}, func(msg sdk.InboundChannelMessage) {
		inbound <- msg
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer handle.Close()

	first := waitInbound(t, inbound)
	if first.ChannelID != "configured-channel" || first.SenderID != "user-1" || first.Text != "hello from node" || first.ThreadID != "thread-1" || first.ReplyToEventID != "evt-0" {
		t.Fatalf("unexpected inbound message: %+v", first)
	}

	if err := handle.Send(sdk.WithChannelReplyTarget(ctx, "user-1"), "outbound"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := handle.(sdk.TypingHandle).SendTyping(ctx, 1234); err != nil {
		t.Fatalf("SendTyping: %v", err)
	}
	if err := handle.(sdk.ReactionHandle).AddReaction(ctx, "evt-1", "👍"); err != nil {
		t.Fatalf("AddReaction: %v", err)
	}
	if err := handle.(sdk.ReactionHandle).RemoveReaction(ctx, "evt-1", "👍"); err != nil {
		t.Fatalf("RemoveReaction: %v", err)
	}
	if err := handle.(sdk.ThreadHandle).SendInThread(ctx, "thread-1", "thread reply"); err != nil {
		t.Fatalf("SendInThread: %v", err)
	}
	if err := handle.(sdk.AudioHandle).SendAudio(ctx, []byte("abc"), "wav"); err != nil {
		t.Fatalf("SendAudio: %v", err)
	}
	if err := handle.(sdk.EditHandle).EditMessage(ctx, "evt-1", "edited"); err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	webhookHandle, ok := handle.(interface {
		HandleWebhook(context.Context, pluginchannels.WebhookRequest) (pluginchannels.WebhookResult, error)
	})
	if !ok {
		t.Fatal("expected webhook-capable handle")
	}
	webhookResult, err := webhookHandle.HandleWebhook(ctx, pluginchannels.WebhookRequest{Method: "POST", Path: "/webhooks/test", Body: []byte("payload")})
	if err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}
	if webhookResult.StatusCode != 202 || string(webhookResult.Body) != "accepted" {
		t.Fatalf("unexpected webhook result: %+v", webhookResult)
	}
	second := waitInbound(t, inbound)
	if second.SenderID != "webhook" || second.Text != "from webhook" {
		t.Fatalf("unexpected webhook inbound: %+v", second)
	}
	handle.Close()

	recordsRaw, err := host.InvokeTool(ctx, "openclaw-channel-plugin", "records", nil)
	if err != nil {
		t.Fatalf("InvokeTool records: %v", err)
	}
	records := recordsRaw.(map[string]any)["records"].([]any)
	wantOps := []string{"connect", "send", "typing", "addReaction", "removeReaction", "thread", "audio", "edit", "webhook", "close"}
	for _, want := range wantOps {
		if !hasRecordOp(records, want) {
			t.Fatalf("missing record op %q in %#v", want, records)
		}
	}
	if got := recordValue(records, "audio", "audioText"); got != "abc" {
		t.Fatalf("audio payload was not decoded, got %#v records=%#v", got, records)
	}
	if got := recordValue(records, "webhook", "bodyBufferText"); got != "payload" {
		t.Fatalf("webhook body was not decoded, got %#v records=%#v", got, records)
	}

	basic := findBridge(t, bridges, "openclaw-basic-channel")
	basicHandle, err := basic.Connect(ctx, "basic-configured", nil, nil)
	if err != nil {
		t.Fatalf("basic Connect: %v", err)
	}
	defer basicHandle.Close()
	if _, ok := basicHandle.(sdk.TypingHandle); ok {
		t.Fatal("basic handle unexpectedly implements TypingHandle")
	}
	if _, ok := basicHandle.(sdk.ReactionHandle); ok {
		t.Fatal("basic handle unexpectedly implements ReactionHandle")
	}
}

func TestOpenClawAndNativeChannelsShareUnifiedRegistry(t *testing.T) {
	host := newTestOpenClawHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := host.LoadPluginResult(ctx, writeOpenClawChannelPlugin(t), nil)
	if err != nil {
		t.Fatalf("LoadPluginResult: %v", err)
	}

	r := registry.NewUnifiedRegistry()
	if err := r.RegisterOpenClawLoadResult(result); err != nil {
		t.Fatalf("RegisterOpenClawLoadResult: %v", err)
	}
	if err := r.RegisterNativeChannel(nativeTestChannelPlugin{}); err != nil {
		t.Fatalf("RegisterNativeChannel: %v", err)
	}
	openclaw, ok := r.Channels().Get("openclaw-test-channel")
	if !ok || openclaw.Source != registry.PluginSourceOpenClaw || !openclaw.Capabilities.Typing || openclaw.Plugin != nil {
		t.Fatalf("unexpected openclaw channel registry entry: %+v ok=%v", openclaw, ok)
	}
	native, ok := r.Channels().Get("native-test-channel")
	if !ok || native.Source != registry.PluginSourceNative || native.Plugin == nil || !native.Capabilities.Reactions {
		t.Fatalf("unexpected native channel registry entry: %+v ok=%v", native, ok)
	}
}

func findBridge(t *testing.T, bridges []sdk.ChannelPlugin, id string) sdk.ChannelPlugin {
	t.Helper()
	for _, bridge := range bridges {
		if bridge.ID() == id {
			return bridge
		}
	}
	t.Fatalf("bridge %q not found", id)
	return nil
}

func waitInbound(t *testing.T, ch <-chan sdk.InboundChannelMessage) sdk.InboundChannelMessage {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for inbound channel message")
		return sdk.InboundChannelMessage{}
	}
}

func hasRecordOp(records []any, op string) bool {
	return recordValue(records, op, "op") == op
}

func recordValue(records []any, op, key string) any {
	for _, record := range records {
		m, _ := record.(map[string]any)
		if m["op"] == op {
			return m[key]
		}
	}
	return nil
}

type nativeTestChannelPlugin struct{}

func (nativeTestChannelPlugin) ID() string                   { return "native-test-channel" }
func (nativeTestChannelPlugin) Type() string                 { return "Native Test Channel" }
func (nativeTestChannelPlugin) ConfigSchema() map[string]any { return map[string]any{"type": "object"} }
func (nativeTestChannelPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{Reactions: true}
}
func (nativeTestChannelPlugin) Connect(context.Context, string, map[string]any, func(sdk.InboundChannelMessage)) (sdk.ChannelHandle, error) {
	return nativeTestChannelHandle{}, nil
}

type nativeTestChannelHandle struct{}

func (nativeTestChannelHandle) ID() string                         { return "native-test-channel" }
func (nativeTestChannelHandle) Send(context.Context, string) error { return nil }
func (nativeTestChannelHandle) Close()                             {}
