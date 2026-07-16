package l402

import (
	"errors"
	"strings"
	"testing"
)

func TestParseChallengeQuotedCommasAndMultipleValues(t *testing.T) {
	challenge, err := ParseChallenge([]string{
		"Bearer realm=\"public, docs\"",
		"l402 MaCaRoOn=\"mac,with,commas\", InVoIcE=\"lnbc1invoice\"",
	})
	if err != nil {
		t.Fatal(err)
	}
	if challenge.Scheme != "L402" || challenge.Macaroon != "mac,with,commas" || challenge.Invoice != "lnbc1invoice" {
		t.Fatalf("unexpected challenge: %#v", challenge)
	}
}

func TestParseChallengeLegacyLSATAndEscapes(t *testing.T) {
	challenge, err := ParseChallenge([]string{"LSAT macaroon=\"mac\\\\\\\"aroon\", invoice=lnbc1token"})
	if err != nil {
		t.Fatal(err)
	}
	if challenge.Scheme != "LSAT" || challenge.Macaroon != "mac\\\"aroon" {
		t.Fatalf("unexpected challenge: %#v", challenge)
	}
}

func TestParseChallengeDuplicateIdenticalValuesAllowed(t *testing.T) {
	value := "L402 macaroon=\"m\", invoice=\"i\""
	challenge, err := ParseChallenge([]string{value, value})
	if err != nil {
		t.Fatal(err)
	}
	if challenge.Macaroon != "m" || challenge.Invoice != "i" {
		t.Fatalf("unexpected challenge: %#v", challenge)
	}
}

func TestParseChallengeRejectsDistinctOrMalformedChallenges(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		target error
	}{
		{"distinct", []string{"L402 macaroon=\"a\", invoice=\"i\"", "L402 macaroon=\"b\", invoice=\"i\""}, ErrAmbiguousChallenge},
		{"mixed schemes", []string{"L402 macaroon=\"a\", invoice=\"i\"", "LSAT macaroon=\"a\", invoice=\"i\""}, ErrAmbiguousChallenge},
		{"missing invoice", []string{"L402 macaroon=\"a\""}, ErrInvalidChallenge},
		{"empty macaroon", []string{"L402 macaroon=\"\", invoice=\"i\""}, ErrInvalidChallenge},
		{"duplicate parameter", []string{"L402 macaroon=\"a\", MACAROON=\"a\", invoice=\"i\""}, ErrInvalidChallenge},
		{"unterminated quote", []string{"L402 macaroon=\"a, invoice=\"i\""}, ErrInvalidChallenge},
		{"absent", []string{"Bearer realm=\"x\""}, ErrNoChallenge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseChallenge(tt.values)
			if !errors.Is(err, tt.target) {
				t.Fatalf("error = %v, want %v", err, tt.target)
			}
		})
	}
}

func TestParseChallengeBoundsHeadersAndCredentials(t *testing.T) {
	_, err := ParseChallenge([]string{strings.Repeat("x", MaxAuthenticateHeaderBytes+1)})
	if !errors.Is(err, ErrInvalidChallenge) {
		t.Fatalf("oversized header error = %v", err)
	}
	_, err = ParseChallenge([]string{"L402 macaroon=\"" + strings.Repeat("m", MaxMacaroonBytes+1) + "\", invoice=\"i\""})
	if !errors.Is(err, ErrInvalidChallenge) {
		t.Fatalf("oversized macaroon error = %v", err)
	}
}

func TestChallengeAuthorizationUsesAdvertisedScheme(t *testing.T) {
	preimage := strings.Repeat("ab", 32)
	got, err := (Challenge{Scheme: "LSAT", Macaroon: "opaque"}).Authorization(preimage)
	if err != nil {
		t.Fatal(err)
	}
	if got != "LSAT opaque:"+preimage {
		t.Fatalf("authorization = %q", got)
	}
	if _, err := (Challenge{Scheme: "L402", Macaroon: "opaque"}).Authorization("not-hex"); err == nil {
		t.Fatal("expected invalid preimage rejection")
	}
}
