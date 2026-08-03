#!/usr/bin/env bash
#
# security-sweep.sh — fast, deterministic security regression sweep.
#
# This is the "scan before commit" fast path: it runs the zero-false-positive
# guards and greps from the go-security / js-security skills. It needs NO running
# stack, NO database, NO network — everything here is a static check that returns
# in ~1-2s, so it is safe to run from the pre-commit hook on every commit.
#
# The DEEP assessment (prompts/06_security-assessment.md) is a separate, LLM-driven
# pentest — it does NOT run here and is not a pre-commit thing.
#
# Design rule: everything in the HARD section must be genuinely zero-hit on a
# clean tree. A guard that false-positives just teaches people to `--no-verify`.
# Ambiguous patterns (known-safe hits exist) go in the SOFT section as warnings.
#
# Usage: scripts/security-sweep.sh          # run the sweep
# Exit:  0 = clean, 1 = a HARD check failed (blocks the commit).
# -e so an unexpected error (a missing tool, an unreadable tree) aborts the
# sweep loudly instead of degrading into a partial — and green-looking — run.
# Every grep whose no-match exit is a PASS is already `|| true`-guarded (see
# grep_forbid) or sits in an if/&&-|| context that -e ignores.
set -euo pipefail

# Outside a git repo, rev-parse prints nothing — an empty $root would make the
# `cd` below a no-op and the sweep would silently scan whatever the cwd is.
root="$(git rev-parse --show-toplevel 2>/dev/null)" \
  || { echo "security-sweep.sh: not inside a git repository — refusing to scan the cwd" >&2; exit 1; }
cd "$root"

fail=0
hard() { printf '  \033[31m✗ %s\033[0m\n' "$1"; fail=1; }
warn() { printf '  \033[33m! %s\033[0m\n' "$1"; }
skip() { printf '  \033[90m- skipped: %s\033[0m\n' "$1"; }
ok()   { printf '  \033[32m✓ %s\033[0m\n' "$1"; }

# grep_forbid <label> <cmd...>  — HARD-fail if the pipeline prints anything.
# NOTE: the `|| true` swallows grep's exit code on purpose (grep exits 1 on
# no-match, which is the PASS case here) — which also means a grep pointed at a
# path that no longer exists would silently "pass". The swept-paths check below
# closes that hole; keep the two lists in sync when a grep gains a new target.
grep_forbid() {
  local label="$1"; shift
  local hits; hits="$("$@" 2>/dev/null)" || true
  if [ -n "$hits" ]; then
    hard "$label"
    printf '%s\n' "$hits" | sed 's/^/      /'
  else
    ok "$label"
  fi
}

echo "── sweep-target existence (guard the guards) ──────────────"
# Every path (or glob) the checks below scan. If one goes missing — a rename,
# a directory restructure — the greps would exit 2, grep_forbid's `|| true`
# would eat it, and the whole sweep would go green while scanning nothing.
# Fail loudly instead, naming the path, the same way
# scripts/check-metrics-not-proxied.sh fails on a missing listed config.
swept_paths=(
  octbase-frontend/js
  octbase-mobile/js
  octbase-shared
  "octbase-frontend/*.html"
  "octbase-mobile/*.html"
  octbase-api/internal
  octbase-api/cmd
  octbase-api/internal/workmanagement/attachment_handler.go
  scripts/check-innerhtml.mjs
  scripts/check-tdz.mjs
  scripts/check-vendor-integrity.sh
)
paths_ok=1
for p in "${swept_paths[@]}"; do
  # compgen -G handles both literal paths and globs (a glob that matches
  # nothing would reach grep as a literal, nonexistent filename).
  if ! compgen -G "$p" >/dev/null; then
    hard "sweep target missing: $p — a rename/move here makes every grep on it vacuously pass; update swept_paths and the greps together"
    paths_ok=0
  fi
done
# (a full if — a bare `[ … ] && ok` would return 1 and trip `set -e` when a
# path is missing, aborting before the failure summary prints)
if [ "$paths_ok" -eq 1 ]; then ok "all swept paths exist"; fi

echo "── frontend integrity guards ──────────────────────────────"
if command -v node >/dev/null 2>&1; then
  if node scripts/check-innerhtml.mjs >/dev/null 2>&1; then
    ok "innerHTML escaping guard"
  else
    hard "innerHTML escaping guard (run: node scripts/check-innerhtml.mjs)"
  fi
  syntax_bad=""
  for f in octbase-frontend/js/*.js octbase-mobile/js/*.js octbase-shared/*.js; do
    [ -f "$f" ] || continue
    node --check "$f" >/dev/null 2>&1 || syntax_bad="$syntax_bad $f"
  done
  [ -z "$syntax_bad" ] && ok "JS syntax" || hard "JS syntax:$syntax_bad"
  # The export-completeness guard retired with the ESM conversion (37b stage 2):
  # an unresolved import is now a build error, and CI owns that (a build is too
  # slow for a pre-commit sweep). The TDZ guard is not covered by the build at
  # all and is fast, so it runs here.
  if node scripts/check-tdz.mjs >/dev/null 2>&1; then
    ok "module TDZ hazards (load-time reads across import cycles)"
  else
    hard "module TDZ hazards (run: node scripts/check-tdz.mjs)"
  fi
else
  skip "node not found — innerHTML + JS syntax guards not run (CI still enforces)"
fi
# The shared-module drift check retired at 37b stage 3: octbase-shared/ is the
# @octbase/shared package both SPAs import, so there is one copy and nothing to
# drift. What replaces it is checking that neither SPA has grown a LOCAL
# re-implementation of a shared module — the failure the drift guard actually
# existed to catch (the pre-DOMPurify mobile sanitizer).
grep_forbid "no local re-implementation of a shared module" \
  bash -c 'grep -rnE "function (sanitizeRichText|rtSafeHref|rtSafeImageSrc|looksLikeHTML)\b" octbase-frontend/js octbase-mobile/js --include="*.js" | grep -v test'
if bash scripts/check-vendor-integrity.sh >/dev/null 2>&1; then
  ok "vendored-dependency integrity (SHA-256 pins in scripts/vendor-manifest.txt)"
else
  hard "vendored-dependency integrity (run: bash scripts/check-vendor-integrity.sh)"
fi

echo "── backend security greps (octbase-api) ───────────────────"
grep_forbid "no math/rand for security material" \
  bash -c 'grep -rn "\"math/rand\"" --include="*.go" octbase-api/internal octbase-api/cmd | grep -v _test.go'
grep_forbid "no InsecureSkipVerify (TLS verification)" \
  bash -c 'grep -rn "InsecureSkipVerify" --include="*.go" octbase-api'
grep_forbid "no os/exec or text/template (cmd-exec / unsafe HTML templating)" \
  bash -c 'grep -rn "os/exec\|text/template" --include="*.go" octbase-api/internal octbase-api/cmd | grep -v _test.go'
grep_forbid "X-Forwarded-For read only via shared.RealIP" \
  bash -c 'grep -rnE "Get\(\"X-(Forwarded-For|Real-IP)\"\)|Header\[\"X-(Forwarded-For|Real-IP)\"\]" --include="*.go" octbase-api/internal octbase-api/cmd | grep -v _test.go | grep -v "shared/realip.go"'
# Presence check: SVG upload rejection must stay in place.
if grep -q 'image/svg' octbase-api/internal/workmanagement/attachment_handler.go 2>/dev/null; then
  ok "SVG upload rejection present"
else
  hard "SVG upload rejection missing in attachment_handler.go"
fi

echo "── frontend security greps ────────────────────────────────"
grep_forbid "no eval / new Function / document.write" \
  bash -c 'grep -rn "eval(\|new Function\|document\.write" octbase-frontend/js octbase-mobile/js octbase-shared --include="*.js" | grep -v test'
# Any <script…> tag WITHOUT a src= attribute is inline, wherever it sits on the
# line and whatever attributes it carries (<script type="module"> included) —
# the old anchor "^<script>" missed indented and attributed tags entirely.
grep_forbid "no inline <script> in SPA HTML (edge CSP is script-src '\''self'\'')" \
  bash -c 'grep -rnE "^[[:space:]]*<script[^>]*>" octbase-frontend/*.html octbase-mobile/*.html | grep -vE "<script[^>]*[[:space:]]src[[:space:]]*="'
grep_forbid "no ?token= outside realtime.js (SSE is the only token-in-URL)" \
  bash -c 'grep -rn "?token=" octbase-frontend/js octbase-mobile/js --include="*.js" | grep -v realtime'
grep_forbid "shared modules stay sink-free (innerHTML only in richtext)" \
  bash -c 'grep -rn "innerHTML" octbase-shared --include="*.js" | grep -v "richtext"'
grep_forbid "no secrets in localStorage/sessionStorage" \
  bash -c 'grep -rniE "(localStorage|sessionStorage)\.setItem\([^)]*(token|jwt|secret|password|auth)" octbase-frontend/js octbase-mobile/js --include="*.js"'

echo "── go formatting ──────────────────────────────────────────"
if command -v gofmt >/dev/null 2>&1; then
  # `|| true`: gofmt -l exits non-zero on an unparsable file; the sweep must
  # still report (go vet / the build own syntax errors), not abort under -e.
  unformatted="$(gofmt -l octbase-api 2>/dev/null || true)"
  [ -z "$unformatted" ] && ok "gofmt" || hard "gofmt (run: gofmt -w octbase-api):"$'\n'"$(printf '%s' "$unformatted" | sed 's/^/      /')"
else
  skip "gofmt not found"
fi

echo "── informational (not blocking — eyeball on review) ───────"
# `|| true` on the captures and full `if`s on the reports: a no-match grep
# (the CLEAN case) exits 1, which under -e/pipefail would abort the sweep.
sql_sprintf="$(grep -rn 'fmt.Sprintf' --include='*.go' octbase-api/internal octbase-api/cmd 2>/dev/null | grep -iE 'select|insert|update|delete|where' | grep -v _test.go || true)"
if [ -n "$sql_sprintf" ]; then warn "fmt.Sprintf near SQL keywords — confirm whitelist/placeholder-only, never a value:"; printf '%s\n' "$sql_sprintf" | sed 's/^/      /'; fi
blank_noopener="$(grep -rn '_blank' octbase-frontend/js octbase-mobile/js --include='*.js' 2>/dev/null | grep -v noopener || true)"
if [ -n "$blank_noopener" ]; then warn "target=_blank without noopener on the same line — confirm rel is set (blob viewer / richtext hook are known-safe):"; printf '%s\n' "$blank_noopener" | sed 's/^/      /'; fi

echo "───────────────────────────────────────────────────────────"
if [ "$fail" -ne 0 ]; then
  printf '\033[31mSecurity sweep FAILED — fix the ✗ items above, or `git commit --no-verify` to override (discouraged).\033[0m\n'
  exit 1
fi
printf '\033[32mSecurity sweep passed.\033[0m\n'
exit 0
