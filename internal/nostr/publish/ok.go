package publish

import (
	"context"
	"fmt"
	"strings"

	nostr "fiatjaf.com/nostr"
)

// Publisher is the subset of nostr.Pool used for OK-aware publishing.
type Publisher interface {
	PublishMany(context.Context, []string, nostr.Event) chan nostr.PublishResult
}

// RelayResult records the relay OK outcome observed for one publish attempt.
type RelayResult struct {
	RelayURL string
	Accepted bool
	Message  string
}

// Report summarizes all relay OK outcomes for a publish.
type Report struct {
	Results  []RelayResult
	Accepted int
}

// AllRejectedError reports a publish where no configured relay accepted the event.
type AllRejectedError struct {
	Relays  []string
	Results []RelayResult
}

func (e *AllRejectedError) Error() string {
	if e == nil {
		return ""
	}
	if len(e.Relays) == 0 {
		return "no relays configured for publish"
	}
	if len(e.Results) == 0 {
		return fmt.Sprintf("no relay accepted publish (%d configured; no relay results)", len(e.Relays))
	}

	parts := make([]string, 0, len(e.Results))
	for _, result := range e.Results {
		relayURL := result.RelayURL
		if relayURL == "" {
			relayURL = "<unknown relay>"
		}
		msg := strings.TrimSpace(result.Message)
		if msg == "" {
			msg = "no OK message"
		}
		parts = append(parts, fmt.Sprintf("%s accepted=%t message=%q", relayURL, result.Accepted, msg))
	}
	return "no relay accepted publish: " + strings.Join(parts, "; ")
}

// PublishToAny publishes evt to relays and succeeds only when at least one relay
// returns an OK acceptance. OK=false responses are surfaced by the nostr library
// as PublishResult.Error values whose text is the relay OK message.
func PublishToAny(ctx context.Context, publisher Publisher, relays []string, evt nostr.Event) (Report, error) {
	if publisher == nil {
		return Report{}, fmt.Errorf("publish: publisher is nil")
	}
	if len(relays) == 0 {
		return Report{}, &AllRejectedError{Relays: nil}
	}

	report := Report{Results: make([]RelayResult, 0, len(relays))}
	for result := range publisher.PublishMany(ctx, relays, evt) {
		relayURL := result.RelayURL
		if relayURL == "" && result.Relay != nil {
			relayURL = result.Relay.URL
		}

		relayResult := RelayResult{
			RelayURL: relayURL,
			Accepted: result.Error == nil,
		}
		if result.Error != nil {
			relayResult.Message = result.Error.Error()
		} else {
			relayResult.Message = "OK accepted"
			report.Accepted++
		}
		report.Results = append(report.Results, relayResult)
	}

	if report.Accepted > 0 {
		return report, nil
	}
	return report, &AllRejectedError{Relays: append([]string(nil), relays...), Results: report.Results}
}
