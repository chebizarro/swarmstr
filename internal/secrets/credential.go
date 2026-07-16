package secrets

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const MaxCredentialBytes = 1 << 20

type CredentialEncoding string

const (
	CredentialEncodingText CredentialEncoding = "text"
	CredentialEncodingHex  CredentialEncoding = "hex"
)

type CredentialSource struct {
	Ref      string
	Encoding CredentialEncoding
}

type ValueResolver interface {
	ResolveBytes(context.Context, CredentialSource) ([]byte, error)
}

// ResolveBytes resolves only explicit secret or absolute file references.
// Unlike Resolve, it never accepts plaintext credential material.
func (s *Store) ResolveBytes(ctx context.Context, source CredentialSource) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ref := strings.TrimSpace(source.Ref)
	var raw []byte
	if strings.HasPrefix(ref, "file:") {
		path := strings.TrimPrefix(ref, "file:")
		if path == "" || !filepath.IsAbs(path) {
			return nil, fmt.Errorf("credential file reference must use an absolute path")
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("read credential file %s: %w", path, err)
		}
		defer file.Close()
		limited, err := io.ReadAll(io.LimitReader(file, MaxCredentialBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read credential file %s: %w", path, err)
		}
		if len(limited) > MaxCredentialBytes {
			return nil, fmt.Errorf("credential file %s exceeds %d bytes", path, MaxCredentialBytes)
		}
		raw = limited
	} else {
		resolveRef := ref
		if strings.HasPrefix(ref, "secret:") {
			name := strings.TrimSpace(strings.TrimPrefix(ref, "secret:"))
			if name == "" {
				return nil, fmt.Errorf("credential secret reference is invalid")
			}
			resolveRef = "env:" + name
		} else if _, explicit := parseSecretRef(ref); !explicit {
			return nil, fmt.Errorf("credential source must be an explicit secret or file reference")
		}
		value, found := s.Resolve(resolveRef)
		if !found {
			return nil, fmt.Errorf("credential reference is unavailable")
		}
		raw = []byte(value)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("credential source resolved to an empty value")
	}
	encoding := CredentialEncoding(strings.ToLower(strings.TrimSpace(string(source.Encoding))))
	if encoding == "" {
		encoding = CredentialEncodingText
	}
	switch encoding {
	case CredentialEncodingHex:
		encoded := make([]byte, hex.EncodedLen(len(raw)))
		hex.Encode(encoded, raw)
		return encoded, nil
	case CredentialEncodingText:
		for _, value := range raw {
			if value < 0x20 || value > 0x7e {
				return nil, fmt.Errorf("text credential contains non-ASCII metadata bytes")
			}
		}
		return append([]byte(nil), raw...), nil
	default:
		return nil, fmt.Errorf("unsupported credential encoding %q", source.Encoding)
	}
}
