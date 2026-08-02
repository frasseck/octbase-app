-- The BUG/CHORE → TASK conversion is one-way; only the structure is reverted.
DROP INDEX IF EXISTS idx_tasks_parent_id;
ALTER TABLE tasks DROP COLUMN IF EXISTS parent_id;
