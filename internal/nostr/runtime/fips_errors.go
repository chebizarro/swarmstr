package runtime

import (
	"context"
	"errors"
	"fmt"
)

// FIPSErrorKind classifies FIPS send failures for transport selection.
type FIPSErrorKind string

const (
	// FIPSErrorKindPermanent means the caller/configuration input is invalid;
	// selectors should not fall back to relay because the same message would be invalid there too.
	FIPSErrorKindPermanent FIPSErrorKind = "permanent"
	// FIPSErrorKindTransport means the FIPS path failed; selectors may fall back to relay.
	FIPSErrorKindTransport FIPSErrorKind = "transport"
)

// FIPSError wraps FIPS transport failures with enough classification for selectors.
type FIPSError struct {
	Kind FIPSErrorKind
	Peer string
	Op   string
	Err  error
}

func (e *FIPSError) Error() string {
	if e == nil {
		return "<nil>"
	}
	msg := "fips"
	if e.Op != "" {
		msg += " " + e.Op
	}
	if e.Peer != "" {
		msg += " " + truncatePubkey(e.Peer)
	}
	if e.Kind != "" {
		msg += fmt.Sprintf(" (%s)", e.Kind)
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *FIPSError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// AllowFallback reports whether selectors may fall back to a relay transport.
func (e *FIPSError) AllowFallback() bool {
	return e != nil && e.Kind == FIPSErrorKindTransport
}

func newFIPSPermanentError(peer, op string, err error) error {
	return &FIPSError{Kind: FIPSErrorKindPermanent, Peer: peer, Op: op, Err: err}
}

func newFIPSTransportError(peer, op string, err error) error {
	return &FIPSError{Kind: FIPSErrorKindTransport, Peer: peer, Op: op, Err: err}
}

func classifyFIPSError(ctx context.Context, peer, op string, kind FIPSErrorKind, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}
	var fipsErr *FIPSError
	if errors.As(err, &fipsErr) {
		return err
	}
	if kind == FIPSErrorKindTransport {
		return newFIPSTransportError(peer, op, err)
	}
	return newFIPSPermanentError(peer, op, err)
}
