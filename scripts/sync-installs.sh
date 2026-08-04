#!/usr/bin/env bash
#
# sync-installs.sh — file-level sync of the Octbase application source between
# the two side-by-side installs on this host:
#
#     SRC (source of truth)  dev.ocete.ch/    (this repo, release branch)
#     DST (deploy target)    demo.ocete.ch/   (this repo, main)
#
# NOTE (2026-07-13): the public demo no longer lives at ~/demo.ocete.ch — it
# migrated to its own oct-demo account, managed by octbase-service (deploy via
# sync-instance.yml from the admin machine; see the release skill). The default
# DST is therefore gone and this script errors unless you point SRC/DST at two
# existing installs yourself. It remains an escape hatch for rsyncing a working
# tree between local checkouts (git metadata never crosses).
#
# Safety model:
#   * DRY-RUN by default. Nothing is written until you pass --apply.
#   * --delete is OFF by default, so files that exist only in DST (e.g. demo-only
#     config) are preserved. Pass --delete to mirror exactly.
#   * Per-install state is NEVER synced: .git, .env / credentials, Postgres data
#     volumes, caches, build artifacts, screenshots. See EXCLUDES below.
#   * After a successful --apply the DST containers are rebuilt & restarted
#     (podman-compose up -d --build) so the synced source goes live. The frontend
#     images COPY+minify their sources, so a plain restart is not enough — a
#     rebuild is required. Pass --no-reload to skip this.
#
# Usage:
#   scripts/sync-installs.sh                 # dry-run, dev.ocete.ch -> demo.ocete.ch
#   scripts/sync-installs.sh --apply         # copy, then rebuild+restart DST containers
#   scripts/sync-installs.sh --apply --no-reload  # copy only, leave containers as-is
#   scripts/sync-installs.sh --apply --delete   # mirror (also remove DST-only files)
#   scripts/sync-installs.sh --reverse       # dry-run, demo.ocete.ch -> dev.ocete.ch
#   SRC=/path/a DST=/path/b scripts/sync-installs.sh --apply   # override paths
#
set -euo pipefail

# --- resolve default paths (the two installs live next to each other) ----------
# Default SRC is the repo this script lives in; DST is its sibling demo install.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEFAULT_SRC="$(cd "$SCRIPT_DIR/.." && pwd)"
DEFAULT_DST="$(cd "$DEFAULT_SRC/.." && pwd)/demo.ocete.ch"

SRC="${SRC:-$DEFAULT_SRC}"
DST="${DST:-$DEFAULT_DST}"

APPLY=0
DELETE=0
REVERSE=0
RELOAD=1
for arg in "$@"; do
  case "$arg" in
    --apply)     APPLY=1 ;;
    --delete)    DELETE=1 ;;
    --reverse)   REVERSE=1 ;;
    --no-reload) RELOAD=0 ;;
    # Print the comment header only (line 2 up to the first non-comment line) —
    # a plain `grep '^#'` would also dump every section-divider comment below.
    -h|--help) awk 'NR>1 && !/^#/{exit} NR>1{sub(/^# ?/,""); print}' "$0"; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

if [[ "$REVERSE" == 1 ]]; then
  tmp="$SRC"; SRC="$DST"; DST="$tmp"
fi

# rsync needs trailing slashes to copy *contents* of SRC into DST.
SRC="${SRC%/}/"
DST="${DST%/}/"

for d in "$SRC" "$DST"; do
  if [[ ! -d "$d" ]]; then echo "not a directory: $d" >&2; exit 1; fi
done

# --- things that must NEVER cross between installs ------------------------------
# Per-install identity, secrets, live data, and regenerable junk.
EXCLUDES=(
  # git metadata — different remotes/branches per install
  ".git/"
  # secrets — .env is a symlink to a per-install credentials file
  ".env" ".env.local" ".env.*.local"
  "credentials/"
  # Postgres bind-mount volumes (owned by the rootless podman uid; live data)
  "pgdata/" "pgdata_dev/" "pgdata*/"
  # python / playwright test env + caches
  ".venv/" ".venv-octbase-tests/" "__pycache__/" "*.py[cod]" ".pytest_cache/"
  # npm dependency trees — host-specific (native binaries, platform-keyed
  # optional deps); each install must run its own `npm ci`. Also keeps a
  # --reverse --apply --delete from rewriting this tree's node_modules.
  "node_modules/"
  # go build output
  "octbase-api/octbase-api" "octbase-api/octbase.db" "octbase-api/vendor/"
  "cover.out" "cover_wm.out" "coverage.txt" "*.cover.html"
  # editor / OS
  ".vscode/" ".DS_Store" "*.swp" "*.swo"
  # local-only Claude harness settings (machine/path specific) — at any depth
  "settings.local.json"
  # working artifacts that don't belong in the deployed install
  "nohup.out" "*.png"
)

RSYNC_OPTS=( -a --human-readable --itemize-changes )
[[ "$DELETE" == 1 ]] && RSYNC_OPTS+=( --delete )
[[ "$APPLY"  == 1 ]] || RSYNC_OPTS+=( --dry-run )
for e in "${EXCLUDES[@]}"; do RSYNC_OPTS+=( --exclude "$e" ); done

# --- report & run --------------------------------------------------------------
echo "SRC : $SRC"
echo "DST : $DST"
echo "mode: $([[ $APPLY == 1 ]] && echo APPLY || echo DRY-RUN)$([[ $DELETE == 1 ]] && echo ' +delete')"
echo "------------------------------------------------------------------------"

if [[ "$APPLY" == 1 ]]; then
  # Show what would change BEFORE asking — a confirmation over an empty screen
  # ("above" referred to nothing) is no confirmation at all. The preview is the
  # same rsync with --dry-run forced on; the answer then gates the real run.
  echo "Preview (dry-run) of the changes --apply will write:"
  rsync "${RSYNC_OPTS[@]}" --dry-run "$SRC" "$DST"
  echo "------------------------------------------------------------------------"
  read -r -p "Write changes to DST above? [y/N] " ans
  [[ "$ans" == [yY] ]] || { echo "aborted."; exit 1; }
fi

rsync "${RSYNC_OPTS[@]}" "$SRC" "$DST"

echo "------------------------------------------------------------------------"
if [[ "$APPLY" != 1 ]]; then
  echo "DRY-RUN only — re-run with --apply to write the changes listed above."
  exit 0
fi

echo "Sync complete."

# --- reload the DST containers so the synced source goes live ------------------
# The frontend images COPY+minify their sources at build time, so a plain restart
# would keep serving the old assets — rebuild is required.
if [[ "$RELOAD" != 1 ]]; then
  echo "Skipping container reload (--no-reload). To deploy manually:"
  echo "  cd \"${DST%/}\" && podman-compose up -d --build"
else
  echo "Reloading DST containers (podman-compose up -d --build) ..."
  ( cd "${DST%/}" && podman-compose up -d --build )
  echo "Containers reloaded."
fi

echo "Reminders for the deploy target ($DST):"
echo "  * review 'git status' — DST is a separate repo; commit deliberately"
echo "  * each install keeps its own .env / credentials and Postgres volume"
