#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "[parity] validating OpenClaw parity matrix snapshot"
go test ./internal/gateway/methods -run 'TestGatewayMethodParityMatrixIsConsistent|TestMapNIP86Error_AuthAndMethodMappings|TestMapNIP86Error_PreconditionData' -count=1

echo "[parity] validating WS auth/rate-limit semantics"
go test ./internal/gateway/ws -run 'TestAllowHandshakeRateLimit|TestHandleWSRateLimitReturnsHTTP429|TestUnauthorizedBurstClosesConnection' -count=1

echo "[parity] validating control/admin precondition semantics"
go test ./internal/admin ./cmd/swarmstrd -run 'TestDispatchMethodCallListPutExpectedVersionZeroSemantics|TestDispatchMethodCallConfigPutExpectedVersionZeroSemantics|TestHandleControlRPCRequest_ListPutExpectedVersionZeroSemantics' -count=1

echo "[parity] validating core parity verifier contracts"
go test ./cmd/swarmstrd -run 'TestCoreParityVerifier_' -count=1

echo "[parity] all parity gates passed"
