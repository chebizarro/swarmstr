# FIPS Manual Soak Scenarios

These scenarios validate behavior that requires a real FIPS daemon/sidecar and cannot be fully exercised by the in-process loopback tests.

Run metiq with:

```bash
go test -tags experimental_fips -run 'TestIntegration_' -count=1 ./internal/nostr/runtime
```

## 1. Daemon restart with pooled TCP connections

Goal: verify metiq recovers when a pooled FIPS TCP connection points at a daemon that restarts.

1. Start two metiq agents with `-tags experimental_fips` and FIPS enabled.
2. Start FIPS daemons for both agents and wait until direct FIPS DM succeeds.
3. Send several DMs A → B to establish and reuse the pooled TCP connection.
4. Restart B's FIPS daemon without restarting metiq.
5. Send another DM A → B.
6. Verify either:
   - the write fails once, metiq evicts the stale pooled connection, reconnects, and the retry succeeds; or
   - the selector classifies the failure as transport, falls back to relay, and retries FIPS after the negative-cache TTL.
7. Continue sending DMs until FIPS succeeds again.

Expected result: no permanent send failure, no stuck stale connection, and later FIPS success clears any negative-cache entry.

## 2. Sustained exchange during daemon rekey

Goal: verify FIPS v0.3.0+ hitless rekey is transparent to metiq.

1. Start two agents and their FIPS daemons.
2. Begin a sustained bidirectional DM stream.
3. Trigger daemon rekey according to the FIPS daemon operator command/configuration.
4. Continue the stream through the rekey window.
5. Verify message counts and logs.

Expected result: no message loss attributable to metiq framing or pooled TCP handling.

## 3. Delayed discovery startup

Goal: verify asynchronous discovery does not permanently suppress FIPS.

1. Start metiq with FIPS enabled before peer discovery is ready.
2. Send A → B immediately.
3. Confirm relay fallback and a temporary FIPS negative-cache entry.
4. Let FIPS discovery complete.
5. Wait for the configured negative-cache TTL or clear selector network state.
6. Send A → B again.

Expected result: the later send attempts FIPS again and a successful FIPS send clears the negative cache.

## 4. Negative-cache TTL expiry

Goal: verify transient FIPS failures do not cache forever.

1. Force FIPS path failure for A → B.
2. Send A → B and confirm relay fallback.
3. Restore FIPS path availability.
4. Wait for `ReachCacheTTL`.
5. Send A → B again.

Expected result: metiq retries FIPS after TTL expiry instead of staying relay-only.
