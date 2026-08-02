-- Track the last accepted TOTP time-step per credential so a code cannot be
-- replayed within its ±1-step acceptance window (~90s). A real step is
-- floor(unixtime/30) — a large positive number — so the default 0 means "no
-- code accepted yet" and never collides with a real step.
ALTER TABLE mfa_credentials ADD COLUMN IF NOT EXISTS last_totp_step BIGINT NOT NULL DEFAULT 0;
