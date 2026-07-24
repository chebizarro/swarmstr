package nip77

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	nostr "fiatjaf.com/nostr"
)

const ProtocolVersion byte = 0x61

var ErrUnsupportedVersion = errors.New("unsupported negentropy protocol version")

type FrameKind string

const (
	FrameOpen  FrameKind = "NEG-OPEN"
	FrameMsg   FrameKind = "NEG-MSG"
	FrameError FrameKind = "NEG-ERR"
	FrameClose FrameKind = "NEG-CLOSE"
)

type Frame struct {
	Kind       FrameKind
	ID         string
	Filter     *nostr.Filter
	Message    []byte
	Reason     string
	MaxRecords *uint64
}

type ProtocolError struct{ Err error }

func (e *ProtocolError) Error() string { return "NIP-77 protocol error: " + e.Err.Error() }
func (e *ProtocolError) Unwrap() error { return e.Err }

type RemoteError struct {
	ID         string
	Reason     string
	MaxRecords *uint64
}

func (e *RemoteError) Error() string {
	if e.MaxRecords != nil {
		return fmt.Sprintf("NIP-77 relay error for %s: %s (max records %d)", e.ID, e.Reason, *e.MaxRecords)
	}
	return fmt.Sprintf("NIP-77 relay error for %s: %s", e.ID, e.Reason)
}

func OpenFrame(id string, filter nostr.Filter, message []byte) (Frame, error) {
	f := filter
	return validatedFrame(Frame{Kind: FrameOpen, ID: id, Filter: &f, Message: append([]byte(nil), message...)})
}

func MessageFrame(id string, message []byte) (Frame, error) {
	return validatedFrame(Frame{Kind: FrameMsg, ID: id, Message: append([]byte(nil), message...)})
}

func ErrorFrame(id, reason string, maxRecords *uint64) (Frame, error) {
	return validatedFrame(Frame{Kind: FrameError, ID: id, Reason: reason, MaxRecords: maxRecords})
}

func CloseFrame(id string) (Frame, error) {
	return validatedFrame(Frame{Kind: FrameClose, ID: id})
}

func EncodeFrame(frame Frame) ([]byte, error) {
	frame, err := validatedFrame(frame)
	if err != nil {
		return nil, err
	}
	switch frame.Kind {
	case FrameOpen:
		return json.Marshal([]any{string(frame.Kind), frame.ID, *frame.Filter, hex.EncodeToString(frame.Message)})
	case FrameMsg:
		return json.Marshal([]any{string(frame.Kind), frame.ID, hex.EncodeToString(frame.Message)})
	case FrameError:
		if frame.MaxRecords == nil {
			return json.Marshal([]any{string(frame.Kind), frame.ID, frame.Reason})
		}
		return json.Marshal([]any{string(frame.Kind), frame.ID, frame.Reason, *frame.MaxRecords})
	case FrameClose:
		return json.Marshal([]any{string(frame.Kind), frame.ID})
	default:
		return nil, protocolErrorf("unknown frame kind %q", frame.Kind)
	}
}

func DecodeFrame(raw []byte) (Frame, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var values []json.RawMessage
	if err := dec.Decode(&values); err != nil {
		return Frame{}, protocolErrorf("decode envelope: %v", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return Frame{}, protocolErrorf("trailing JSON value")
	}
	if len(values) == 0 {
		return Frame{}, protocolErrorf("empty envelope")
	}
	var command string
	if err := json.Unmarshal(values[0], &command); err != nil {
		return Frame{}, protocolErrorf("command must be a string")
	}
	frame := Frame{Kind: FrameKind(command)}
	want := func(n ...int) bool {
		for _, size := range n {
			if len(values) == size {
				return true
			}
		}
		return false
	}
	switch frame.Kind {
	case FrameOpen:
		if !want(4) {
			return Frame{}, protocolErrorf("NEG-OPEN must contain 4 elements")
		}
		if err := decodeString(values[1], &frame.ID, "subscription id"); err != nil {
			return Frame{}, err
		}
		trimmed := bytes.TrimSpace(values[2])
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return Frame{}, protocolErrorf("NEG-OPEN filter must be an object")
		}
		var filter nostr.Filter
		if err := json.Unmarshal(values[2], &filter); err != nil {
			return Frame{}, protocolErrorf("decode filter: %v", err)
		}
		frame.Filter = &filter
		if err := decodeHexMessage(values[3], &frame.Message); err != nil {
			return Frame{}, err
		}
	case FrameMsg:
		if !want(3) {
			return Frame{}, protocolErrorf("NEG-MSG must contain 3 elements")
		}
		if err := decodeString(values[1], &frame.ID, "subscription id"); err != nil {
			return Frame{}, err
		}
		if err := decodeHexMessage(values[2], &frame.Message); err != nil {
			return Frame{}, err
		}
	case FrameError:
		if !want(3, 4) {
			return Frame{}, protocolErrorf("NEG-ERR must contain 3 or 4 elements")
		}
		if err := decodeString(values[1], &frame.ID, "subscription id"); err != nil {
			return Frame{}, err
		}
		if err := decodeString(values[2], &frame.Reason, "reason"); err != nil {
			return Frame{}, err
		}
		if len(values) == 4 {
			number := string(bytes.TrimSpace(values[3]))
			if number == "" || strings.ContainsAny(number, ".eE-+\"") {
				return Frame{}, protocolErrorf("max records must be an unsigned integer")
			}
			maximum, err := strconv.ParseUint(number, 10, 64)
			if err != nil {
				return Frame{}, protocolErrorf("invalid max records")
			}
			frame.MaxRecords = &maximum
		}
	case FrameClose:
		if !want(2) {
			return Frame{}, protocolErrorf("NEG-CLOSE must contain 2 elements")
		}
		if err := decodeString(values[1], &frame.ID, "subscription id"); err != nil {
			return Frame{}, err
		}
	default:
		return Frame{}, protocolErrorf("unknown command %q", command)
	}
	return validatedFrame(frame)
}

func validatedFrame(frame Frame) (Frame, error) {
	if frame.ID == "" {
		return Frame{}, protocolErrorf("subscription id is required")
	}
	switch frame.Kind {
	case FrameOpen:
		if frame.Filter == nil {
			return Frame{}, protocolErrorf("NEG-OPEN filter is required")
		}
		if err := validateMessage(frame.Message); err != nil {
			return Frame{}, err
		}
		if frame.Reason != "" || frame.MaxRecords != nil {
			return Frame{}, protocolErrorf("NEG-OPEN contains invalid fields")
		}
	case FrameMsg:
		if err := validateMessage(frame.Message); err != nil {
			return Frame{}, err
		}
		if frame.Filter != nil || frame.Reason != "" || frame.MaxRecords != nil {
			return Frame{}, protocolErrorf("NEG-MSG contains invalid fields")
		}
	case FrameError:
		if frame.Reason == "" {
			return Frame{}, protocolErrorf("NEG-ERR reason is required")
		}
		if frame.Filter != nil || len(frame.Message) != 0 {
			return Frame{}, protocolErrorf("NEG-ERR contains invalid fields")
		}
	case FrameClose:
		if frame.Filter != nil || len(frame.Message) != 0 || frame.Reason != "" || frame.MaxRecords != nil {
			return Frame{}, protocolErrorf("NEG-CLOSE contains invalid fields")
		}
	default:
		return Frame{}, protocolErrorf("unknown frame kind %q", frame.Kind)
	}
	return frame, nil
}

func validateMessage(message []byte) error {
	if len(message) == 0 {
		return protocolErrorf("negentropy message is empty")
	}
	if message[0] != ProtocolVersion {
		return &ProtocolError{Err: fmt.Errorf("%w: 0x%02x", ErrUnsupportedVersion, message[0])}
	}
	return nil
}

func decodeString(raw json.RawMessage, target *string, name string) error {
	if err := json.Unmarshal(raw, target); err != nil || *target == "" {
		return protocolErrorf("%s must be a nonempty string", name)
	}
	return nil
}

func decodeHexMessage(raw json.RawMessage, target *[]byte) error {
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil || encoded == "" || encoded != strings.ToLower(encoded) || len(encoded)%2 != 0 {
		return protocolErrorf("message must be nonempty canonical lowercase hex")
	}
	decoded, err := hex.DecodeString(encoded)
	if err != nil {
		return protocolErrorf("decode message hex: %v", err)
	}
	*target = decoded
	return nil
}

func protocolErrorf(format string, args ...any) error {
	return &ProtocolError{Err: fmt.Errorf(format, args...)}
}
