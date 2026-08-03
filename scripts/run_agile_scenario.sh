#!/usr/bin/env bash
#
# run_agile_scenario.sh — full, self-contained run of the Octbase agile
# end-to-end scenario test.
#
#   1. Resets + reseeds the database (scripts/reset_db.sh --yes) so the run
#      starts from the canonical demo seed.
#   2. Runs scripts/simulate_agile_project.py against the API.
#   3. Resets + reseeds the database again so the environment is left clean.
#
# The whole thing is wrapped in a 20-minute wall-clock timeout, per the test's
# contract. Exit code is non-zero if the scenario fails (the trailing reset
# still runs).
#
# Usage:
#   scripts/run_agile_scenario.sh           # prompts before wiping the database
#   scripts/run_agile_scenario.sh --yes     # no prompt (for automation)
#
# The API base is read from .env (API_PORT) like reset_db.sh does, and can be
# overridden with OCTBASE_API_BASE.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

ASSUME_YES=0
for arg in "$@"; do
  case "$arg" in
    -y|--yes) ASSUME_YES=1 ;;
    # Print the comment header (line 2 up to the first non-comment line).
    -h|--help) awk 'NR>1 && !/^#/{exit} NR>1{sub(/^# ?/,""); print}' "$0"; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

envval() { grep -E "^$1=" "$ROOT/.env" 2>/dev/null | tail -1 | cut -d= -f2- | tr -d '"'; }
API_PORT="$(envval API_PORT)"; API_PORT="${API_PORT:-8000}"
BASE="${OCTBASE_API_BASE:-http://127.0.0.1:${API_PORT}}"

# This run wipes the database TWICE (before and after the scenario) via
# reset_db.sh --yes — and on this host that database belongs to the live,
# SHARED dev stack, which concurrent sessions may be using right now. So the
# destruction must be acknowledged HERE, out loud, before the first reset;
# the --yes passed down to reset_db.sh only suppresses its inner re-prompt.
PGDB="$(envval POSTGRES_DB)";             PGDB="${PGDB:-octbase}"
PROJECT="$(envval COMPOSE_PROJECT_NAME)"; PROJECT="${PROJECT:-$(basename "$ROOT")}"
if [[ "$ASSUME_YES" -ne 1 ]]; then
  echo "About to DESTROY and reseed database '$PGDB' (compose project '$PROJECT') TWICE:"
  echo "once before and once after the scenario. All current data in it — including"
  echo "anything a concurrent session is working with — will be lost both times."
  read -r -p "Type 'yes' to continue (or re-run with --yes): " reply
  [[ "$reply" == "yes" ]] || { echo "aborted."; exit 1; }
fi

# Pick a Python that has `requests` (prefer the frontend test venv).
PY="python3"
if [[ -x "$ROOT/octbase-frontend/tests/.venv/bin/python" ]] \
   && "$ROOT/octbase-frontend/tests/.venv/bin/python" -c 'import requests' 2>/dev/null; then
  PY="$ROOT/octbase-frontend/tests/.venv/bin/python"
fi

# Overall 20-minute budget for the entire run (reset + test + reset). Floored
# at 1s so a slow first reset can't leave the later timeout calls disabled
# (some `timeout` implementations treat 0/negative as "no limit").
DEADLINE=$(( $(date +%s) + 1200 ))
remaining() { local r=$(( DEADLINE - $(date +%s) )); (( r < 1 )) && r=1; echo "$r"; }

reset() {
  echo "==> resetting database ($1)"
  timeout "$(remaining)" "$ROOT/scripts/reset_db.sh" --yes
}

echo "### Octbase agile end-to-end scenario"
echo "### API base: $BASE   Python: $PY"

reset "before" || { echo "pre-run reset failed"; exit 2; }

echo "==> running scenario against $BASE"
timeout "$(remaining)" "$PY" "$ROOT/scripts/simulate_agile_project.py" --base "$BASE"
rc=$?

reset "after" || echo "WARNING: post-run reset failed; environment may be dirty"

if [[ $rc -eq 124 ]]; then
  echo "### FAILED: scenario exceeded the 20-minute budget"
elif [[ $rc -ne 0 ]]; then
  echo "### FAILED: scenario exited with code $rc"
else
  echo "### PASSED: scenario completed and database reset"
fi
exit $rc
