-- OAuth support for repository connections: distinguish PAT vs OAUTH auth, and
-- store the (encrypted) refresh token plus access-token expiry for rotation.
ALTER TABLE repository_connections ADD COLUMN IF NOT EXISTS auth_kind         TEXT NOT NULL DEFAULT 'PAT';
ALTER TABLE repository_connections ADD COLUMN IF NOT EXISTS refresh_token_enc TEXT;
ALTER TABLE repository_connections ADD COLUMN IF NOT EXISTS token_expires_at  TEXT;

-- One-time CSRF/state records tying an in-flight OAuth authorization back to the
-- connection and user that started it.
CREATE TABLE IF NOT EXISTS oauth_states (
    state         TEXT PRIMARY KEY,
    provider      TEXT NOT NULL,
    repository_id TEXT NOT NULL REFERENCES repository_connections(id) ON DELETE CASCADE,
    user_id       TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    expires_at    TEXT NOT NULL
);
