package matrix

import (
	"net/http"
	"strings"
	"time"
)

func newMatrixClient(channelID, hsURL, accessToken, roomID string) *matrixBot {
	return &matrixBot{
		channelID:      channelID,
		hsURL:          strings.TrimRight(hsURL, "/"),
		accessToken:    accessToken,
		roomID:         roomID,
		allowedSenders: map[string]bool{},
		done:           make(chan struct{}),
		httpClient:     &http.Client{Timeout: 60 * time.Second},
	}
}
