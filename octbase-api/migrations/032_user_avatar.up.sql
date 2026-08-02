-- Profile pictures: store each user's avatar image inline on the users row.
-- Kept in the DB (not the attachment filesystem) so avatars work on every
-- deployment regardless of whether OCTBASE_ATTACHMENTS_DIR is configured, are
-- removed transactionally with the user, and need no shared storage across API
-- instances. avatar_updated_at doubles as the client-side cache-busting token
-- (its presence also signals "this user has an avatar"). TEXT to match the
-- users table's existing created_at/updated_at convention.
ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar BYTEA;
ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar_content_type TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar_updated_at TEXT;
