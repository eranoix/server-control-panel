#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

SPEC_PATH="android/data/mobile-api-client/openapi/mobile-v1.yaml"

if [ ! -f "$SPEC_PATH" ]; then
  echo "check-mobile-openapi-drift: FAILED -- $SPEC_PATH does not exist in this checkout" >&2
  exit 1
fi

backup="$(mktemp)"
cp "$SPEC_PATH" "$backup"
trap 'cp "$backup" "$SPEC_PATH"; rm -f "$backup"' EXIT

make mobile-openapi-spec

if ! diff -u "$backup" "$SPEC_PATH" > /tmp/mobile-openapi-drift.diff 2>&1; then
  echo "check-mobile-openapi-drift: FAILED -- $SPEC_PATH is out of date with the current huma registry (internal/mobilebff). Run 'make mobile-openapi-spec' and commit the result. Diff:" >&2
  cat /tmp/mobile-openapi-drift.diff >&2
  rm -f /tmp/mobile-openapi-drift.diff
  exit 1
fi
rm -f /tmp/mobile-openapi-drift.diff

echo "check-mobile-openapi-drift: OK -- $SPEC_PATH matches what the current huma registry would generate"
