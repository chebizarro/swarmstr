# Real FIPS daemon interoperability (opt-in)

This suite is the real-daemon counterpart to the loopback tests in
`internal/nostr/runtime/fips_integration_test.go`. It runs two privileged FIPS
Linux daemons with real `fips0` TUN interfaces and a small event-driven Nostr
relay, then executes the production swarmstr FIPS transport against them.

## Coverage

The runner validates:

- both daemons publish signed kind-37195 adverts using the
  `d=fips-overlay-v1`, `protocol`, `version`, `expiration`, and structured
  endpoint schema;
- discovery is performed via `REQ` / `EVENT` / `EOSE`, and both daemons form
  links from those adverts;
- `.fips` DNS resolution primes daemon identity state;
- bidirectional version-1 DM and control frames cross real FIPS TUN paths;
- authenticated socket identities match claimed senders; forged DM and control
  claims are rejected;
- delivery completion comes from DM/control callbacks, not sleep-based polling.

The bounded shell loops only check daemon, link, and listener health.

## Run

The suite deliberately does not pull or synthesize a daemon image. It uses a
locally built FIPS test image and skips cleanly when that image is absent:

```bash
./testing/fips/real-daemon/run.sh
# SKIP: real FIPS daemon image 'fips-test:latest' is unavailable ...
```

Make absence fatal in CI:

```bash
FIPS_REAL_DAEMON_REQUIRED=1 ./testing/fips/real-daemon/run.sh
```

Select another local image or platform:

```bash
FIPS_TEST_IMAGE=my-fips:test \
FIPS_TEST_PLATFORM=linux/amd64 \
./testing/fips/real-daemon/run.sh
```

Docker must support privileged containers, `NET_ADMIN`, `/dev/net/tun`, and
IPv6. The image must contain `fips`, `fipsctl`, `dnsmasq`, `ping`, and the FIPS
test image's usual runtime libraries.

## Building the daemon image

Using the reference checkout at `/Users/bizarro/Documents/Dev/fips`:

### Linux

```bash
cd /Users/bizarro/Documents/Dev/fips
./testing/scripts/build.sh
# produces fips-test:latest
```

### macOS cross-build

The FIPS build script uses `cargo-zigbuild` and the
`x86_64-unknown-linux-musl` target:

```bash
cargo install cargo-zigbuild
rustup target add x86_64-unknown-linux-musl
cd /Users/bizarro/Documents/Dev/fips
DOCKER_DEFAULT_PLATFORM=linux/amd64 ./testing/scripts/build.sh
docker image inspect fips-test:latest
```

Run that image under Docker Desktop emulation with:

```bash
FIPS_TEST_PLATFORM=linux/amd64 ./testing/fips/real-daemon/run.sh
```

The reference build script is authoritative; keep these commands aligned with
`fips/testing/scripts/build.sh` if its target or image name changes.

## Known local Docker Desktop blocker (2026-07-24)

The FIPS source itself builds natively on this host:

```bash
cd /Users/bizarro/Documents/Dev/fips
CARGO_TARGET_DIR=/tmp/fips-target cargo build --release --bin fips
```

The local Docker Desktop daemon could not produce the Linux test image:

1. BuildKit remained stuck resolving already-cached base image metadata
   (`docker/dockerfile:1` and `ubuntu:latest`).
2. The legacy builder stalled while accepting even a pre-archived, target-free
   source context.
3. A disposable Ubuntu compile container progressed through dependency
   compilation but `rustables` bindgen failed because `libclang.so` was absent.

Per WS-E scope, the suite is therefore committed as opt-in and E7 remains open
until it passes on a healthy Linux/Docker runner. See the linked beads hardening
issue for CI/image work; do not replace these tests with loopback mocks.
