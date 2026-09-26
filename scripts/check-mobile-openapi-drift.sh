#!/usr/bin/env bash
# check-mobile-openapi-drift.sh: the committed mobile contract
# (android/data/mobile-api-client/openapi/mobile-v1.yaml) must stay exactly what
# `make mobile-openapi-spec` would generate from today's huma registry in
# internal/mobilebff.
#
# Catches a BFF handler whose struct tag (or registry field/route) changed without
# regenerating the spec: the generated Kotlin client would compile against a
# contract the server no longer honours.
#
# The generator only writes to the fixed path above (see cmd/mobile-openapi-gen),
# so the original file is restored on exit, success or failure, to never leave the
# working tree dirty.
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
