DROP TABLE IF EXISTS oauth_states;
ALTER TABLE repository_connections DROP COLUMN IF EXISTS token_expires_at;
ALTER TABLE repository_connections DROP COLUMN IF EXISTS refresh_token_enc;
ALTER TABLE repository_connections DROP COLUMN IF EXISTS auth_kind;
