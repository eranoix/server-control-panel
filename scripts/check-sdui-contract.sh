#!/usr/bin/env bash
# check-sdui-contract.sh: the CI gate that replaces the compile error SDUI removed.
#
# The app ships through its own F-Droid repository with no forced updates, so an
# installed build can sit for months while the server keeps changing. A contract
# change in the SDUI payload would break that old build with no warning, because
# there is no client-side `go build` to catch it. This script is that build.
#
# Three checks, none optional:
#   1. Drift: contracts/sdui/contract.json (read by published apps) must match what
#      the Go generator produces (`make sdui-contract`).
#   2. `sdui-compat check`: the CURRENT contract/fixtures against every frozen
#      manifest in contracts/sdui/client-support/ (one per published app build).
#      With zero frozen manifests this step is inert by design.
#   3. Golden screen harness: every registered screen has committed admin and
#      non-admin goldens and, when they differ, an entry in forbiddenForNonAdmin
#      (or a reasoned entry in screensWithNoRoleDifference). This is what stops a
#      new screen from silently leaking admin-only content.
#
# Any failure exits non-zero and names the exact manifest/screen/field.
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
