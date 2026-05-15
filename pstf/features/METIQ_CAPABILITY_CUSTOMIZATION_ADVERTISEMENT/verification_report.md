# METIQ_CAPABILITY_CUSTOMIZATION_ADVERTISEMENT Verification

## Evidence

- `go test ./internal/nostr/runtime ./internal/gateway/methods ./cmd/metiqd`

## Result

Passed locally on 2026-05-15.

## Notes

Metiq advertises method-name parity with OpenClaw but marks provider-specific live hooks as partial/stubbed where the current handlers persist requested customization instead of invoking live provider reload/generation/system-prompt paths.
