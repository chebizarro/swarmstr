package toolbuiltin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip59"

	"metiq/internal/agent"
	nostruntime "metiq/internal/nostr/runtime"
)

// NostrDMDecryptDef is the ToolDefinition for nostr_dm_decrypt.
var NostrDMDecryptDef = agent.ToolDefinition{
	Name:        "nostr_dm_decrypt",
	Description: "Decrypt DM-like Nostr payloads (NIP-04 kind:4, NIP-59 gift wrap kind:1059, or direct ciphertext with sender pubkey).",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"event":         {Type: "object", Description: "Optional event object to decrypt. Provide kind/content/pubkey/tags fields."},
			"ciphertext":    {Type: "string", Description: "Optional raw ciphertext for direct decrypt mode."},
			"sender_pubkey": {Type: "string", Description: "Sender pubkey (hex or npub) for direct decrypt mode."},
			"scheme":        {Type: "string", Description: "Optional override: auto|nip04|nip44|giftwrap"},
		},
	},
}

// NostrDMDecryptTool decrypts fetched DM events or raw ciphertext.
func NostrDMDecryptTool(opts NostrToolOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		if opts.Keyer == nil {
			return "", dmDecryptErr("no_keyer", "signing/decryption keyer not configured")
		}
		scheme := strings.ToLower(strings.TrimSpace(argString(args, "scheme")))
		if scheme == "" {
			scheme = "auto"
		}

		if evRaw, ok := args["event"]; ok && evRaw != nil {
			ev, err := decodeEventArg(evRaw)
			if err != nil {
				return "", dmDecryptErr("invalid_event", "invalid event payload")
			}
			return decryptEvent(ctx, opts, ev, scheme)
		}

		ciphertext := argString(args, "ciphertext")
		senderRaw := argString(args, "sender_pubkey")
		if strings.TrimSpace(ciphertext) == "" || strings.TrimSpace(senderRaw) == "" {
			return "", dmDecryptErr("missing_inputs", "provide event, or ciphertext + sender_pubkey")
		}
		if scheme == "nip04" {
			if err := validateNIP04Ciphertext(ciphertext); err != nil {
				return "", dmDecryptErr("invalid_nip04_ciphertext", err.Error())
			}
		}
		senderHex, err := resolveNostrPubkey(senderRaw)
		if err != nil {
			return "", dmDecryptErr("invalid_sender_pubkey", "sender_pubkey must be hex pubkey or npub")
		}
		sender, err := nostr.PubKeyFromHex(senderHex)
		if err != nil {
			return "", dmDecryptErr("invalid_sender_pubkey", "sender_pubkey must be a valid 32-byte hex key")
		}

		plaintext, usedScheme, err := decryptCiphertext(ctx, opts, sender, ciphertext, scheme)
		if err != nil {
			return "", mapDMDecryptErr(err)
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "scheme": usedScheme, "plaintext": plaintext})
		return string(out), nil
	}
}

func decryptEvent(ctx context.Context, opts NostrToolOpts, ev nostr.Event, scheme string) (string, error) {
	if scheme == "giftwrap" || ev.Kind == nostr.KindGiftWrap {
		rumor, err := nip59.GiftUnwrap(ev, func(otherpubkey nostr.PubKey, ciphertext string) (string, error) {
			return opts.Keyer.Decrypt(ctx, ciphertext, otherpubkey)
		})
		if err != nil {
			return "", dmDecryptErr("giftwrap_decrypt_failed", err.Error())
		}
		out, _ := json.Marshal(map[string]any{
			"ok":        true,
			"scheme":    "giftwrap",
			"plaintext": rumor.Content,
			"rumor":     eventToMap(rumor),
		})
		return string(out), nil
	}

	if scheme == "nip04" || ev.Kind == nostr.KindEncryptedDirectMessage {
		dec, ok := opts.Keyer.(nostruntime.NIP04Decrypter)
		if !ok {
			return "", dmDecryptErr("unsupported_nip04", "keyer does not support NIP-04 decrypt")
		}
		plaintext, err := dec.DecryptNIP04(ctx, ev.Content, ev.PubKey)
		if err != nil {
			return "", mapDMDecryptErr(err)
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "scheme": "nip04", "plaintext": plaintext})
		return string(out), nil
	}

	if ev.Kind == nostr.KindDirectMessage {
		out, _ := json.Marshal(map[string]any{"ok": true, "scheme": "nip17", "plaintext": ev.Content})
		return string(out), nil
	}

	plaintext, usedScheme, err := decryptCiphertext(ctx, opts, ev.PubKey, ev.Content, scheme)
	if err != nil {
		return "", mapDMDecryptErr(err)
	}
	out, _ := json.Marshal(map[string]any{"ok": true, "scheme": usedScheme, "plaintext": plaintext})
	return string(out), nil
}

func decryptCiphertext(ctx context.Context, opts NostrToolOpts, sender nostr.PubKey, ciphertext, scheme string) (string, string, error) {
	if scheme == "nip04" {
		dec, ok := opts.Keyer.(nostruntime.NIP04Decrypter)
		if !ok {
			return "", "", fmt.Errorf("keyer does not support NIP-04 decrypt")
		}
		plain, err := dec.DecryptNIP04(ctx, ciphertext, sender)
		return plain, "nip04", err
	}
	plain, err := opts.Keyer.Decrypt(ctx, ciphertext, sender)
	if err != nil {
		if scheme == "nip44" {
			return "", "", err
		}
		if dec, ok := opts.Keyer.(nostruntime.NIP04Decrypter); ok {
			plain04, err04 := dec.DecryptNIP04(ctx, ciphertext, sender)
			if err04 == nil {
				return plain04, "nip04", nil
			}
		}
		return "", "", err
	}
	return plain, "nip44", nil
}

func decodeEventArg(v any) (nostr.Event, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nostr.Event{}, err
	}
	var ev nostr.Event
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nostr.Event{}, err
	}
	return ev, nil
}

func argString(args map[string]any, key string) string {
	if s, ok := args[key].(string); ok {
		return s
	}
	return ""
}

func dmDecryptErr(code, message string) error {
	payload, _ := json.Marshal(map[string]any{
		"code":    code,
		"message": strings.TrimSpace(message),
	})
	return fmt.Errorf("nostr_dm_decrypt_error:%s", string(payload))
}

func mapDMDecryptErr(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(err.Error())
	lmsg := strings.ToLower(msg)
	switch {
	case errors.Is(err, nostruntime.ErrInvalidPadding), errors.Is(err, nostruntime.ErrInvalidPlaintext):
		return dmDecryptErr("decrypt_failed", "ciphertext failed NIP-04 integrity validation")
	case strings.Contains(lmsg, "keyer does not support nip-04"):
		return dmDecryptErr("unsupported_nip04", "keyer does not support NIP-04 decryption")
	case strings.Contains(lmsg, "base64") || strings.Contains(lmsg, "invalid byte"):
		return dmDecryptErr("invalid_ciphertext", "ciphertext format is invalid for selected scheme")
	case strings.Contains(lmsg, "decrypt"):
		return dmDecryptErr("decrypt_failed", msg)
	default:
		return dmDecryptErr("operation_failed", msg)
	}
}

func validateNIP04Ciphertext(ciphertext string) error {
	parts := strings.SplitN(strings.TrimSpace(ciphertext), "?iv=", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return fmt.Errorf("expected NIP-04 ciphertext format '<base64>?iv=<base64>'")
	}
	if _, err := base64.StdEncoding.DecodeString(parts[0]); err != nil {
		return fmt.Errorf("ciphertext payload is not valid base64")
	}
	if _, err := base64.StdEncoding.DecodeString(parts[1]); err != nil {
		return fmt.Errorf("ciphertext iv is not valid base64")
	}
	return nil
}
