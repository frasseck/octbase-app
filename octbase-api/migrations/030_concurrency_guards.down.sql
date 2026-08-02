DROP INDEX IF EXISTS idx_pages_project_slug;
DROP INDEX IF EXISTS idx_sprints_one_active;
ALTER TABLE task_comments DROP COLUMN IF EXISTS version;
ALTER TABLE board_columns DROP COLUMN IF EXISTS version;
ALTER TABLE boards DROP COLUMN IF EXISTS version;
