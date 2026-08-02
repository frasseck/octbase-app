#!/usr/bin/env bash
#
# setup-git.sh — one-time per-clone git config for this repo.
#
# Hooks can't live in a commit, so every clone must run this once to point git
# at the tracked ones. Safe to re-run.
#
# Until 37b stage 5 this also registered a `stamphtml` merge driver, because the
# SPA index.html files carried derived `?v=<content-hash>` asset queries that
# conflicted spuriously whenever two branches touched the same asset. Vite
# content-hashes the filenames now (scripts/vite-hash-classic-assets.mjs), so
# index.html holds no generated content at all and there is nothing left for a
# merge driver to reconcile.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

# pre-commit: the fast, deterministic security sweep.
# pre-push: the slower gate (go vet, golangci-lint, govulncheck, go test).
git config core.hooksPath scripts/git-hooks

echo "Installed git-hooks (pre-commit security sweep, pre-push gate)."
