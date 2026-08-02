#!/usr/bin/env bash
#
# security-heavy.sh — the slower security/quality gate: static analysis,
# dependency vulnerability scan, and the test suite. Run by the pre-push hook
# (too slow for pre-commit) and safe to run by hand.
#
#   go vet          — compile-level correctness
#   golangci-lint   — lint incl. gosec-class rules (if installed)
#   govulncheck     — Go dependency CVEs with call-path reachability
#   go test         — the integration test suite (needs Postgres)
#
# Missing optional tooling / DB is a loud WARN-and-skip, not a hard block, so a
# push never wedges on a laptop without the full toolchain — CI is the real gate.
# A tool that IS present and FAILS blocks the push.
set -uo pipefail
root="$(git rev-parse --show-toplevel)"
cd "$root/octbase-api"

fail=0
run_hard() { printf '\033[36m▶ %s\033[0m\n' "$1"; shift; if "$@"; then printf '\033[32m✓ passed\033[0m\n\n'; else printf '\033[31m✗ FAILED\033[0m\n\n'; fail=1; fi; }
skip() { printf '\033[90m- skipped: %s\033[0m\n\n' "$1"; }

run_hard "go vet ./..." go vet ./...

if command -v golangci-lint >/dev/null 2>&1; then
  run_hard "golangci-lint run" golangci-lint run ./...
else
  skip "golangci-lint not installed (https://golangci-lint.run)"
fi

# Prefer an installed binary; else fall back to `go run` (needs network the first
# time). If neither can run (offline, no cache), warn-skip rather than block.
if command -v govulncheck >/dev/null 2>&1; then
  run_hard "govulncheck ./..." govulncheck ./...
elif go run golang.org/x/vuln/cmd/govulncheck@latest -version >/dev/null 2>&1; then
  run_hard "govulncheck ./... (via go run)" go run golang.org/x/vuln/cmd/govulncheck@latest ./...
else
  skip "govulncheck unavailable (install: go install golang.org/x/vuln/cmd/govulncheck@latest)"
fi

if [ -n "${TEST_DATABASE_URL:-}" ]; then
  run_hard "go test ./... (integration)" go test ./... -count=1
else
  skip "go test — TEST_DATABASE_URL unset (see the 'testing'/'dev-stack' skills)"
fi

if [ "$fail" -ne 0 ]; then
  printf '\033[31mHeavy checks FAILED — fix above, or `git push --no-verify` to override (discouraged).\033[0m\n'
  exit 1
fi
printf '\033[32mHeavy checks passed.\033[0m\n'
exit 0
