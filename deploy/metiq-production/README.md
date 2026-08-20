# Dedicated Metiq production runbook

This directory is the reproducible Track A input for a dedicated **metiqd**
SoulFactory runtime. It does not deploy a host, publish events, or contact a
relay. Rendered files are specific to one isolated runtime and must never reuse
an incumbent runtime's identity, volumes, container, or controller policy.

## Security and runtime contract

- The runtime identity remains in Signet. Metiq receives NIP-46 signing through
  a stable file-backed client-channel key.
- bootstrap.json contains only a sanitized bunker locator. It must not contain
  one-time connection material or an identity private key.
- config.json trusts one explicit SoulFactory controller public key and only
  soulfactory.provision. Direct messages, heartbeats, legacy token fallback,
  execution, network tools, and writes are disabled by default.
- Kind values are supplied by cascadia-go in the application. The kind 30317
  capability is parameterized-replaceable and advertises exactly
  soulfactory.provision.
- metiq-production-state persists checkpoints and managed-agent bindings.
  metiq-production-workspace is separate persistent workspace state.
- The rootless container is read-only outside its volumes/tmpfs, drops all
  capabilities, sets NoNewPrivileges, uses journald, and has CPU, memory,
  process-count, health, and restart limits.
- The admin listener is container-loopback only and is not host-published.

## 1. Capture immutable source and image pins

Start from a clean checkout at the reviewed source revision:

~~~bash
SOURCE_COMMIT="$(git rev-parse HEAD)"
test -z "$(git status --porcelain)"
git show --no-patch --format=fuller "$SOURCE_COMMIT"
~~~

Resolve every Dockerfile base to a registry digest and record the output in the
change record. Never build production with floating base tags.

~~~bash
GOLANG_IMAGE='docker.io/library/golang:1.25-bookworm@sha256:<64-hex>'
DEBIAN_IMAGE='docker.io/library/debian:bookworm@sha256:<64-hex>'
DEBIAN_SLIM_IMAGE='docker.io/library/debian:bookworm-slim@sha256:<64-hex>'
NODE_IMAGE='docker.io/library/node:24-bookworm-slim@sha256:<64-hex>'

podman build --pull=never \
  --build-arg VERSION="$SOURCE_COMMIT" \
  --build-arg COMMIT="$SOURCE_COMMIT" \
  --build-arg GOLANG_IMAGE="$GOLANG_IMAGE" \
  --build-arg DEBIAN_BOOKWORM_IMAGE="$DEBIAN_IMAGE" \
  --build-arg DEBIAN_BOOKWORM_SLIM_IMAGE="$DEBIAN_SLIM_IMAGE" \
  --build-arg NODE_IMAGE="$NODE_IMAGE" \
  --tag registry.example/metiq:"$SOURCE_COMMIT" .
podman push registry.example/metiq:"$SOURCE_COMMIT"
METIQ_IMAGE="$(skopeo inspect --format '{{.Name}}@{{.Digest}}' \
  docker://registry.example/metiq:"$SOURCE_COMMIT")"
case "$METIQ_IMAGE" in *@sha256:*) ;; *) exit 1 ;; esac
~~~

Registry credentials belong in the container auth file, never in a command
argument or captured output. Preserve the source commit, four base image
digests, final image digest, and rendered-file checksums in the release record.

## 2. Render secret-free runtime inputs

The Signet locator used here is the steady-state locator after enrollment. It
must not carry a one-time connection parameter.

~~~bash
export METIQ_SOURCE_COMMIT="$SOURCE_COMMIT"
export METIQ_IMAGE
export METIQ_SIGNER_URL='bunker://<signet-pubkey>?relay=wss%3A%2F%2F<relay>'
export METIQ_RELAYS_JSON='["wss://<canonical-relay-1>","wss://<canonical-relay-2>"]'
export SOULFACTORY_CONTROLLER_PUBKEY='<64-hex-controller-pubkey>'
./deploy/metiq-production/render.sh /tmp/metiq-production-rendered
sha256sum /tmp/metiq-production-rendered/* > /tmp/metiq-production-rendered/SHA256SUMS
~~~

Review the output and confirm that the controller is intended, the image is
digest-addressed, the source pin matches the checkout, and no placeholder
remains.

## 3. Create the isolated boundary

Run these steps as a dedicated, non-incumbent rootless service account.

~~~bash
install -d -m 700 ~/.config/containers/systemd
install -m 600 /tmp/metiq-production-rendered/*.volume \
  ~/.config/containers/systemd/
install -m 600 /tmp/metiq-production-rendered/metiq-production.container \
  ~/.config/containers/systemd/

podman volume create metiq-production-state
podman volume create metiq-production-workspace
STATE_MOUNT="$(podman volume mount metiq-production-state)"
install -m 600 /tmp/metiq-production-rendered/bootstrap.json "$STATE_MOUNT/bootstrap.json"
install -m 600 /tmp/metiq-production-rendered/config.json "$STATE_MOUNT/config.json"
~~~

Do not mount an incumbent home directory or reuse an incumbent volume name.

### File-backed NIP-46 enrollment and one-time cleanup

Obtain from Signet through an approved secret channel:

1. the dedicated runtime's one-time bunker connection file;
2. a dedicated stable NIP-46 client-channel key file, or authorization to
   generate one locally;
3. the expected runtime public key and the least-privilege Signet policy
   permitting only event kinds required by capability/control-result publishing
   and encrypted state/transport.

Create the stable Podman secret without placing it in argv or logs:

~~~bash
CLIENT_KEY_FILE=/run/user/"$(id -u)"/metiq-nip46-client-key
install -m 600 /approved/input/client-channel-key "$CLIENT_KEY_FILE"
podman secret create metiq-nip46-client-key "$CLIENT_KEY_FILE"
rm -f "$CLIENT_KEY_FILE"
~~~

For first authorization only, create a bootstrap file on user tmpfs. rawfile
keeps the one-time value out of process arguments.

~~~bash
ONE_TIME_FILE=/run/user/"$(id -u)"/signet-one-time-bunker
ENROLL_DIR=/run/user/"$(id -u)"/metiq-enroll
install -d -m 700 "$ENROLL_DIR"
jq --rawfile signer "$ONE_TIME_FILE" \
  '.signer_url=($signer | sub("[\\r\\n]+$"; ""))' \
  /tmp/metiq-production-rendered/bootstrap.json > "$ENROLL_DIR/bootstrap.json"
chmod 600 "$ENROLL_DIR/bootstrap.json"

podman run --name metiq-nip46-enroll --rm \
  --read-only --tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m \
  --cap-drop=all --security-opt=no-new-privileges \
  --volume metiq-production-state:/data/.metiq:rw \
  --volume metiq-production-workspace:/data/.metiq/workspace:rw \
  --volume "$ENROLL_DIR/bootstrap.json:/run/metiq/bootstrap.json:ro,Z" \
  --secret metiq-nip46-client-key,type=mount,target=/run/secrets/metiq-nip46-client-key \
  --env HOME=/data --env METIQ_BOOTSTRAP_PATH=/run/metiq/bootstrap.json \
  "$METIQ_IMAGE"
~~~

Approve only the expected Signet request. Stop the enrollment container after a
successful signed health/status check. Do not capture its raw logs. Then remove
all one-time material and verify absence:

~~~bash
podman stop metiq-nip46-enroll 2>/dev/null || true
rm -f "$ONE_TIME_FILE" "$ENROLL_DIR/bootstrap.json"
rmdir "$ENROLL_DIR"
test ! -e "$ONE_TIME_FILE"
~~~

The steady-state bootstrap uses the sanitized locator plus the stable
file-backed client key, so restart does not consume another grant. Keep a copy
of the client-channel key only in approved secret escrow; it is not part of
runtime-state backups or evidence.

## 4. Deploy and verify

~~~bash
systemctl --user daemon-reload
systemctl --user enable --now metiq-production.service
systemctl --user status metiq-production.service
podman inspect metiq-production --format '{{.ImageName}} {{.State.Health.Status}}'
podman healthcheck run metiq-production
podman exec metiq-production curl -fsS http://127.0.0.1:7423/healthz
podman exec metiq-production curl -fsS http://127.0.0.1:7423/health
~~~

Interpret healthz as liveness and health as local admin readiness. Confirm the
returned identity equals the dedicated Signet identity and differs from every
incumbent before enabling controller traffic.

Inspect structured journal records without exporting event bodies:

~~~bash
journalctl --user -u metiq-production.service -o json --since today
podman stats --no-stream metiq-production
podman inspect metiq-production --format '{{json .HostConfig}}'
~~~

On every daemon start, CapabilityMonitor.runPublisher immediately republishes
the replaceable capability. Confirm the newest accepted kind 30317 is authored
by the dedicated runtime, has its canonical d tag, contains only
soulfactory.provision, and lists only the intended controller.

## 5. Backup and restore

Quiesce the service so checkpoint and binding documents are captured together.
The archive contains neither the Signet identity key nor NIP-46 client key.

~~~bash
BACKUP_DIR=/secure/backups/metiq-production/"$(date -u +%Y%m%dT%H%M%SZ)"
install -d -m 700 "$BACKUP_DIR"
systemctl --user stop metiq-production.service
podman run --rm --entrypoint /bin/tar \
  -v metiq-production-state:/snapshot/state:ro \
  -v metiq-production-workspace:/snapshot/workspace:ro \
  -v "$BACKUP_DIR:/backup:rw,Z" \
  "$METIQ_IMAGE" -C /snapshot -czf /backup/state-workspace.tgz state workspace
cp /tmp/metiq-production-rendered/{IMAGE_DIGEST,SOURCE_COMMIT,bootstrap.json,config.json} "$BACKUP_DIR/"
sha256sum "$BACKUP_DIR"/* > "$BACKUP_DIR/SHA256SUMS"
systemctl --user start metiq-production.service
~~~

Restore only into new dedicated volumes after verifying SHA256SUMS. Obtain the
matching client-channel key from approved secret escrow.

~~~bash
systemctl --user stop metiq-production.service
podman volume rm metiq-production-state metiq-production-workspace
podman volume create metiq-production-state
podman volume create metiq-production-workspace
podman run --rm --entrypoint /bin/tar \
  -v metiq-production-state:/restore/state:rw \
  -v metiq-production-workspace:/restore/workspace:rw \
  -v "$BACKUP_DIR:/backup:ro,Z" \
  "$METIQ_IMAGE" -C /restore -xzf /backup/state-workspace.tgz
systemctl --user start metiq-production.service
~~~

Verify readiness, exact replay, duplicate_conflict, recovered runtime_binding,
and capability republish after restoration.

## 6. Rollback

Before every upgrade capture the previous final image digest/source commit,
rendered bootstrap/config with checksums, a quiesced state/workspace backup, and
current dedicated runtime public key/capability event ID.

To roll back, stop the service, restore the prior archive, replace Image with
the captured prior digest, restore captured secret-free config, reload systemd,
and start. Verify identity, health, capability replacement, replay, and
controller allowlist. Never start, stop, or edit an incumbent OpenClaw service.

## 7. Incident guidance

- **Identity mismatch or possible key exposure:** stop Metiq, revoke its Signet
  authorization/client-channel key, preserve only sanitized event IDs, and
  provision a new dedicated identity. Never fall back to a local identity key.
- **Unexpected controller or method:** stop Metiq, preserve rendered-config
  checksum and offending event ID, correct the allowlist, and verify fail-closed
  behavior before restart.
- **Replay anomaly:** stop controller delivery, quiesce/back up both volumes,
  record request/result event IDs, and do not use a new idempotency key until
  checkpoint state is inspected.
- **Capability drift:** stop Metiq if kind 30317 advertises more than
  soulfactory.provision; restore the reviewed digest/config and republish.
- **Relay outage or late result:** keep the subscription open, use EOSE for
  backfill completion, and reconcile by correlation/idempotency key. Do not poll
  or create a second provisioning request.
- **Resource exhaustion:** inspect journald, cgroup counters, and volume usage;
  retain limits and restore/replay rather than sharing an incumbent runtime.

## 8. Track B disposable-soul live validation

Track B is intentionally not executed by this artifact task. Use a disposable
soul and canonical relays/Signet only after Track A review.

Evidence must be immutable and sanitized. Record event IDs, timestamps, relay
acceptance status, source/image/config checksums, and pass/fail assertions only.
Do not store event content, decrypted payloads, connection-bearing URLs, tokens,
key files, argv dumps, or raw logs.

1. Record winning kind 31952 Soul definition and kind 5950 request event IDs;
   prove runtime selection resolves to the dedicated Metiq kind 30317.
2. Send one addressed, validly signed kind 38384 to the dedicated runtime.
   Record only its event ID and selected capability event ID.
3. Prove exactly one local binding is created and its identity/state does not
   overlap an incumbent.
4. Record the signed kind 38386 result event ID. Verify its e, p, request,
   method, idempotency, soul, agent, and spec-hash correlations without
   retaining decrypted content.
5. Verify resulting kind 31951 and kind 7950 projections contain no credentials
   or connection material; retain only event IDs and the assertion.
6. Verify kind 30317 contains exactly soulfactory.provision. Send every other
   lifecycle/customization method and record rejected result IDs proving a
   fail-closed authorization or unsupported-method response with no binding
   mutation.
7. Replay the exact 38384 with the same idempotency key. Prove the cached prior
   payload is returned and no second provisioning side effect occurs.
8. Send a conflicting request with the same idempotency key. Record the result
   ID proving duplicate_conflict and unchanged binding/spec state.
9. Restart the bridge. Prove readiness, a newer replacement kind 30317,
   recovered checkpoint/binding state, exact replay, and conflict behavior.
10. Exercise Bahia in both orders: result observed live, and result arriving
    during/after an EOSE-bounded backfill. Prove identical final reconciliation
    and no duplicate provisioning.
11. Re-run the OpenClaw disposable control path unchanged. Prove its identity,
    state, capability, and service were not mutated by Metiq.
12. Rehearse rollback to the captured prior Metiq digest/config/state, then
    verify health, identity, capability, replay recovery, and OpenClaw safety.

A second operator must independently verify identity isolation, controller and
Signet least privilege, secret hygiene, persistent-state recovery, event
lineage/signatures, OpenClaw regression safety, and rollback evidence.
