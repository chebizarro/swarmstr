#!/usr/bin/env bash
set -euo pipefail

LND_IMAGE="${LND_IMAGE:-lightninglabs/lnd:v0.21.1-beta}"
TAPD_IMAGE="${TAPD_IMAGE:-lightninglabs/taproot-assets:v0.8.0}"
BITCOIND_IMAGE="${BITCOIND_IMAGE:-bitcoin/bitcoin:28.1}"
PREFIX="metiq-lightning-${USER:-ci}-$$"
NETWORK="$PREFIX"
ROOT="$(mktemp -d "${TMPDIR:-/tmp}/metiq-lightning-regtest.XXXXXX")"
containers=("$PREFIX-bitcoind" "$PREFIX-lnd-a" "$PREFIX-lnd-b" "$PREFIX-tapd")

fail() {
  printf 'lightning integration: %s\n' "$*" >&2
  exit 1
}

cleanup() {
  docker rm -f "${containers[@]}" >/dev/null 2>&1 || true
  docker network rm "$NETWORK" >/dev/null 2>&1 || true
  rm -rf "$ROOT"
}
trap cleanup EXIT INT TERM

command -v docker >/dev/null 2>&1 || fail "Docker is required but the docker executable was not found"
docker info >/dev/null 2>&1 || fail "Docker is required but the daemon is not running or is inaccessible"

mkdir -p "$ROOT/lnd-a" "$ROOT/lnd-b" "$ROOT/tapd"
chmod 700 "$ROOT" "$ROOT/lnd-a" "$ROOT/lnd-b" "$ROOT/tapd"
docker network create "$NETWORK" >/dev/null

docker run -d --name "$PREFIX-bitcoind" --network "$NETWORK" --network-alias bitcoind   "$BITCOIND_IMAGE"   -regtest -server -txindex=1 -fallbackfee=0.00001   -rpcuser=metiq -rpcpassword=metiq-regtest   -rpcbind=0.0.0.0 -rpcallowip=0.0.0.0/0   -zmqpubrawblock=tcp://0.0.0.0:28332   -zmqpubrawtx=tcp://0.0.0.0:28333 >/dev/null

bitcoin_cli() {
  docker exec "$PREFIX-bitcoind" bitcoin-cli -regtest -rpcuser=metiq -rpcpassword=metiq-regtest "$@"
}
wait_for() {
  local description="$1"
  shift
  for _ in $(seq 1 180); do
    if "$@" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  fail "timed out waiting for $description"
}
wait_for "bitcoind RPC" bitcoin_cli getblockchaininfo
miner_address="$(bitcoin_cli getnewaddress)"
bitcoin_cli generatetoaddress 101 "$miner_address" >/dev/null

start_lnd() {
  local suffix="$1" alias="$2" host_port="$3"
  docker run -d --name "$PREFIX-lnd-$suffix" --network "$NETWORK" --network-alias "lnd-$suffix"     -p "127.0.0.1:$host_port:10009" -v "$ROOT/lnd-$suffix:/data"     "$LND_IMAGE"     --lnddir=/data --noseedbackup --nobootstrap --alias="$alias"     --bitcoin.active --bitcoin.regtest --bitcoin.node=bitcoind     --bitcoind.rpchost=bitcoind:18443 --bitcoind.rpcuser=metiq --bitcoind.rpcpass=metiq-regtest     --bitcoind.zmqpubrawblock=tcp://bitcoind:28332 --bitcoind.zmqpubrawtx=tcp://bitcoind:28333     --rpclisten=0.0.0.0:10009 --listen=0.0.0.0:9735     --tlsextraip=127.0.0.1 --tlsextradomain=localhost --tlsextradomain="lnd-$suffix" >/dev/null
}
start_lnd a metiq-a 11009
start_lnd b metiq-b 12009

lncli() {
  local suffix="$1"
  shift
  docker exec "$PREFIX-lnd-$suffix" lncli --network=regtest --rpcserver=localhost:10009     --tlscertpath=/data/tls.cert     --macaroonpath=/data/data/chain/bitcoin/regtest/admin.macaroon "$@"
}
wait_for "LND A" lncli a getinfo
wait_for "LND B" lncli b getinfo

address_a="$(lncli a newaddress p2wkh | tr -d '\r\n" ')"
address_b="$(lncli b newaddress p2wkh | tr -d '\r\n" ')"
bitcoin_cli sendtoaddress "$address_a" 1 >/dev/null
bitcoin_cli sendtoaddress "$address_b" 1 >/dev/null
bitcoin_cli generatetoaddress 6 "$miner_address" >/dev/null
wait_for "LND A wallet funding" bash -c "[[ \$(docker exec '$PREFIX-lnd-a' lncli --network=regtest --rpcserver=localhost:10009 --tlscertpath=/data/tls.cert --macaroonpath=/data/data/chain/bitcoin/regtest/admin.macaroon walletbalance | sed -n 's/.*"confirmed_balance": "\([1-9][0-9]*\)".*/\1/p') ]]"
wait_for "LND B wallet funding" bash -c "[[ \$(docker exec '$PREFIX-lnd-b' lncli --network=regtest --rpcserver=localhost:10009 --tlscertpath=/data/tls.cert --macaroonpath=/data/data/chain/bitcoin/regtest/admin.macaroon walletbalance | sed -n 's/.*"confirmed_balance": "\([1-9][0-9]*\)".*/\1/p') ]]"

pubkey_b="$(lncli b getinfo | sed -n 's/.*"identity_pubkey": "\([^"]*\)".*/\1/p')"
[[ -n "$pubkey_b" ]] || fail "could not read LND B identity pubkey"
lncli a connect "$pubkey_b@lnd-b:9735" >/dev/null
lncli a openchannel --node_key="$pubkey_b" --local_amt=10000000 --push_amt=1000000 >/dev/null
bitcoin_cli generatetoaddress 6 "$miner_address" >/dev/null
wait_for "active Lightning channel" bash -c "docker exec '$PREFIX-lnd-a' lncli --network=regtest --rpcserver=localhost:10009 --tlscertpath=/data/tls.cert --macaroonpath=/data/data/chain/bitcoin/regtest/admin.macaroon listchannels | grep -q '"active": true'"

docker run -d --name "$PREFIX-tapd" --network "$NETWORK" --network-alias tapd   -p 127.0.0.1:10029:10029   -v "$ROOT/tapd:/data" -v "$ROOT/lnd-a:/lnd:ro"   "$TAPD_IMAGE"   --tapddir=/data --network=regtest   --lnd.host=lnd-a:10009   --lnd.macaroonpath=/lnd/data/chain/bitcoin/regtest/admin.macaroon   --lnd.tlspath=/lnd/tls.cert   --rpclisten=0.0.0.0:10029 --tlsextraip=127.0.0.1 --tlsextradomain=localhost >/dev/null

tapcli() {
  docker exec "$PREFIX-tapd" tapcli --network=regtest --rpcserver=localhost:10029     --tlscertpath=/data/tls.cert --macaroonpath=/data/data/regtest/admin.macaroon "$@"
}
wait_for "tapd" tapcli getinfo
tapcli assets mint --type normal --name metiqinterop --supply 1000 >/dev/null
tapcli assets mint finalize >/dev/null
bitcoin_cli generatetoaddress 1 "$miner_address" >/dev/null
wait_for "confirmed Taproot Asset mint" bash -c "docker exec '$PREFIX-tapd' tapcli --network=regtest --rpcserver=localhost:10029 --tlscertpath=/data/tls.cert --macaroonpath=/data/data/regtest/admin.macaroon assets list | grep -q 'asset_id'"

export METIQ_LND_A_TARGET=127.0.0.1:11009
export METIQ_LND_A_TLS_CERT="$ROOT/lnd-a/tls.cert"
export METIQ_LND_A_MACAROON="$ROOT/lnd-a/data/chain/bitcoin/regtest/admin.macaroon"
export METIQ_LND_B_TARGET=127.0.0.1:12009
export METIQ_LND_B_TLS_CERT="$ROOT/lnd-b/tls.cert"
export METIQ_LND_B_MACAROON="$ROOT/lnd-b/data/chain/bitcoin/regtest/admin.macaroon"
export METIQ_TAPD_TARGET=127.0.0.1:10029
export METIQ_TAPD_TLS_CERT="$ROOT/tapd/tls.cert"
export METIQ_TAPD_MACAROON="$ROOT/tapd/data/regtest/admin.macaroon"

go test -tags=lightning_integration ./internal/lightning -run '^TestContainerRegtestInteroperability$' -count=1 -v
