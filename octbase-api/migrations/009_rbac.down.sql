-- 009_rbac.down.sql: Reverse the RBAC migration.

DROP TABLE IF EXISTS audit_logs;

-- Reverse project membership role names
UPDATE memberships SET role = 'OWNER'     WHERE role = 'PROJECT_ADMIN';
UPDATE memberships SET role = 'DEVELOPER' WHERE role = 'PROJECT_MEMBER';
UPDATE memberships SET role = 'VIEWER'    WHERE role = 'PROJECT_VIEWER';

ALTER TABLE projects    DROP COLUMN IF EXISTS created_by_user_id;
ALTER TABLE memberships DROP COLUMN IF EXISTS assigned_by_user_id;
ALTER TABLE users       DROP COLUMN IF EXISTS last_login_at;
ALTER TABLE users       DROP COLUMN IF EXISTS status;
ALTER TABLE users       DROP COLUMN IF EXISTS global_role;

DROP INDEX IF EXISTS idx_users_global_role;
