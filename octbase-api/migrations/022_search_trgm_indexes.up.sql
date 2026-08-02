-- Trigram GIN indexes so the ILIKE '%q%' searches (task search, page search,
-- unified search, project lookup) can use an index instead of scanning the
-- whole table.
--
-- pg_trgm is a "trusted" extension (PostgreSQL 13+), so the application's own
-- database role may create it without superuser rights. It is installed into
-- "public" and referenced schema-qualified below, because the test harness runs
-- these migrations with search_path pinned to a per-test schema that does not
-- include public. The advisory lock serializes concurrent test-schema migration
-- runs, which would otherwise race on CREATE EXTENSION IF NOT EXISTS.
SELECT pg_advisory_xact_lock(422202600);
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

CREATE INDEX IF NOT EXISTS idx_tasks_title_trgm ON tasks USING GIN (title public.gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_tasks_description_trgm ON tasks USING GIN (description public.gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_pages_title_trgm ON pages USING GIN (title public.gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_pages_content_trgm ON pages USING GIN (content public.gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_projects_name_trgm ON projects USING GIN (name public.gin_trgm_ops);
