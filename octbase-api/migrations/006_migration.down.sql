ALTER TABLE pages DROP COLUMN IF EXISTS external_ref;
DROP INDEX IF EXISTS idx_tasks_external_ref;
ALTER TABLE tasks DROP COLUMN IF EXISTS external_ref;
