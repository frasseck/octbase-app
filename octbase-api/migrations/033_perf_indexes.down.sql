-- Reverse 033: drop the added indexes and restore the two prefix indexes that
-- the up migration removed (definitions copied verbatim from 002/007).
DROP INDEX IF EXISTS idx_task_comments_task_created;
DROP INDEX IF EXISTS idx_task_links_task_created;
DROP INDEX IF EXISTS idx_task_attachments_task_created;
DROP INDEX IF EXISTS idx_task_relations_source_type;
DROP INDEX IF EXISTS idx_task_relations_target;
DROP INDEX IF EXISTS idx_boards_project;
DROP INDEX IF EXISTS idx_releases_project;
DROP INDEX IF EXISTS idx_page_revisions_page_created;
DROP INDEX IF EXISTS idx_page_revisions_author;
DROP INDEX IF EXISTS idx_tasks_project_created;

CREATE INDEX IF NOT EXISTS idx_board_columns_board ON board_columns(board_id);
CREATE INDEX IF NOT EXISTS idx_sprints_project_id ON sprints(project_id);
