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

Persist failure diagnostics before containers are removed:

```bash
FIPS_DIAGNOSTICS_DIR=artifacts/fips-real-daemon \
FIPS_REAL_DAEMON_REQUIRED=1 \
./testing/fips/real-daemon/run.sh
```

The diagnostics directory receives bounded daemon/relay logs, peer/link state,
and any application-listener logs.

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

## Linux CI

`.github/workflows/fips-real-daemon.yml` is the mandatory Linux execution path.
It runs for relevant pull requests, nightly, and by manual dispatch. The job:

1. checks out `jmcorgan/fips` v0.5.0 at pinned commit
   `80f8f965aa872296edbce84ade9949ece2596602` (`FIPS_REF`);
2. caches the Cargo registry, git dependencies, and release build separately
   from the Docker layer cache;
3. builds all daemon binaries with the FIPS checkout's pinned Rust toolchain;
4. builds and loads `fips-test:latest` for `linux/amd64`;
5. runs this suite with `FIPS_REAL_DAEMON_REQUIRED=1`; and
6. uploads failure diagnostics for seven days.

Update `FIPS_REF` deliberately when swarmstr adopts a newer daemon revision.
The pin keeps nightly results reproducible and prevents an unrelated daemon
branch update from silently changing the interop contract.

## Local Docker Desktop result (2026-07-24)

A plain Docker build completed once supplied with all four binaries, so the
previous Docker Desktop metadata hang was not reproduced. The locally available
native macOS binaries cannot run in the Linux image (`Exec format error`), and
this host does not currently have `cargo-zigbuild` or a Linux Rust target
installed. Use the documented macOS cross-build above for another local attempt;
the Linux workflow avoids that cross-compilation dependency and is the reliable
mandatory path.
