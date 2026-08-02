-- Bring task_categories, task_templates and repository_connections in line
-- with the codebase's optimistic-locking convention (docs/architecture.md
-- §3): every mutable aggregate gets a version column, and updates are
-- guarded by WHERE id=$N AND version=$M. These three tables predate that
-- convention and were updated with plain WHERE id=$N UPDATEs; existing rows
-- start at version 1, matching the default used when new rows are created.
ALTER TABLE task_categories ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE task_templates ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE repository_connections ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1;
