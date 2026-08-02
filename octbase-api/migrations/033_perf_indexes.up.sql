-- 033_perf_indexes.sql: indexes for the child-table and per-project read paths.
--
-- PostgreSQL does NOT create an index for the referencing side of a foreign
-- key, so every "list the children of this parent" query below was a sequential
-- scan of the whole child table. Each index is (parent_id, created_at) where the
-- read path also orders by creation time, so one index serves both the filter
-- and the sort.

-- Task detail panel, project export and Jira CSV export: comments, links and
-- attachments of a task, ordered by created_at.
CREATE INDEX IF NOT EXISTS idx_task_comments_task_created
    ON task_comments(task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_task_links_task_created
    ON task_links(task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_task_attachments_task_created
    ON task_attachments(task_id, created_at);

-- Task relations are read from both ends: the outgoing side additionally filters
-- by relation_type (the BLOCKS cycle walk), the incoming side does not.
CREATE INDEX IF NOT EXISTS idx_task_relations_source_type
    ON task_relations(source_task_id, relation_type);
CREATE INDEX IF NOT EXISTS idx_task_relations_target
    ON task_relations(target_task_id);

-- Per-project listings: the boards of a project (dashboard/My Work) and its
-- releases (release list, upcoming releases).
CREATE INDEX IF NOT EXISTS idx_boards_project ON boards(project_id);
CREATE INDEX IF NOT EXISTS idx_releases_project ON releases(project_id);

-- Page history: the revisions of a page are listed newest-first, and the
-- retention purge / author attribution look revisions up by author.
CREATE INDEX IF NOT EXISTS idx_page_revisions_page_created
    ON page_revisions(page_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_page_revisions_author
    ON page_revisions(author_id);

-- The task list is filtered by project and sorted by created_at DESC (the
-- default sort), which the single-column idx_tasks_project_id cannot serve.
CREATE INDEX IF NOT EXISTS idx_tasks_project_created
    ON tasks(project_id, created_at DESC);

-- Redundant indexes: both are an exact left prefix of a wider index on the same
-- table, so the wider index already serves every lookup they could
-- (idx_board_columns_board_status is UNIQUE(board_id, status) from 002;
-- idx_sprints_project_status is (project_id, status) from 007). Keeping them
-- only costs write amplification. Recreated by the down migration.
DROP INDEX IF EXISTS idx_board_columns_board;
DROP INDEX IF EXISTS idx_sprints_project_id;
