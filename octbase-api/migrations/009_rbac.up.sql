-- 009_rbac.up.sql: Global roles, user status, audit logs, updated project membership roles.
-- All statements are idempotent (IF NOT EXISTS / OR IGNORE).

-- Add global_role to users
ALTER TABLE users ADD COLUMN IF NOT EXISTS global_role TEXT NOT NULL DEFAULT 'USER';

-- Migrate existing admin flag
UPDATE users SET global_role = 'ADMIN' WHERE is_admin = true;

-- Add status column (active | invited | disabled)
ALTER TABLE users ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
UPDATE users SET status = 'disabled' WHERE is_active = false;

-- Add last_login_at
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMPTZ;

-- Add assigned_by_user_id to memberships
ALTER TABLE memberships ADD COLUMN IF NOT EXISTS assigned_by_user_id TEXT REFERENCES users(id);

-- Add created_by_user_id to projects
ALTER TABLE projects ADD COLUMN IF NOT EXISTS created_by_user_id TEXT REFERENCES users(id);

-- Rename project membership roles to new canonical names
UPDATE memberships SET role = 'PROJECT_ADMIN'  WHERE role = 'OWNER';
UPDATE memberships SET role = 'PROJECT_MEMBER' WHERE role = 'DEVELOPER';
UPDATE memberships SET role = 'PROJECT_VIEWER' WHERE role = 'VIEWER';

-- Audit logs table
CREATE TABLE IF NOT EXISTS audit_logs (
    id            TEXT PRIMARY KEY,
    actor_user_id TEXT NOT NULL DEFAULT '',
    action        TEXT NOT NULL,
    target_type   TEXT NOT NULL DEFAULT '',
    target_id     TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    ip_address    TEXT NOT NULL DEFAULT '',
    user_agent    TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_actor   ON audit_logs(actor_user_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created ON audit_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_action  ON audit_logs(action);
CREATE INDEX IF NOT EXISTS idx_users_global_role  ON users(global_role);
