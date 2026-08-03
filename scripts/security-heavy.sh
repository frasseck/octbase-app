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
# -e so an environment error aborts loudly; the check runners themselves are
# if-guarded (`run_hard`, the command -v probes), so a FAILING check still
# reports and blocks via $fail instead of dying mid-run.
set -euo pipefail
# Outside a git repo, rev-parse prints nothing — the cd would then resolve
# against the cwd. Fail with a message naming the actual problem instead.
root="$(git rev-parse --show-toplevel 2>/dev/null)" \
  || { echo "security-heavy.sh: not inside a git repository" >&2; exit 1; }
cd "$root/octbase-api" \
  || { echo "security-heavy.sh: $root/octbase-api not found — wrong repo?" >&2; exit 1; }

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
# The fallback is PINNED, not @latest: a push-time hook must not fetch and run
# whatever x/vuln published a minute ago. Bump the pin deliberately (check
# https://pkg.go.dev/golang.org/x/vuln — v1.6.0 was current 2026-08).
GOVULNCHECK_VERSION=v1.6.0
if command -v govulncheck >/dev/null 2>&1; then
  run_hard "govulncheck ./..." govulncheck ./...
elif go run "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}" -version >/dev/null 2>&1; then
  run_hard "govulncheck ./... (via go run, ${GOVULNCHECK_VERSION})" go run "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}" ./...
else
  skip "govulncheck unavailable (install: go install golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION})"
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
