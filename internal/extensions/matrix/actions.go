package matrix

import (
	"context"
	"encoding/base64"
	"fmt"

	"metiq/internal/plugins/sdk"
)

// GatewayMethods exposes Matrix action/client/send operations through the plugin interface.
func (p *MatrixPlugin) GatewayMethods() []sdk.GatewayMethod {
	return []sdk.GatewayMethod{
		{
			Method:      "matrix.send",
			Description: "Send a Matrix room message",
			Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
				bot, err := matrixBotFromParams(params, "matrix.send")
				if err != nil {
					return nil, err
				}
				text, _ := params["text"].(string)
				if text == "" {
					return nil, fmt.Errorf("matrix.send: text is required")
				}
				receipt, err := bot.SendWithReceipt(ctx, text)
				if err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "message_id": receipt.MessageID}, nil
			},
		},
		{
			Method:      "matrix.send_media",
			Description: "Upload and send Matrix media",
			Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
				bot, err := matrixBotFromParams(params, "matrix.send_media")
				if err != nil {
					return nil, err
				}
				dataRaw, _ := params["data_base64"].(string)
				if dataRaw == "" {
					return nil, fmt.Errorf("matrix.send_media: data_base64 is required")
				}
				data, err := base64.StdEncoding.DecodeString(dataRaw)
				if err != nil {
					return nil, fmt.Errorf("matrix.send_media: decode data_base64: %w", err)
				}
				text, _ := params["text"].(string)
				mimeType, _ := params["mime_type"].(string)
				filename, _ := params["filename"].(string)
				receipt, err := bot.SendMedia(ctx, text, data, mimeType, filename)
				if err != nil {
					return nil, err
				}
				return map[string]any{"ok": true, "message_id": receipt.MessageID}, nil
			},
		},
		matrixSimpleAction("matrix.typing", "Send Matrix typing indicator", func(ctx context.Context, b *matrixBot, params map[string]any) error {
			duration := 5000
			if v, ok := params["duration_ms"].(float64); ok {
				duration = int(v)
			}
			return b.SendTyping(ctx, duration)
		}),
		matrixSimpleAction("matrix.redact", "Redact a Matrix event", func(ctx context.Context, b *matrixBot, params map[string]any) error {
			eventID, _ := params["event_id"].(string)
			if eventID == "" {
				return fmt.Errorf("matrix.redact: event_id is required")
			}
			return b.DeleteMessage(ctx, eventID)
		}),
		matrixSimpleAction("matrix.react", "React to a Matrix event", func(ctx context.Context, b *matrixBot, params map[string]any) error {
			eventID, _ := params["event_id"].(string)
			emoji, _ := params["emoji"].(string)
			if eventID == "" || emoji == "" {
				return fmt.Errorf("matrix.react: event_id and emoji are required")
			}
			return b.AddReaction(ctx, eventID, emoji)
		}),
		matrixSimpleAction("matrix.thread_reply", "Send a Matrix threaded reply", func(ctx context.Context, b *matrixBot, params map[string]any) error {
			threadID, _ := params["thread_id"].(string)
			text, _ := params["text"].(string)
			if threadID == "" || text == "" {
				return fmt.Errorf("matrix.thread_reply: thread_id and text are required")
			}
			return b.SendInThread(ctx, threadID, text)
		}),
		matrixSimpleAction("matrix.edit", "Edit a Matrix event", func(ctx context.Context, b *matrixBot, params map[string]any) error {
			eventID, _ := params["event_id"].(string)
			text, _ := params["text"].(string)
			if eventID == "" || text == "" {
				return fmt.Errorf("matrix.edit: event_id and text are required")
			}
			return b.EditMessage(ctx, eventID, text)
		}),
	}
}

type matrixActionFunc func(context.Context, *matrixBot, map[string]any) error

func matrixSimpleAction(method, description string, fn matrixActionFunc) sdk.GatewayMethod {
	return sdk.GatewayMethod{Method: method, Description: description, Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
		bot, err := matrixBotFromParams(params, method)
		if err != nil {
			return nil, err
		}
		if err := fn(ctx, bot, params); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	}}
}

func matrixBotFromParams(params map[string]any, method string) (*matrixBot, error) {
	hsURL, _ := params["homeserver_url"].(string)
	accessToken, _ := params["access_token"].(string)
	roomID, _ := params["room_id"].(string)
	if roomID == "" {
		roomID, _ = params["channel_id"].(string)
	}
	if hsURL == "" || accessToken == "" || roomID == "" {
		return nil, fmt.Errorf("%s: homeserver_url, access_token, and room_id/channel_id are required", method)
	}
	return newMatrixClient("matrix-action", hsURL, accessToken, roomID), nil
}
