package l402

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

const (
	MaxAuthenticateHeaderBytes = 16 * 1024
	MaxMacaroonBytes           = 8 * 1024
	MaxInvoiceBytes            = 8 * 1024
)

var (
	ErrNoChallenge        = errors.New("L402 challenge not found")
	ErrInvalidChallenge   = errors.New("invalid L402 challenge")
	ErrAmbiguousChallenge = errors.New("multiple distinct L402 challenges")
)

// Challenge is a parsed L402 or legacy LSAT authentication challenge.
type Challenge struct {
	Scheme   string
	Macaroon string
	Invoice  string
}

// ParseChallenge parses all WWW-Authenticate field values as one challenge set.
func ParseChallenge(values []string) (Challenge, error) {
	total := 0
	var found []Challenge
	for _, value := range values {
		total += len(value)
		if total > MaxAuthenticateHeaderBytes {
			return Challenge{}, fmt.Errorf("%w: authentication headers are too large", ErrInvalidChallenge)
		}
		segments, err := splitHeaderValue(value)
		if err != nil {
			return Challenge{}, fmt.Errorf("%w: %v", ErrInvalidChallenge, err)
		}
		var current *rawChallenge
		for _, segment := range segments {
			scheme, rest, startsChallenge := challengePrefix(segment)
			if startsChallenge {
				item := rawChallenge{scheme: scheme, params: map[string]string{}}
				current = &item
				if isL402Scheme(scheme) {
					found = append(found, Challenge{Scheme: canonicalScheme(scheme)})
					current.out = &found[len(found)-1]
				}
				if strings.TrimSpace(rest) != "" && current.out != nil {
					if err := current.addParam(rest); err != nil {
						return Challenge{}, fmt.Errorf("%w: %v", ErrInvalidChallenge, err)
					}
				}
				continue
			}
			if current != nil && current.out != nil {
				if err := current.addParam(segment); err != nil {
					return Challenge{}, fmt.Errorf("%w: %v", ErrInvalidChallenge, err)
				}
			}
		}
	}

	if len(found) == 0 {
		return Challenge{}, ErrNoChallenge
	}
	unique := make(map[string]Challenge, len(found))
	for _, challenge := range found {
		if strings.TrimSpace(challenge.Macaroon) == "" || strings.TrimSpace(challenge.Invoice) == "" {
			return Challenge{}, fmt.Errorf("%w: invoice and macaroon are required", ErrInvalidChallenge)
		}
		if len(challenge.Macaroon) > MaxMacaroonBytes || len(challenge.Invoice) > MaxInvoiceBytes {
			return Challenge{}, fmt.Errorf("%w: invoice or macaroon is too large", ErrInvalidChallenge)
		}
		unique[challenge.identity()] = challenge
	}
	if len(unique) != 1 {
		return Challenge{}, ErrAmbiguousChallenge
	}
	for _, challenge := range unique {
		return challenge, nil
	}
	panic("unreachable")
}

// Authorization formats the credential using the advertised challenge scheme.
func (c Challenge) Authorization(preimageHex string) (string, error) {
	decoded, err := hex.DecodeString(preimageHex)
	if err != nil || len(decoded) != sha256.Size || preimageHex != strings.ToLower(preimageHex) {
		return "", fmt.Errorf("preimage must be 32-byte lowercase hex")
	}
	if !isL402Scheme(c.Scheme) || strings.TrimSpace(c.Macaroon) == "" {
		return "", fmt.Errorf("%w: unusable challenge", ErrInvalidChallenge)
	}
	return canonicalScheme(c.Scheme) + " " + c.Macaroon + ":" + preimageHex, nil
}

// Fingerprint identifies an exact advertised challenge without exposing it.
func (c Challenge) Fingerprint() string {
	sum := sha256.Sum256([]byte(c.identity()))
	return hex.EncodeToString(sum[:])
}

// MacaroonFingerprint identifies the opaque macaroon without exposing it.
func (c Challenge) MacaroonFingerprint() string {
	sum := sha256.Sum256([]byte(c.Macaroon))
	return hex.EncodeToString(sum[:])
}

func (c Challenge) identity() string {
	return strings.ToUpper(c.Scheme) + "\x00" + c.Macaroon + "\x00" + c.Invoice
}

type rawChallenge struct {
	scheme string
	params map[string]string
	out    *Challenge
}

func (c *rawChallenge) addParam(raw string) error {
	name, value, err := parseAuthParam(raw)
	if err != nil {
		return err
	}
	key := strings.ToLower(name)
	if _, duplicate := c.params[key]; duplicate {
		return fmt.Errorf("duplicate %s parameter", key)
	}
	c.params[key] = value
	switch key {
	case "macaroon":
		c.out.Macaroon = value
	case "invoice":
		c.out.Invoice = value
	}
	return nil
}

func splitHeaderValue(value string) ([]string, error) {
	var segments []string
	start := 0
	quoted := false
	escaped := false
	for i, r := range value {
		if r == '\r' || r == '\n' || (unicode.IsControl(r) && r != '\t') {
			return nil, fmt.Errorf("control character in header")
		}
		switch {
		case escaped:
			escaped = false
		case quoted && r == '\\':
			escaped = true
		case r == '"':
			quoted = !quoted
		case r == ',' && !quoted:
			if part := strings.TrimSpace(value[start:i]); part != "" {
				segments = append(segments, part)
			}
			start = i + 1
		}
	}
	if quoted || escaped {
		return nil, fmt.Errorf("unterminated quoted value")
	}
	if part := strings.TrimSpace(value[start:]); part != "" {
		segments = append(segments, part)
	}
	return segments, nil
}

func challengePrefix(segment string) (scheme, rest string, ok bool) {
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return "", "", false
	}
	i := 0
	for i < len(segment) && isTokenByte(segment[i]) {
		i++
	}
	if i == 0 {
		return "", "", false
	}
	if i == len(segment) {
		return segment, "", true
	}
	if segment[i] == '=' {
		return "", "", false
	}
	if segment[i] != ' ' && segment[i] != '\t' {
		return "", "", false
	}
	return segment[:i], strings.TrimSpace(segment[i:]), true
}

func parseAuthParam(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	eq := strings.IndexByte(raw, '=')
	if eq <= 0 {
		return "", "", fmt.Errorf("malformed authentication parameter")
	}
	name := strings.TrimSpace(raw[:eq])
	if name == "" {
		return "", "", fmt.Errorf("empty authentication parameter name")
	}
	for i := range name {
		if !isTokenByte(name[i]) {
			return "", "", fmt.Errorf("invalid authentication parameter name")
		}
	}
	valuePart := strings.TrimSpace(raw[eq+1:])
	if valuePart == "" {
		return name, "", nil
	}
	if valuePart[0] != '"' {
		i := 0
		for i < len(valuePart) && isTokenByte(valuePart[i]) {
			i++
		}
		if i == 0 || strings.TrimSpace(valuePart[i:]) != "" {
			return "", "", fmt.Errorf("invalid token parameter value")
		}
		return name, valuePart[:i], nil
	}
	var value strings.Builder
	escaped := false
	for i := 1; i < len(valuePart); i++ {
		ch := valuePart[i]
		switch {
		case escaped:
			value.WriteByte(ch)
			escaped = false
		case ch == '\\':
			escaped = true
		case ch == '"':
			if strings.TrimSpace(valuePart[i+1:]) != "" {
				return "", "", fmt.Errorf("trailing data after quoted parameter")
			}
			return name, value.String(), nil
		case ch < 0x20 || ch == 0x7f:
			return "", "", fmt.Errorf("control character in quoted parameter")
		default:
			value.WriteByte(ch)
		}
	}
	return "", "", fmt.Errorf("unterminated quoted parameter")
}

func isL402Scheme(value string) bool {
	return strings.EqualFold(value, "L402") || strings.EqualFold(value, "LSAT")
}

func canonicalScheme(value string) string {
	if strings.EqualFold(value, "LSAT") {
		return "LSAT"
	}
	return "L402"
}

func isTokenByte(ch byte) bool {
	if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' {
		return true
	}
	return ch == 0x60 || strings.ContainsRune("!#$%&'*+-.^_|~", rune(ch))
}
