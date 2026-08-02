-- Close the remaining "users can overwrite each other" gaps
-- (docs/architecture.md §3): boards, board_columns and task_comments join the
-- optimistic-locking convention (version column + WHERE id=$N AND version=$M
-- guarded UPDATEs). Existing rows start at version 1, matching the default
-- used when new rows are created.
ALTER TABLE boards ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE board_columns ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE task_comments ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1;

-- One ACTIVE sprint per project is a business rule that was only enforced by
-- an application-level check-then-act (Service.StartSprint), so two
-- concurrent starts could both pass the check. Back it with the database.
CREATE UNIQUE INDEX IF NOT EXISTS idx_sprints_one_active
  ON sprints(project_id) WHERE status = 'ACTIVE';

-- Page slugs were only checked for uniqueness in the handler (check-then-act),
-- so concurrent creates could produce duplicates. Deterministically de-dupe
-- any existing collisions (keep the oldest row's slug, suffix later ones with
-- a fragment of their id), then back the rule with a unique index.
UPDATE pages p
SET slug = p.slug || '-' || substr(p.id, 1, 8)
WHERE EXISTS (
  SELECT 1 FROM pages q
  WHERE q.project_id = p.project_id AND q.slug = p.slug
    AND (q.created_at < p.created_at OR (q.created_at = p.created_at AND q.id < p.id))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_pages_project_slug
  ON pages(project_id, slug);
