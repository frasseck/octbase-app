DROP INDEX IF EXISTS idx_projects_name_trgm;
DROP INDEX IF EXISTS idx_pages_content_trgm;
DROP INDEX IF EXISTS idx_pages_title_trgm;
DROP INDEX IF EXISTS idx_tasks_description_trgm;
DROP INDEX IF EXISTS idx_tasks_title_trgm;
-- The pg_trgm extension is deliberately left installed: it is database-wide,
-- other schemas/objects may depend on it, and keeping it is harmless.
