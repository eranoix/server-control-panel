#!/usr/bin/env bash
# release-git.sh: the git half of a release, shared by the publish scripts.
#
#   scripts/release-git.sh check           refuse unless on a clean main that
#                                          matches origin/main
#   scripts/release-git.sh publish X.Y.Z   push main, tag vX.Y.Z (annotated)
#                                          and create the GitHub Release
#
# SKIP_GIT_RELEASE=1 turns both into no-ops (used by the script tests).
set -euo pipefail

[ "${SKIP_GIT_RELEASE:-0}" = "1" ] && exit 0

cd "$(dirname "${BASH_SOURCE[0]}")/.."

fail() {
  echo "release: $*" >&2
  exit 1
}

check() {
  local branch
  branch="$(git rev-parse --abbrev-ref HEAD)"
  [ "$branch" = "main" ] || fail "publishing runs from main, not $branch"
  [ -z "$(git status --porcelain --untracked-files=no)" ] || fail "main has uncommitted changes"
  git fetch --quiet origin main || fail "could not fetch origin/main"
  [ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] \
    || fail "main differs from origin/main; pull or push before publishing"
}

publish() {
  local version="${1:?usage: release-git.sh publish X.Y.Z}"
  [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "version must be X.Y.Z, got $version"
  local tag="v$version" previous notes
  check
  git push --quiet origin main
  if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
    [ "$(git rev-parse "$tag^{commit}")" = "$(git rev-parse HEAD)" ] || fail "$tag already points at another commit"
  else
    git tag -a "$tag" -m "Release $version"
  fi
  git push --quiet origin "refs/tags/$tag"
  previous="$(git describe --tags --abbrev=0 "$tag^" 2>/dev/null || true)"
  notes="$(git log --format='- %s' ${previous:+"$previous.."}"$tag")"
  if gh release view "$tag" >/dev/null 2>&1; then
    echo "release: $tag already has a GitHub Release"
  else
    gh release create "$tag" --verify-tag --title "$version" --notes "$notes"
  fi
  echo "release: $tag pushed and released"
}

case "${1:-}" in
  check) check ;;
  publish) shift; publish "$@" ;;
  *) fail "usage: release-git.sh check | publish X.Y.Z" ;;
esac
