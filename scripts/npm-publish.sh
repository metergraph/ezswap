#!/usr/bin/env bash
# Publishes the npm packages that scripts/npm-packages.mjs wrote to npm-out:
# the platform packages first, then the launcher that depends on them.
#
#   scripts/npm-publish.sh <dist-tag> [--dry-run]
#
# A version that is already on the registry is skipped, so a release that
# failed part way can be run again. With --dry-run nothing is uploaded; CI
# uses it to exercise the same commands the release runs.
set -euo pipefail

tag="${1:?usage: npm-publish.sh <dist-tag> [--dry-run]}"
dry_run="${2:-}"

publish() {
  local dir="$1" name version
  name="$(node -p "require('./$dir/package.json').name")"
  version="$(node -p "require('./$dir/package.json').version")"
  # The leading ./ matters: npm reads a bare "npm-out/name" as the GitHub
  # repository npm-out/name and tries to clone it.
  if [ "$dry_run" = "--dry-run" ]; then
    npm publish --dry-run --access public --tag "$tag" "./$dir"
    return
  fi
  if npm view "$name@$version" version >/dev/null 2>&1; then
    echo "$name@$version is already published, skipping"
    return
  fi
  npm publish --access public --provenance --tag "$tag" "./$dir"
}

for dir in npm-out/ezswap-*; do
  publish "$dir"
done
publish npm-out/ezswap
