#!/usr/bin/env bash
#
# check-vendor-integrity.sh — CI integrity guard for vendored third-party JS.
# Fails if any vendored file's SHA-256 drifts from its pin in
# scripts/vendor-manifest.txt, and if a vendored file exists that the manifest
# does not pin (a new vendored file added without provenance). Offline: it never
# reaches the network — the manifest already records upstream provenance for a
# human to re-verify. See the manifest header for the re-pin procedure.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFEST="$ROOT/scripts/vendor-manifest.txt"
cd "$ROOT"

fail=0

# 1) Every pinned entry: the file exists and its SHA-256 matches the pin.
#    Enforced lines are `<sha256>  <path>`; comments/blanks are ignored.
pinned_paths=()
while read -r expected path; do
  [[ -z "${expected:-}" || "${expected:0:1}" == "#" ]] && continue
  pinned_paths+=("$path")
  if [[ ! -f "$path" ]]; then
    echo "MISSING: pinned vendored file not found: $path"
    fail=1
    continue
  fi
  actual="$(sha256sum "$path" | awk '{print $1}')"
  if [[ "$actual" != "$expected" ]]; then
    echo "DRIFT: $path"
    echo "  expected $expected"
    echo "  actual   $actual"
    fail=1
  fi
done < "$MANIFEST"

# 2) Coverage: every build-time vendored file under scripts/vendor/ must be
#    pinned, so a newly vendored file cannot slip in unpinned. (The two runtime
#    libs live in octbase-shared/ among first-party modules and are pinned by
#    explicit path above; scripts/vendor/ is exclusively vendored, so it is the
#    directory we can guard wholesale.)
is_pinned() { local p; for p in "${pinned_paths[@]}"; do [[ "$p" == "$1" ]] && return 0; done; return 1; }
shopt -s nullglob
for f in scripts/vendor/*.mjs scripts/vendor/*.js; do
  if ! is_pinned "$f"; then
    echo "UNPINNED: vendored file not in the manifest: $f"
    echo "  add it to scripts/vendor-manifest.txt with its upstream provenance and SHA-256"
    fail=1
  fi
done
shopt -u nullglob

if [[ $fail -ne 0 ]]; then
  echo "vendor-integrity guard: FAILED — see scripts/vendor-manifest.txt for the re-pin procedure" >&2
  exit 1
fi
echo "vendor-integrity guard: clean ✓"
