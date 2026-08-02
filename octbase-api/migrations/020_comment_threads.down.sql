DROP INDEX IF EXISTS idx_task_comments_parent_id;
ALTER TABLE task_comments DROP COLUMN IF EXISTS parent_id;
