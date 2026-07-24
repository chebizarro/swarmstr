#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "[parity] validating OpenClaw parity matrix snapshot"
go test ./internal/gateway/methods -run 'TestGatewayMethodParityMatrixIsConsistent|TestGatewayMethodParityTriageMatchesSourceRules|TestMapNIP86Error_AuthAndMethodMappings|TestMapNIP86Error_PreconditionData' -count=1

echo "[parity] validating CLI classifications and Web UI gateway callsites"
go test ./cmd/metiq -run 'TestCLIParityCatalogMatchesClassificationsAndRegistry' -count=1
go test ./internal/webui -run 'TestGatewayMethodCallsitesAreRegistered' -count=1

echo "[parity] validating WS auth/rate-limit semantics"
go test ./internal/gateway/ws -run 'TestAllowHandshakeRateLimit|TestHandleWSRateLimitReturnsHTTP429|TestUnauthorizedBurstClosesConnection' -count=1

echo "[parity] validating control/admin precondition semantics"
go test ./internal/admin ./cmd/metiqd -run 'TestDispatchMethodCallListPutExpectedVersionZeroSemantics|TestDispatchMethodCallConfigPutExpectedVersionZeroSemantics|TestHandleControlRPCRequest_ListPutExpectedVersionZeroSemantics' -count=1

echo "[parity] validating core parity verifier contracts"
go test ./cmd/metiqd -run 'TestCoreParityVerifier_' -count=1

echo "[parity] all parity gates passed"
