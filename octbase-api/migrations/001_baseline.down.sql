-- Reverses 001_baseline: drops the entire application schema.
--
-- CASCADE because the baseline creates 50 foreign keys and dropping tables in
-- dependency order by hand is a maintenance trap; the tables named here are the
-- complete set the up-migration creates.
--
-- pg_trgm is deliberately left installed, as migration 022's down did: it is
-- database-wide, lives in public rather than in this schema, other objects may
-- depend on it, and leaving it costs nothing.
DROP TABLE IF EXISTS activity_entries CASCADE;
DROP TABLE IF EXISTS audit_logs CASCADE;
DROP TABLE IF EXISTS board_columns CASCADE;
DROP TABLE IF EXISTS board_external_columns CASCADE;
DROP TABLE IF EXISTS boards CASCADE;
DROP TABLE IF EXISTS branch_references CASCADE;
DROP TABLE IF EXISTS invitations CASCADE;
DROP TABLE IF EXISTS memberships CASCADE;
DROP TABLE IF EXISTS mfa_credentials CASCADE;
DROP TABLE IF EXISTS mfa_recovery_codes CASCADE;
DROP TABLE IF EXISTS notification_preferences CASCADE;
DROP TABLE IF EXISTS notifications CASCADE;
DROP TABLE IF EXISTS oauth_states CASCADE;
DROP TABLE IF EXISTS page_revisions CASCADE;
DROP TABLE IF EXISTS page_task_references CASCADE;
DROP TABLE IF EXISTS pages CASCADE;
DROP TABLE IF EXISTS password_reset_tokens CASCADE;
DROP TABLE IF EXISTS project_priorities CASCADE;
DROP TABLE IF EXISTS project_task_counters CASCADE;
DROP TABLE IF EXISTS projects CASCADE;
DROP TABLE IF EXISTS refresh_tokens CASCADE;
DROP TABLE IF EXISTS releases CASCADE;
DROP TABLE IF EXISTS repository_connections CASCADE;
DROP TABLE IF EXISTS sprints CASCADE;
DROP TABLE IF EXISTS task_attachments CASCADE;
DROP TABLE IF EXISTS task_categories CASCADE;
DROP TABLE IF EXISTS task_comments CASCADE;
DROP TABLE IF EXISTS task_links CASCADE;
DROP TABLE IF EXISTS task_relations CASCADE;
DROP TABLE IF EXISTS task_templates CASCADE;
DROP TABLE IF EXISTS tasks CASCADE;
DROP TABLE IF EXISTS user_preferences CASCADE;
DROP TABLE IF EXISTS users CASCADE;
