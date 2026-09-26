#!/usr/bin/env bash
# check-mobile-bff-only.sh: CI net that needs no Gradle/JDK.
#
# The app's only legal HTTP surface is /api/mobile/v1/* through the generated
# client in :data:mobile-api-client. This enforces the same two violation classes
# as the Gradle lexical gate (BffOnlyNetworkPlugin), with grep/awk only:
#   1. a direct okhttp3./retrofit2. import outside the BFF
#   2. a string literal naming an /api/* route that does not start with
#      /api/mobile/v1 (and is not one of the WebSocket paths approved separately)
#
# NOT covered on purpose: routes built by concatenation/interpolation and fully
# qualified references without an import (`okhttp3.OkHttpClient()`). The
# type-resolving detekt rule (BffOnlyNetworkClientRule, android/build-logic/
# lint-rules) closes those.
#
# Scope: app modules (android/**/src/main|test/kotlin/**/*.kt), EXCLUDING
# android/data/** (the authorized :data + :data:mobile-api-client pair) and
# android/build-logic/** (it implements the gates; its docs quote the forbidden
# tokens as prose).
#
# Block comments /* ... */ (including KDoc) are blanked BEFORE matching, then whole
# `//` lines are skipped, so prose that only explains the rule is not a violation.
# Nested block comments are not handled; the authoritative gates do that.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

ALLOWED_API_PREFIX="/api/mobile/v1"
WS_EXEMPT_1="/ws/shell"
WS_EXEMPT_2="/ws/videocall"

# Removes (non-nested) /* ... */ blocks keeping line numbers, then drops whole
# `//` lines.
strip_comments() {
  awk '
    BEGIN { in_block = 0 }
    {
      line = $0
      out = ""
      while (length(line) > 0) {
        if (in_block) {
          pos = index(line, "*/")
          if (pos == 0) { line = ""; break }
          line = substr(line, pos + 2)
          in_block = 0
          continue
        }
        pos = index(line, "/*")
        if (pos == 0) { out = out line; line = ""; break }
        out = out substr(line, 1, pos - 1)
        line = substr(line, pos + 2)
        in_block = 1
      }
      trimmed = out
      sub(/^[ \t]+/, "", trimmed)
      if (index(trimmed, "//") == 1) { out = "" }
      print out
    }
  '
}

violations_file="$(mktemp)"
trap 'rm -f "$violations_file"' EXIT

while IFS= read -r -d '' kt_file; do
  code_only="$(strip_comments < "$kt_file")"

  line_no=0
  while IFS= read -r code_line; do
    line_no=$((line_no + 1))
    trimmed="$(printf '%s' "$code_line" | sed -e 's/^[[:space:]]*//')"

    case "$trimmed" in
      "import okhttp3."*|"import retrofit2."*)
        echo "$kt_file:$line_no: forbidden import outside the BFF -- $trimmed" >> "$violations_file"
        ;;
    esac

    while [[ "$code_line" =~ (/api/[A-Za-z0-9._/-]*) ]]; do
      match="${BASH_REMATCH[1]}"
      rest="${code_line#*"$match"}"
      code_line="$rest"
      if [[ "$match" == "$ALLOWED_API_PREFIX"* ]]; then
        continue
      fi
      if [[ "$match" == *"$WS_EXEMPT_1"* || "$match" == *"$WS_EXEMPT_2"* ]]; then
        continue
      fi
      echo "$kt_file:$line_no: route \"$match\" outside the mobile/v1 BFF" >> "$violations_file"
    done
  done <<< "$code_only"
done < <(find android \
  -path "android/data" -prune -o \
  -path "android/build-logic" -prune -o \
  -path "*/build/*" -prune -o \
  -type f -name "*.kt" -print0)

if [ -s "$violations_file" ]; then
  echo "check-mobile-bff-only: FAILED -- /api/* route outside the mobile/v1 BFF or direct okhttp3/retrofit2 import:" >&2
  cat "$violations_file" >&2
  exit 1
fi

echo "check-mobile-bff-only: OK -- no app module references okhttp3/retrofit2 or an /api/* route outside the mobile/v1 prefix"
