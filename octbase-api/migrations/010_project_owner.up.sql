-- 010_project_owner.up.sql: Introduce the PROJECT_OWNER role.
-- Idempotent: re-running is a no-op once every project has an owner.

-- Backfill 1: promote the project creator's membership (if PROJECT_ADMIN)
-- to PROJECT_OWNER.
UPDATE memberships m
SET role = 'PROJECT_OWNER'
WHERE m.role = 'PROJECT_ADMIN'
  AND m.user_id = (
    SELECT p.created_by_user_id FROM projects p
    WHERE p.id = m.project_id AND p.created_by_user_id IS NOT NULL
  );

-- Backfill 2 (fallback): for any project still without a PROJECT_OWNER
-- (no created_by_user_id, or the creator has no membership row), promote the
-- earliest-assigned PROJECT_ADMIN.
UPDATE memberships m
SET role = 'PROJECT_OWNER'
WHERE m.id = (
    SELECT m2.id FROM memberships m2
    WHERE m2.project_id = m.project_id AND m2.role = 'PROJECT_ADMIN'
    ORDER BY m2.created_at ASC, m2.id ASC
    LIMIT 1
  )
  AND NOT EXISTS (
    SELECT 1 FROM memberships m3
    WHERE m3.project_id = m.project_id AND m3.role = 'PROJECT_OWNER'
  );
