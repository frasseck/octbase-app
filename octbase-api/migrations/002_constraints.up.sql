-- 002_constraints.sql: indexes and uniqueness constraints added in POC hardening.
-- All statements are idempotent (IF NOT EXISTS).

-- Board column status uniqueness: each status may appear at most once per board.
CREATE UNIQUE INDEX IF NOT EXISTS idx_board_columns_board_status
    ON board_columns(board_id, status);

-- Performance indexes for common query patterns.
CREATE INDEX IF NOT EXISTS idx_tasks_project_id     ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_assignee_id    ON tasks(assignee_id);
CREATE INDEX IF NOT EXISTS idx_tasks_release_id   ON tasks(release_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status         ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_board_column   ON tasks(board_column_id);
CREATE INDEX IF NOT EXISTS idx_board_columns_board  ON board_columns(board_id);
CREATE INDEX IF NOT EXISTS idx_activity_project     ON activity_entries(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_activity_task        ON activity_entries(task_id);
CREATE INDEX IF NOT EXISTS idx_page_refs_page       ON page_task_references(page_id);
CREATE INDEX IF NOT EXISTS idx_page_refs_task       ON page_task_references(task_id);
CREATE INDEX IF NOT EXISTS idx_branch_refs_task     ON branch_references(task_id);
CREATE INDEX IF NOT EXISTS idx_memberships_user     ON memberships(user_id);
