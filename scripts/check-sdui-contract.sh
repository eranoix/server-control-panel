#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "=== check-sdui-contract: 1/3 — committed contract matches the generator ==="
go test ./internal/mobilebff/sdui/ -run '^TestContractDriftAgainstCommittedFile$' -v -count=1

echo
echo "=== check-sdui-contract: 2/3 — compatibility with manifests of published apps ==="
go run ./cmd/sdui-compat check

echo
echo "=== check-sdui-contract: 3/3 — golden screen harness (per-screen RBAC omission) ==="
go test ./internal/mobilebff/sdui/ -run '^(TestGoldenScreens|TestGoldenScreens_EmptyRegistryPassesTrivially|TestGoldenScreens_RoleOmissionIsReasoned|TestGoldenScreens_NonAdminNeverContainsForbiddenStrings|TestFixtureConformance|TestFixtureRoundTripMatchesRealMarshaller)$' -v -count=1

echo
echo "check-sdui-contract: OK — committed contract, frozen manifests and screen goldens are all consistent"
