-- repair-039-poststamp.sql
--
-- For an instance that was at version 38 and got stamped to 1 WITHOUT first
-- applying 039. The schema_migrations row is already correct (now at 2) — this
-- adds only the DDL/backfill that 039 would have done. Do NOT re-stamp.
--
-- Safe to run on any partial state: every step is guarded, so re-running it or
-- running it on an instance that was genuinely a 39 is a no-op.
--
--   podman exec -i octbase_postgres_1 psql -U octbase -d octbase -v ON_ERROR_STOP=1 -q < repair-039-poststamp.sql

BEGIN;

ALTER TABLE activity_entries ADD COLUMN IF NOT EXISTS release_id TEXT;
ALTER TABLE activity_entries ADD COLUMN IF NOT EXISTS sprint_id TEXT;
ALTER TABLE activity_entries ADD COLUMN IF NOT EXISTS target_deleted BOOLEAN NOT NULL DEFAULT FALSE;

-- Entries whose task is already gone: unlink and grey them.
UPDATE activity_entries a
   SET task_id = NULL, target_deleted = TRUE
 WHERE a.task_id IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.id = a.task_id);

-- Entries whose project is gone are unreachable rows.
DELETE FROM activity_entries a
 WHERE NOT EXISTS (SELECT 1 FROM projects p WHERE p.id = a.project_id);

-- The four FKs, each added only if absent (plain ADD CONSTRAINT is not
-- idempotent — this is what makes the script safe to re-run).
DO $$
DECLARE
  c RECORD;
BEGIN
  FOR c IN
    SELECT * FROM (VALUES
      ('fk_activity_entries_project', 'project_id', 'projects'),
      ('fk_activity_entries_task',    'task_id',    'tasks'),
      ('fk_activity_entries_release', 'release_id', 'releases'),
      ('fk_activity_entries_sprint',  'sprint_id',  'sprints')
    ) AS t(name, col, reftable)
  LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = c.name) THEN
      EXECUTE format(
        'ALTER TABLE activity_entries ADD CONSTRAINT %I FOREIGN KEY (%I) REFERENCES %I(id)',
        c.name, c.col, c.reftable);
      RAISE NOTICE 'added %', c.name;
    ELSE
      RAISE NOTICE 'skipped % (already present)', c.name;
    END IF;
  END LOOP;
END $$;

CREATE INDEX IF NOT EXISTS idx_activity_release ON activity_entries(release_id);
CREATE INDEX IF NOT EXISTS idx_activity_sprint  ON activity_entries(sprint_id);

COMMIT;

-- Verify: expect 4 | 3
SELECT (SELECT count(*) FROM pg_constraint
          WHERE conname LIKE 'fk_activity_entries_%') AS fks,
       (SELECT count(*) FROM information_schema.columns
          WHERE table_name = 'activity_entries'
            AND column_name IN ('release_id','sprint_id','target_deleted')) AS cols;
