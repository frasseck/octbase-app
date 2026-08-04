#!/usr/bin/env bash
#
# stamp-baseline.sh — repair instances broken by the 001_baseline squash (56827b4).
#
# Run as ROOT (rootless podman is per-user; this drives each client's own
# podman context the way /usr/local/lib/octbase/backup-fleet.sh does).
#
#   ./stamp-baseline.sh              # every client in /etc/octbase/clients.d
#   ./stamp-baseline.sh demo beyags  # only these
#   DRY_RUN=1 ./stamp-baseline.sh    # inspect and decide, change nothing
#
# Per client it: dumps the DB, reads schema_migrations, decides 38-vs-39 from
# the FK (not the column — ADD COLUMN in 039 is idempotent, ADD CONSTRAINT is
# not), applies 039 only if genuinely missing, stamps to 1, restarts the API
# and waits for health. Any client that is not in a state it fully understands
# is skipped untouched.
set -uo pipefail

REGISTRY=/etc/octbase/clients.d
BACKUP_DIR="${BACKUP_DIR:-/var/backups/octbase/prestamp}"
DRY_RUN="${DRY_RUN:-0}"
STAMP="$(date +%Y%m%d-%H%M%S)"
rc_overall=0

[ "$(id -u)" -eq 0 ] || { echo "must run as root (rootless podman is per-user)" >&2; exit 3; }

log()  { printf '%s  %s\n' "$(date '+%H:%M:%S')" "$*"; }
fail() { log "  ERROR: $*"; rc_overall=1; }

# Run a command in a client's podman context. cd /tmp because `sudo -u` refuses
# to start in a cwd the target account cannot read.
as_user() { local acct="$1" uid="$2"; shift 2; (cd /tmp && sudo -u "$acct" XDG_RUNTIME_DIR="/run/user/$uid" "$@"); }

read -r -d '' MIG_039 <<'SQL'
ALTER TABLE activity_entries ADD COLUMN IF NOT EXISTS release_id TEXT;
ALTER TABLE activity_entries ADD COLUMN IF NOT EXISTS sprint_id TEXT;
ALTER TABLE activity_entries ADD COLUMN IF NOT EXISTS target_deleted BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE activity_entries a
   SET task_id = NULL, target_deleted = TRUE
 WHERE a.task_id IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.id = a.task_id);

DELETE FROM activity_entries a
 WHERE NOT EXISTS (SELECT 1 FROM projects p WHERE p.id = a.project_id);

ALTER TABLE activity_entries
  ADD CONSTRAINT fk_activity_entries_project FOREIGN KEY (project_id) REFERENCES projects(id);
ALTER TABLE activity_entries
  ADD CONSTRAINT fk_activity_entries_task FOREIGN KEY (task_id) REFERENCES tasks(id);
ALTER TABLE activity_entries
  ADD CONSTRAINT fk_activity_entries_release FOREIGN KEY (release_id) REFERENCES releases(id);
ALTER TABLE activity_entries
  ADD CONSTRAINT fk_activity_entries_sprint FOREIGN KEY (sprint_id) REFERENCES sprints(id);

CREATE INDEX IF NOT EXISTS idx_activity_release ON activity_entries(release_id);
CREATE INDEX IF NOT EXISTS idx_activity_sprint  ON activity_entries(sprint_id);
SQL

shopt -s nullglob
if [ $# -gt 0 ]; then confs=(); for n in "$@"; do confs+=("$REGISTRY/$n.conf"); done
else confs=("$REGISTRY"/*.conf); fi

mkdir -p "$BACKUP_DIR"; chmod 700 "$BACKUP_DIR"

for f in "${confs[@]}"; do
  NAME="" USER_ACCT="" HOME_DIR="" API_PORT=""
  [ -f "$f" ] || { echo "no such client conf: $f" >&2; rc_overall=1; continue; }
  . "$f"
  [ -n "$NAME" ] && [ -n "$USER_ACCT" ] || continue

  log "[$NAME] ---------------------------------------------"
  uid="$(id -u "$USER_ACCT" 2>/dev/null)" || true
  [ -n "$uid" ] || { fail "account $USER_ACCT missing"; continue; }

  PSQL=(as_user "$USER_ACCT" "$uid" podman exec octbase_postgres_1 psql -U octbase -d octbase)
  q() { "${PSQL[@]}" -tAc "$1" 2>/dev/null | tr -d '[:space:]'; }

  if ! as_user "$USER_ACCT" "$uid" podman exec octbase_postgres_1 pg_isready -U octbase >/dev/null 2>&1; then
    fail "postgres container not reachable — cannot proceed"; continue
  fi

  ver="$(q 'SELECT version FROM schema_migrations LIMIT 1')"
  dirty="$(q 'SELECT dirty FROM schema_migrations LIMIT 1')"
  log "  schema_migrations: version=${ver:-?} dirty=${dirty:-?}"

  [ -n "$ver" ] || { fail "could not read schema_migrations"; continue; }
  if [ "$dirty" = "t" ] || [ "$dirty" = "true" ]; then
    fail "database is DIRTY — a migration half-applied. Hand-inspect; not safe to stamp."; continue
  fi

  case "$ver" in
    1|2) log "  already stamped (version $ver) — nothing to migrate"; need_stamp=0 ;;
    38|39) need_stamp=1 ;;
    *) fail "unexpected version '$ver' — not a pre-squash instance. Skipping."; continue ;;
  esac

  if [ "$need_stamp" = "1" ]; then
    # 38 vs 39 decided by the FK: 039's ADD COLUMNs are idempotent, its ADD
    # CONSTRAINTs are not, so a real 39 would fail on a duplicate constraint.
    has_fk="$(q "SELECT 1 FROM pg_constraint WHERE conname='fk_activity_entries_sprint'")"
    if [ "$has_fk" = "1" ]; then
      log "  039 already present (fk_activity_entries_sprint exists) → stamp only"
      apply_039=0
    else
      log "  039 missing → will apply it, then stamp"
      apply_039=1
    fi

    if [ "$DRY_RUN" = "1" ]; then
      log "  DRY_RUN: would apply_039=$apply_039, stamp to 1, restart API"; continue
    fi

    dump="$BACKUP_DIR/${NAME}-prestamp-${STAMP}.dump"
    log "  dumping to $dump"
    if ! as_user "$USER_ACCT" "$uid" podman exec octbase_postgres_1 \
        pg_dump -U octbase -d octbase -Fc --no-owner >"$dump" 2>/dev/null; then
      fail "pre-change pg_dump failed — refusing to touch this database"; rm -f "$dump"; continue
    fi
    sz=$(stat -c%s "$dump" 2>/dev/null || echo 0)
    [ "$sz" -ge 1024 ] || { fail "dump suspiciously small ($sz bytes) — refusing to proceed"; continue; }
    log "  dump ok ($sz bytes)"

    if [ "$apply_039" = "1" ]; then
      if ! printf '%s\n' "$MIG_039" | as_user "$USER_ACCT" "$uid" \
          podman exec -i octbase_postgres_1 psql -U octbase -d octbase -v ON_ERROR_STOP=1 -q; then
        fail "applying 039 failed — database NOT stamped, restore from $dump if needed"; continue
      fi
      log "  039 applied"
    fi

    if ! "${PSQL[@]}" -q -c 'UPDATE schema_migrations SET version=1, dirty=false' >/dev/null 2>&1; then
      fail "stamping failed"; continue
    fi
    log "  stamped to version 1"
  fi

  # Restart the API (works on a stopped or crash-looping container alike).
  api_ctr="$(as_user "$USER_ACCT" "$uid" podman ps -a --format '{{.Names}}' 2>/dev/null | grep -E 'octbase.*api' | head -1)"
  [ -n "$api_ctr" ] || { fail "could not find the API container"; continue; }
  log "  restarting $api_ctr"
  as_user "$USER_ACCT" "$uid" podman restart "$api_ctr" >/dev/null 2>&1 || fail "restart returned non-zero"

  ok=0
  for _ in $(seq 1 30); do
    for path in /api/v1/health /health; do
      if out="$(curl -fsS -m 3 "http://127.0.0.1:${API_PORT}${path}" 2>/dev/null)"; then
        log "  HEALTH ok: $out"; ok=1; break 2
      fi
    done
    sleep 2
  done
  [ "$ok" = "1" ] || fail "API did not become healthy on port ${API_PORT} — check: sudo -u $USER_ACCT XDG_RUNTIME_DIR=/run/user/$uid podman logs --tail 50 $api_ctr"
done

log "=============================================="
[ "$rc_overall" -eq 0 ] && log "all clients OK" || log "one or more clients FAILED — see above"
exit "$rc_overall"
