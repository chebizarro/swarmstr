#!/usr/bin/env bash
set -euo pipefail

readonly LND_COMMIT="2b87887fd9ef6b3e1391dc25e4c658ee73a06fa0"
readonly TAPD_COMMIT="b5121d06b5799e263e2a9804865d88af828e87b8"
readonly PROTOC_VERSION="34.1"
readonly LND_SHA256="0920e3435a2b1fece60531f39e8e5876674a4d3fb6d03133d897471fad4d5b95"
readonly TAPD_SHA256="32495f56bf1409c178853064c330849f95c01d4ae060c6336a324bb36296e4d1"

here="$(cd "$(dirname "$BASH_SOURCE")" && pwd)"
mode="write"
if [[ $# -gt 0 ]]; then mode="$1"; fi
if [[ "$mode" != "write" && "$mode" != "--check" ]]; then
  echo "usage: $0 [--check]" >&2
  exit 2
fi
for command in curl tar protoc shasum cmp; do
  command -v "$command" >/dev/null || { echo "missing required command: $command" >&2; exit 1; }
done
actual_protoc="$(protoc --version | awk '{print $2}')"
if [[ "$actual_protoc" != "$PROTOC_VERSION" ]]; then
  echo "protoc $PROTOC_VERSION is required for byte-for-byte descriptor regeneration (found $actual_protoc)" >&2
  exit 1
fi

temp_root="$TMPDIR"
if [[ -z "$temp_root" ]]; then temp_root="/tmp"; fi
work="$(mktemp -d "$temp_root/metiq-descriptors.XXXXXX")"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/lnd" "$work/tapd" "$work/out"

curl -fsSL "https://github.com/lightningnetwork/lnd/archive/$LND_COMMIT.tar.gz" |
  tar -xz -C "$work/lnd" --strip-components=1
curl -fsSL "https://github.com/lightninglabs/taproot-assets/archive/$TAPD_COMMIT.tar.gz" |
  tar -xz -C "$work/tapd" --strip-components=1

protoc -I "$work/lnd/lnrpc" --include_imports   --descriptor_set_out="$work/out/lnd.pb"   lightning.proto routerrpc/router.proto invoicesrpc/invoices.proto walletrpc/walletkit.proto
protoc -I "$work/tapd/taprpc" --include_imports   --descriptor_set_out="$work/out/tapd.pb"   taprootassets.proto mintrpc/mint.proto assetwalletrpc/assetwallet.proto

check_sha() {
  local file="$1" expected="$2" actual
  actual="$(shasum -a 256 "$file" | awk '{print $1}')"
  [[ "$actual" == "$expected" ]] || {
    echo "unexpected descriptor checksum for $(basename "$file"): $actual (want $expected)" >&2
    exit 1
  }
}
check_sha "$work/out/lnd.pb" "$LND_SHA256"
check_sha "$work/out/tapd.pb" "$TAPD_SHA256"

if [[ "$mode" == "--check" ]]; then
  cmp "$work/out/lnd.pb" "$here/lnd.pb"
  cmp "$work/out/tapd.pb" "$here/tapd.pb"
  cmp "$work/lnd/LICENSE" "$here/LICENSE-LND"
  cmp "$work/tapd/LICENSE" "$here/LICENSE-TAPROOT-ASSETS"
  echo "descriptor assets are reproducible"
  exit 0
fi

install -m 0644 "$work/out/lnd.pb" "$here/lnd.pb"
install -m 0644 "$work/out/tapd.pb" "$here/tapd.pb"
install -m 0644 "$work/lnd/LICENSE" "$here/LICENSE-LND"
install -m 0644 "$work/tapd/LICENSE" "$here/LICENSE-TAPROOT-ASSETS"
echo "regenerated lnd.pb and tapd.pb"
