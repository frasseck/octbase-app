#!/usr/bin/env bash
#
# check-health.sh — Octbase stack health observer.
#
# Probes every container in an Octbase podman-compose deployment at two layers:
#
#   1. Container layer — is the container running, what is its podman healthcheck
#      status (where one is defined), and how often has it restarted?
#   2. Application layer — does the service actually answer? The API exposes a
#      rich JSON /health (db ping + migration version); the Caddy frontends serve
#      HTTP; Postgres answers pg_isready.
#
# A service is only GREEN when BOTH layers pass. A container that is "Up" but
# whose app returns 503 (e.g. API can't reach the DB, or migrations are behind)
# is reported DEGRADED, not OK — that is the whole point of the app layer.
#
# Usage:
#   ./check-health.sh [--project NAME] [--json] [--quiet] [--no-deep]
#
#   --project NAME   compose project prefix (default: $OCTBASE_PROJECT or "octbase").
#                    Containers are <project>_<service>_1. "octbase" is the
#                    project name on the platform-managed client accounts (the
#                    fleet monitor passes --project octbase explicitly); under
#                    the dev account only "octbase_dev" runs, so pass
#                    --project octbase_dev there — a bare run finds no containers.
#   --json           emit a single machine-readable JSON object (for monitors/cron).
#   --quiet          only print the final summary line.
#   --no-deep        skip exec-based probes (Postgres pg_isready);
#                    container-state only for those. Use where `podman exec` is denied.
#
# Exit codes (so cron / monitoring can branch on them):
#   0  all services OK
#   1  at least one service DEGRADED (up but unhealthy / behind)
#   2  at least one service DOWN (container missing or not running)
#   3  usage / environment error (e.g. podman not found)
#
set -uo pipefail

# ── Configuration ────────────────────────────────────────────────────────────
PROJECT="${OCTBASE_PROJECT:-octbase}"
JSON=0
QUIET=0
DEEP=1

while [ $# -gt 0 ]; do
  case "$1" in
    --project) PROJECT="${2:?--project needs a value}"; shift 2 ;;
    --project=*) PROJECT="${1#*=}"; shift ;;
    --json) JSON=1; shift ;;
    --quiet) QUIET=1; shift ;;
    --no-deep) DEEP=0; shift ;;
    # Print the comment header (from line 2 up to the first non-comment line)
    # rather than a hard-coded line range, which drifted into the code below.
    -h|--help) awk 'NR>1 && !/^#/{exit} NR>1{sub(/^# ?/,""); print}' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 3 ;;
  esac
done

command -v podman >/dev/null 2>&1 || { echo "podman not found on PATH" >&2; exit 3; }
HAVE_CURL=0; command -v curl >/dev/null 2>&1 && HAVE_CURL=1

# Restart-count threshold above which a running container is flagged DEGRADED
# (a healthy long-lived service should not be flapping).
RESTART_WARN="${OCTBASE_RESTART_WARN:-5}"

# ── Output helpers ───────────────────────────────────────────────────────────
if [ -t 1 ] && [ "$JSON" -eq 0 ]; then
  C_OK=$'\033[32m'; C_WARN=$'\033[33m'; C_BAD=$'\033[31m'; C_DIM=$'\033[2m'; C_RST=$'\033[0m'
else
  C_OK=''; C_WARN=''; C_BAD=''; C_DIM=''; C_RST=''
fi

# Collected results, one line per service: "name|state|detail"
# state ∈ OK | DEGRADED | DOWN
RESULTS=()
record() { RESULTS+=("$1|$2|$3"); }

# ── Probe primitives ─────────────────────────────────────────────────────────

# container_state <container> -> echoes "running|<health>|<restarts>" or "missing||"
container_state() {
  local c="$1"
  podman inspect "$c" \
    --format '{{.State.Status}}|{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}|{{.RestartCount}}' \
    2>/dev/null || echo "missing||"
}

# Resolve a container's published host endpoint for an internal port.
# Echoes "host:port" or empty if the port isn't published.
host_endpoint() {
  local c="$1" port="$2" mapped
  mapped="$(podman port "$c" "${port}/tcp" 2>/dev/null | head -n1)" || return 0
  [ -z "$mapped" ] && return 0
  # mapped looks like "0.0.0.0:8001" or "[::]:8001"; normalise the host.
  local hp="${mapped##*:}"
  echo "127.0.0.1:${hp}"
}

# http_probe <url> [expect_body_substr] -> echoes "<http_code>|<body_first_line>"
http_probe() {
  local url="$1" body code
  [ "$HAVE_CURL" -eq 1 ] || { echo "000|curl-missing"; return; }
  body="$(curl -s -S --max-time 5 -w $'\n%{http_code}' "$url" 2>/dev/null)" || { echo "000|unreachable"; return; }
  code="${body##*$'\n'}"
  body="${body%$'\n'*}"
  echo "${code}|$(printf '%s' "$body" | head -c 200 | tr '\n' ' ')"
}

# exec_probe <container> <cmd...> -> runs inside the container; echoes combined output, sets $?
exec_probe() { podman exec "$1" "${@:2}" 2>&1; }

# ── Service checks ───────────────────────────────────────────────────────────
# Each check resolves the container, evaluates the container layer, then the app
# layer, and records the worse of the two states.

worse() { # echo the more severe of two states
  case "$1$2" in
    *DOWN*) echo DOWN ;;
    *DEGRADED*) echo DEGRADED ;;
    *) echo OK ;;
  esac
}

check_container_layer() { # <container> -> echoes "STATE|detail"; returns container running?
  local cs; cs="$(container_state "$1")"
  local status="${cs%%|*}"
  local rest="${cs#*|}"
  local health="${rest%%|*}"
  local restarts="${rest#*|}"
  if [ "$status" != "running" ]; then
    echo "DOWN|container ${status:-missing}"
    return 1
  fi
  local detail="up"
  local state="OK"
  if [ "$health" != "none" ] && [ "$health" != "healthy" ] && [ -n "$health" ]; then
    state="DEGRADED"; detail="healthcheck=$health"
  fi
  if [ -n "$restarts" ] && [ "$restarts" -ge "$RESTART_WARN" ] 2>/dev/null; then
    state="$(worse "$state" DEGRADED)"; detail="$detail restarts=$restarts"
  fi
  echo "${state}|${detail}"
  return 0
}

check_postgres() {
  local c="${PROJECT}_postgres_1" cl
  cl="$(check_container_layer "$c")" || { record postgres DOWN "${cl#*|}"; return; }
  local state="${cl%%|*}" detail="${cl#*|}"
  if [ "$DEEP" -eq 1 ]; then
    if out="$(exec_probe "$c" pg_isready -U "${POSTGRES_USER:-octbase}")" && echo "$out" | grep -q "accepting connections"; then
      detail="$detail; pg_isready ok"
    else
      state="$(worse "$state" DEGRADED)"; detail="$detail; pg_isready FAILED"
    fi
  fi
  record postgres "$state" "$detail"
}

check_api() {
  local c="${PROJECT}_octbase-api_1" cl
  cl="$(check_container_layer "$c")" || { record api DOWN "${cl#*|}"; return; }
  local state="${cl%%|*}" detail="${cl#*|}"
  local ep; ep="$(host_endpoint "$c" 8000)"
  if [ -n "$ep" ]; then
    local r code body; r="$(http_probe "http://${ep}/health")"; code="${r%%|*}"; body="${r#*|}"
    if [ "$code" = "200" ] && echo "$body" | grep -q '"status":"ok"'; then
      local ver; ver="$(echo "$body" | grep -o '"migrationVersion":[0-9]*' | head -n1)"
      detail="$detail; /health 200 ($ver)"
    elif [ "$code" = "503" ]; then
      state="$(worse "$state" DEGRADED)"; detail="$detail; /health 503 degraded: $body"
    else
      state="$(worse "$state" DEGRADED)"; detail="$detail; /health $code"
    fi
  else
    detail="$detail; (port 8000 not published)"
  fi
  record api "$state" "$detail"
}

# Caddy static frontends: container layer + an HTTP GET that should return 200.
check_caddy() { # <logical-name> <container> <path> [extra-path...]
  local name="$1" c="$2" path="$3"; shift 3
  local cl; cl="$(check_container_layer "$c")" || { record "$name" DOWN "${cl#*|}"; return; }
  local state="${cl%%|*}" detail="${cl#*|}"
  local ep; ep="$(host_endpoint "$c" 8080)"
  if [ -n "$ep" ]; then
    local p; for p in "$path" "$@"; do
      local r code; r="$(http_probe "http://${ep}${p}")"; code="${r%%|*}"
      if [ "$code" = "200" ] || [ "$code" = "302" ] || [ "$code" = "304" ]; then
        detail="$detail; ${p} ${code}"
      elif [ "$code" = "401" ]; then
        # Front door is up but the optional installation password
        # (OCTBASE_SITE_PASSWORD_HASH) is active — a 401 here is healthy, not a fault.
        # /health itself is never gated, so the API reverse proxy is still
        # validated by its own probe.
        detail="$detail; ${p} 401 (password-gated)"
      else
        state="$(worse "$state" DEGRADED)"; detail="$detail; ${p} ${code}"
      fi
    done
  else
    detail="$detail; (port 8080 not published — checked via container layer only)"
  fi
  record "$name" "$state" "$detail"
}

# ── Run all checks ───────────────────────────────────────────────────────────
check_postgres
check_api
# Frontend also validates its reverse proxy to the API (/health) and the mobile
# app served under /m/ — one probe covers three moving parts.
check_caddy frontend "${PROJECT}_octbase-frontend_1" "/" "/health" "/m/"
check_caddy mobile   "${PROJECT}_octbase-mobile_1"   "/"

# ── Aggregate & report ───────────────────────────────────────────────────────
OVERALL="OK"; EXIT=0
for line in "${RESULTS[@]}"; do
  st="${line#*|}"; st="${st%%|*}"
  case "$st" in
    DOWN) OVERALL="DOWN"; EXIT=2 ;;
    DEGRADED) [ "$OVERALL" = "OK" ] && { OVERALL="DEGRADED"; EXIT=1; } ;;
  esac
done

# When not a single container of the project exists, "every service DOWN" is
# almost never four separate outages — it is a wrong --project (e.g. a bare
# run under the dev account, whose stack is "octbase_dev"). Detect that shape
# and say so explicitly instead of letting the operator chase four ghosts.
NONE_FOUND=1
for line in "${RESULTS[@]}"; do
  case "$line" in
    *"|DOWN|container missing") ;;
    *) NONE_FOUND=0 ;;
  esac
done
NONE_HINT="no containers found for project '$PROJECT' — wrong --project?"

if [ "$JSON" -eq 1 ]; then
  printf '{"project":"%s","overall":"%s","ts":"%s",' \
    "$PROJECT" "$OVERALL" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  [ "$NONE_FOUND" -eq 1 ] && printf '"hint":"%s",' "$NONE_HINT"
  printf '"services":{'
  first=1
  for line in "${RESULTS[@]}"; do
    n="${line%%|*}"; rest="${line#*|}"; s="${rest%%|*}"; d="${rest#*|}"
    # JSON-escape the detail (a probed body can contain anything): backslashes
    # FIRST — or the escapes added next would themselves be doubled — then
    # double quotes; control characters are stripped, they carry no signal here.
    d="${d//\\/\\\\}"
    d="${d//\"/\\\"}"
    d="$(printf '%s' "$d" | tr -d '\000-\037\177')"
    [ $first -eq 0 ] && printf ','
    printf '"%s":{"state":"%s","detail":"%s"}' "$n" "$s" "$d"
    first=0
  done
  printf '}}\n'
  exit $EXIT
fi

if [ "$QUIET" -eq 0 ]; then
  printf '%sOctbase health — project "%s" — %s%s\n' "$C_DIM" "$PROJECT" "$(date -u +%H:%M:%SZ)" "$C_RST"
  for line in "${RESULTS[@]}"; do
    n="${line%%|*}"; rest="${line#*|}"; s="${rest%%|*}"; d="${rest#*|}"
    case "$s" in
      OK) col="$C_OK"; mark="OK  " ;;
      DEGRADED) col="$C_WARN"; mark="WARN" ;;
      *) col="$C_BAD"; mark="DOWN" ;;
    esac
    printf '  %s[%s]%s %-9s %s%s%s\n' "$col" "$mark" "$C_RST" "$n" "$C_DIM" "$d" "$C_RST"
  done
fi

if [ "$NONE_FOUND" -eq 1 ]; then
  printf '%s==> %s%s\n' "$C_BAD" "$NONE_HINT" "$C_RST"
fi

case "$OVERALL" in
  OK) col="$C_OK" ;;
  DEGRADED) col="$C_WARN" ;;
  *) col="$C_BAD" ;;
esac
printf '%s==> overall: %s%s\n' "$col" "$OVERALL" "$C_RST"
exit $EXIT
