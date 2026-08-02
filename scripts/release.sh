#!/usr/bin/env bash
#
# release.sh — release the current branch and redeploy the live demo.
#
# Steps (run from the octbase repo, i.e. frasseck/octbase.git):
#   1. Commit any working-tree changes on the current branch.
#   2. Push the current branch to origin.
#   3. Merge the current branch into main (--no-ff) and push main.
#   4. Switch back to the original branch.
#   5. If a local demo checkout exists (DEMO_DIR), check out main, pull, and
#      rebuild its containers (podman-compose up -d --build).
#
# Since 2026-07-13 the public demo is platform-managed under its own
# oct-demo account (see octbase-service, migrate-instance) — there is no
# local demo checkout on this host anymore. Step 5 is skipped with a notice;
# deploy the demo from the ADMIN machine instead:
#   ansible-playbook playbooks/sync-instance.yml -e client=demo
# (and stamp the version via the ledger's app_version + create-instance.yml).
#
# Usage:
#   scripts/release.sh -m "commit message"   # commit + release + deploy
#   scripts/release.sh                        # error if there are uncommitted changes
#   scripts/release.sh -m "msg" --yes         # skip the confirmation prompt
#
# Env:
#   DEMO_DIR   demo install path (default: /home/claude/demo.ocete.ch)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEMO_DIR="${DEMO_DIR:-/home/claude/demo.ocete.ch}"
MAIN_BRANCH="main"

MSG=""
ASSUME_YES=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    -m|--message) MSG="${2:-}"; shift 2 ;;
    -y|--yes) ASSUME_YES=1; shift ;;
    -h|--help) sed -n '2,30p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

cd "$ROOT"

BRANCH="$(git rev-parse --abbrev-ref HEAD)"
if [[ "$BRANCH" == "$MAIN_BRANCH" ]]; then
  echo "Already on $MAIN_BRANCH; check out the feature/release branch first." >&2
  exit 1
fi

DIRTY=0
git diff --quiet && git diff --cached --quiet || DIRTY=1
if [[ "$DIRTY" -eq 1 && -z "$MSG" ]]; then
  echo "Working tree has changes but no commit message given. Use -m \"message\"." >&2
  exit 1
fi

echo "About to:"
echo "  * commit + push '$BRANCH' to origin (octbase.git)"
echo "  * merge '$BRANCH' into '$MAIN_BRANCH' and push"
if [[ -d "$DEMO_DIR/.git" ]]; then
  echo "  * deploy demo: git pull main + rebuild containers in $DEMO_DIR"
else
  echo "  * (no local demo checkout at $DEMO_DIR — demo deploy happens via octbase-service)"
fi
if [[ "$ASSUME_YES" -ne 1 ]]; then
  read -r -p "Type 'yes' to continue: " reply
  [[ "$reply" == "yes" ]] || { echo "aborted."; exit 1; }
fi

if [[ "$DIRTY" -eq 1 ]]; then
  echo "==> committing changes on $BRANCH"
  git add -A
  git commit -m "$MSG"
else
  echo "==> working tree clean; nothing to commit"
fi

echo "==> pushing $BRANCH"
git push -u origin "$BRANCH"

echo "==> merging $BRANCH into $MAIN_BRANCH"
git checkout "$MAIN_BRANCH"
git pull --ff-only origin "$MAIN_BRANCH"
git merge --no-ff "$BRANCH" -m "Merge $BRANCH into $MAIN_BRANCH"
git push origin "$MAIN_BRANCH"

echo "==> returning to $BRANCH"
git checkout "$BRANCH"

if [[ ! -d "$DEMO_DIR/.git" ]]; then
  cat <<EOF
==> no local demo checkout at $DEMO_DIR — skipping demo deploy.
    The demo is platform-managed (oct-demo account). From the ADMIN machine:
      ansible-playbook playbooks/sync-instance.yml -e client=demo
    To stamp the released version: set app_version in
    octbase-service/ledger/clients/demo.yml and run create-instance.yml.
Release complete ✓ (demo deploy pending on the admin machine)
EOF
  exit 0
fi

echo "==> deploying demo in $DEMO_DIR"
git -C "$DEMO_DIR" fetch origin
git -C "$DEMO_DIR" checkout "$MAIN_BRANCH"
git -C "$DEMO_DIR" pull --ff-only origin "$MAIN_BRANCH"

echo "==> rebuilding demo containers"
( cd "$DEMO_DIR" && podman-compose up -d --build )

echo "Release + demo deploy complete ✓"
