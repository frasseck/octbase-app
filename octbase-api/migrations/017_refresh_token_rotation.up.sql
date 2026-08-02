-- Track refresh-token rotation so reuse of an already-rotated token can be
-- detected. On rotation the old token is marked (rotated_at) instead of being
-- deleted; replaying it then signals theft and revokes the whole session family.
ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS rotated_at TIMESTAMPTZ;
