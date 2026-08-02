-- Per-connection access token (AES-GCM encrypted at rest) and optional API base
-- URL for self-hosted GitLab / Bitbucket Server / GitHub Enterprise instances.
ALTER TABLE repository_connections ADD COLUMN IF NOT EXISTS access_token_enc TEXT;
ALTER TABLE repository_connections ADD COLUMN IF NOT EXISTS api_base_url     TEXT;
