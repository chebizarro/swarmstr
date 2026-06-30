package msteams

import (
	"context"
	"fmt"
	"strings"

	"metiq/internal/gateway/channels"
)

type teamsAttachment struct {
	ContentType string
	ContentURL  string
	Name        string
}

func teamsAttachmentFromActivity(activity botFrameworkActivity) (teamsAttachment, bool) {
	if len(activity.Attachments) == 0 {
		return teamsAttachment{}, false
	}
	att := activity.Attachments[0]
	if strings.TrimSpace(att.ContentURL) == "" && strings.TrimSpace(att.Name) == "" {
		return teamsAttachment{}, false
	}
	return teamsAttachment{ContentType: att.ContentType, ContentURL: att.ContentURL, Name: att.Name}, true
}

func (b *teamsBot) SendFileAttachment(ctx context.Context, text string, attachment teamsAttachment) (channels.DeliveryReceipt, error) {
	if strings.TrimSpace(attachment.ContentURL) == "" {
		receipt := channels.DeliveryReceipt{ChannelID: b.channelID, Provider: "msteams", Attempts: 1}
		err := fmt.Errorf("msteams attachment: content URL is required")
		receipt.Status = channels.DeliveryFailed
		receipt.Error = err.Error()
		return receipt, err
	}
	return b.SendAttachment(ctx, text, attachment.ContentURL, attachment.ContentType, attachment.Name)
}
