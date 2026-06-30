package nip98

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"
)

const KindHTTPAuth nostr.Kind = 27235

var Window = 60 * time.Second

type BuildOptions struct {
	Method    string
	URL       string
	Body      []byte
	CreatedAt time.Time
}

func Build(ctx context.Context, keyer nostr.Signer, opts BuildOptions) (nostr.Event, string, error) {
	if keyer == nil {
		return nostr.Event{}, "", errors.New("keyer is required")
	}
	method := strings.ToUpper(strings.TrimSpace(opts.Method))
	u := strings.TrimSpace(opts.URL)
	if method == "" || u == "" {
		return nostr.Event{}, "", errors.New("method and url are required")
	}
	if _, err := url.ParseRequestURI(u); err != nil {
		return nostr.Event{}, "", fmt.Errorf("invalid url: %w", err)
	}
	created := opts.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	tags := nostr.Tags{{"u", u}, {"method", method}}
	if opts.Body != nil {
		sum := sha256.Sum256(opts.Body)
		tags = append(tags, nostr.Tag{"payload", hex.EncodeToString(sum[:])})
	}
	evt := nostr.Event{Kind: KindHTTPAuth, CreatedAt: nostr.Timestamp(created.Unix()), Tags: tags, Content: ""}
	if err := keyer.SignEvent(ctx, &evt); err != nil {
		return nostr.Event{}, "", err
	}
	raw, err := json.Marshal(evt)
	if err != nil {
		return nostr.Event{}, "", err
	}
	return evt, "Nostr " + base64.StdEncoding.EncodeToString(raw), nil
}

func Verify(authHeader, method, requestURL string, body []byte) (string, error) {
	return VerifyAt(authHeader, method, requestURL, body, time.Now(), Window, false)
}

func VerifyPayloadRequired(authHeader, method, requestURL string, body []byte) (string, error) {
	return VerifyAt(authHeader, method, requestURL, body, time.Now(), Window, true)
}

func VerifyAt(authHeader, method, requestURL string, body []byte, now time.Time, window time.Duration, requirePayload bool) (string, error) {
	evt, err := DecodeAuthorization(authHeader)
	if err != nil {
		return "", err
	}
	if evt.Kind != KindHTTPAuth {
		return "", fmt.Errorf("invalid auth event kind %d", evt.Kind)
	}
	if window <= 0 {
		window = Window
	}
	created := time.Unix(int64(evt.CreatedAt), 0)
	if created.Before(now.Add(-window)) || created.After(now.Add(window)) {
		return "", errors.New("auth event expired")
	}
	if tagValue(evt.Tags, "u") != strings.TrimSpace(requestURL) {
		return "", errors.New("auth event url mismatch")
	}
	if !strings.EqualFold(tagValue(evt.Tags, "method"), strings.TrimSpace(method)) {
		return "", errors.New("auth event method mismatch")
	}
	payload := tagValue(evt.Tags, "payload")
	if requirePayload && payload == "" {
		return "", errors.New("auth event payload tag required")
	}
	if payload != "" {
		sum := sha256.Sum256(body)
		if !strings.EqualFold(payload, hex.EncodeToString(sum[:])) {
			return "", errors.New("auth event payload mismatch")
		}
	}
	if !evt.CheckID() {
		return "", errors.New("auth event id mismatch")
	}
	if !evt.VerifySignature() {
		return "", errors.New("auth event signature invalid")
	}
	return evt.PubKey.Hex(), nil
}

func DecodeAuthorization(authHeader string) (nostr.Event, error) {
	parts := strings.Fields(strings.TrimSpace(authHeader))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Nostr") {
		return nostr.Event{}, errors.New("missing Nostr authorization")
	}
	raw, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return nostr.Event{}, fmt.Errorf("invalid authorization encoding: %w", err)
	}
	var evt nostr.Event
	if err := json.Unmarshal(raw, &evt); err != nil {
		return nostr.Event{}, fmt.Errorf("invalid authorization event: %w", err)
	}
	return evt, nil
}

func tagValue(tags nostr.Tags, key string) string {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == key {
			return tag[1]
		}
	}
	return ""
}
