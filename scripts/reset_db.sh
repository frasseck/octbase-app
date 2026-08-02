#!/usr/bin/env bash
#
# reset_db.sh — wipe this install's Octbase database and repopulate it with the
# canonical demo seed content.
#
# What it does:
#   1. Ensures the Postgres container is up.
#   2. DROP + CREATE the database WITH (FORCE) — this terminates any live API
#      connections and throws away ALL data: both the seed rows and anything a
#      tester created. (FORCE needs Postgres >= 13; this stack runs 18.)
#   3. Restarts the API container. On startup the API re-runs every migration and,
#      because the stack runs with OCTBASE_DEMO_MODE=true, runs the deterministic
#      seed in octbase-api/internal/seed — recreating the demo users
#      (demo@octbase.dev / demo1234, super@octbase.dev / super1234), project,
#      board, tasks, wiki, repo, activity, etc.
#   4. Waits until /health reports ok at the freshly migrated version.
#
# The target is whatever podman-compose.yml + .env in THIS repo describe. With
# the checked-in dev .env that is the "octbase_dev" stack (API on :8001). It does
# NOT touch the separate live demo install in demo.ocete.ch.
#
# Usage:
#   scripts/reset_db.sh           # prompts before wiping
#   scripts/reset_db.sh --yes     # no prompt (for automation)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

ASSUME_YES=0
for arg in "$@"; do
  case "$arg" in
    -y|--yes) ASSUME_YES=1 ;;
    -h|--help) sed -n '2,30p' "$0"; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

# Pull a value out of the repo .env (which podman-compose also reads), falling
# back to the same defaults as podman-compose.yml.
envval() { grep -E "^$1=" "$ROOT/.env" 2>/dev/null | tail -1 | cut -d= -f2- | tr -d '"'; }
PGUSER="$(envval POSTGRES_USER)";   PGUSER="${PGUSER:-octbase}"
PGDB="$(envval POSTGRES_DB)";       PGDB="${PGDB:-octbase}"
API_PORT="$(envval API_PORT)";      API_PORT="${API_PORT:-8000}"
PROJECT="$(envval COMPOSE_PROJECT_NAME)"; PROJECT="${PROJECT:-$(basename "$ROOT")}"

echo "About to DESTROY and reseed database '$PGDB' for compose project '$PROJECT'."
echo "All data in that database will be lost and replaced with the demo seed."
if [[ "$ASSUME_YES" -ne 1 ]]; then
  read -r -p "Type 'yes' to continue: " reply
  [[ "$reply" == "yes" ]] || { echo "aborted."; exit 1; }
fi

echo "==> ensuring Postgres is up"
podman-compose up -d postgres >/dev/null

echo "==> waiting for Postgres to accept connections"
for i in $(seq 1 30); do
  if podman-compose exec -T postgres pg_isready -U "$PGUSER" >/dev/null 2>&1; then break; fi
  [[ $i -eq 30 ]] && { echo "Postgres did not become ready" >&2; exit 1; }
  sleep 1
done

echo "==> dropping and recreating database '$PGDB'"
podman-compose exec -T postgres psql -U "$PGUSER" -d postgres -v ON_ERROR_STOP=1 <<SQL
DROP DATABASE IF EXISTS "$PGDB" WITH (FORCE);
CREATE DATABASE "$PGDB" OWNER "$PGUSER";
SQL

echo "==> restarting API to re-run migrations + demo seed"
podman-compose restart octbase-api >/dev/null

echo "==> waiting for API /health on :$API_PORT"
for i in $(seq 1 60); do
  body="$(curl -fsS "http://localhost:${API_PORT}/health" 2>/dev/null || true)"
  if [[ "$body" == *'"status":"ok"'* ]]; then
    echo "    $body"
    echo "Database reset and reseeded ✓"
    exit 0
  fi
  sleep 1
done

echo "API did not report healthy in time; check 'podman-compose logs octbase-api'" >&2
exit 1
