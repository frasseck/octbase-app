#!/usr/bin/env bash
#
# release.sh — release the current branch and redeploy the live demo.
#
# Steps (run from the octbase repo, i.e. frasseck/octbase.git):
#   1. Commit the release flow's own edits (CHANGELOG.md) on the current branch.
#   2. Push the current branch to origin and wait for CI on that exact commit.
#   3. Merge the CI-verified commit into main (--no-ff) and push main — done in
#      a TEMPORARY git worktree, so this (shared) checkout never switches
#      branches and concurrent sessions are never disturbed.
#   4. If a local demo checkout exists (DEMO_DIR), check out main, pull, and
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
#   scripts/release.sh --no-ci-gate           # merge without waiting for CI
#                                             # (discouraged; the default is to
#                                             # watch the branch's CI run via gh
#                                             # and abort the merge if it fails)
#
# Env:
#   DEMO_DIR   demo install path (default: /home/claude/demo.ocete.ch)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEMO_DIR="${DEMO_DIR:-/home/claude/demo.ocete.ch}"
MAIN_BRANCH="main"

# The files the release flow itself edits. Since 37b stage 5 the changelog
# release entry is the ONLY in-repo edit a release makes: the version stamp
# lives in the deployment .env / octbase-service ledger, and asset
# cache-busting is handled by the Vite build (see the release skill). Extend
# this list deliberately if the flow ever edits more files.
RELEASE_PATHS=( CHANGELOG.md )

MSG=""
ASSUME_YES=0
NO_CI_GATE=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    -m|--message) MSG="${2:-}"; shift 2 ;;
    -y|--yes) ASSUME_YES=1; shift ;;
    --no-ci-gate) NO_CI_GATE=1; shift ;;
    # Print the comment header (from line 2 up to the first non-comment line)
    # rather than a hard-coded line range, which drifted into the code below.
    -h|--help) awk 'NR>1 && !/^#/{exit} NR>1{sub(/^# ?/,""); print}' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

cd "$ROOT"

BRANCH="$(git rev-parse --abbrev-ref HEAD)"
if [[ "$BRANCH" == "$MAIN_BRANCH" ]]; then
  echo "Already on $MAIN_BRANCH; check out the feature/release branch first." >&2
  exit 1
fi

# Only the release-owned files count as "dirty": this checkout is shared by
# concurrent sessions, and a repo-wide dirtiness check would let THEIR
# in-progress edits gate — and previously enter — the release commit.
DIRTY=0
git diff --quiet -- "${RELEASE_PATHS[@]}" \
  && git diff --cached --quiet -- "${RELEASE_PATHS[@]}" || DIRTY=1
if [[ "$DIRTY" -eq 1 && -z "$MSG" ]]; then
  echo "Release files (${RELEASE_PATHS[*]}) have changes but no commit message given. Use -m \"message\"." >&2
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
  echo "==> committing release edits on $BRANCH (${RELEASE_PATHS[*]})"
  # This checkout is shared by concurrent sessions, so nothing may be staged
  # repo-wide: `git add -A` would sweep a neighbour's untracked scratch files
  # (or worse, secrets) into the release commit, and `git add -u` their
  # in-progress edits to tracked files. Committing with an explicit pathspec
  # records ONLY the release-owned files — and, because a pathspec commit
  # takes those paths' working-tree content directly, it also ignores
  # whatever a concurrent session may have left in the shared index.
  git commit -m "$MSG" -- "${RELEASE_PATHS[@]}"
else
  echo "==> release files unchanged; nothing to commit"
fi
if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "    note: unrelated working-tree changes exist (likely a concurrent session's) — they are NOT part of this release."
fi

echo "==> pushing $BRANCH"
git push -u origin "$BRANCH"

# CI gate: never merge a red — or unverified — branch into main. The push
# above triggers the CI workflow; wait for the run on exactly this commit and
# abort the merge if it fails. Without gh there is no way to check, so that
# also aborts unless --no-ci-gate says "merge unverified" out loud.
HEAD_SHA="$(git rev-parse HEAD)"
if [[ "$NO_CI_GATE" -eq 1 ]]; then
  echo "!! --no-ci-gate: merging $BRANCH into $MAIN_BRANCH WITHOUT a CI verdict."
elif command -v gh >/dev/null 2>&1; then
  echo "==> waiting for CI on $BRANCH @ ${HEAD_SHA:0:12}"
  RUN_ID=""
  for _ in $(seq 1 30); do   # a run usually appears within seconds; allow 5 min
    RUN_ID="$(gh run list --branch "$BRANCH" --commit "$HEAD_SHA" --limit 1 \
      --json databaseId --jq '.[0].databaseId' 2>/dev/null || true)"
    [[ -n "$RUN_ID" ]] && break
    sleep 10
  done
  if [[ -z "$RUN_ID" ]]; then
    echo "No CI run appeared for $HEAD_SHA within 5 minutes — NOT merging." >&2
    echo "Re-run once CI is visible, or pass --no-ci-gate to merge anyway (discouraged)." >&2
    exit 1
  fi
  if ! gh run watch "$RUN_ID" --exit-status; then
    echo "CI FAILED for $BRANCH @ $HEAD_SHA — NOT merging into $MAIN_BRANCH." >&2
    echo "Fix the branch (or, knowingly, pass --no-ci-gate) and re-run." >&2
    exit 1
  fi
  echo "==> CI green for $BRANCH @ ${HEAD_SHA:0:12}"
else
  echo "!! gh CLI not found — cannot verify CI for $BRANCH before merging." >&2
  echo "   Install gh, or re-run with --no-ci-gate to merge unverified (discouraged)." >&2
  exit 1
fi

echo "==> merging $BRANCH @ ${HEAD_SHA:0:12} into $MAIN_BRANCH (temporary worktree)"
# The merge must never run in THIS checkout: it is shared by concurrent
# sessions, and even a brief `git checkout main` here yanks every file out
# from under them. A disposable worktree gives the merge its own directory
# and index while the shared tree stays parked on $BRANCH throughout.
# Merging $HEAD_SHA — not the branch NAME — closes a time-of-check/time-of-use
# hole: CI was verified for exactly that commit above, and anything pushed to
# the branch since must not ride along unverified.
MERGE_WT="$(mktemp -d "${TMPDIR:-/tmp}/octbase-release-merge.XXXXXX")"
cleanup_merge_worktree() {
  # `worktree remove` unregisters AND deletes the directory; if it refuses
  # (e.g. a conflicted merge left the tree dirty), fall back to rm + prune so
  # no stale worktree registration lingers in the shared repo.
  git worktree remove --force "$MERGE_WT" >/dev/null 2>&1 \
    || { rm -rf "$MERGE_WT"; git worktree prune >/dev/null 2>&1 || true; }
}
# Remove the worktree on ANY exit — success, merge conflict, or a failing
# push under `set -e` — so an aborted release never strands a checkout.
trap cleanup_merge_worktree EXIT
git worktree add "$MERGE_WT" "$MAIN_BRANCH"
git -C "$MERGE_WT" pull --ff-only origin "$MAIN_BRANCH"
git -C "$MERGE_WT" merge --no-ff "$HEAD_SHA" -m "Merge $BRANCH into $MAIN_BRANCH"
git -C "$MERGE_WT" push origin "$MAIN_BRANCH"
cleanup_merge_worktree
trap - EXIT

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
