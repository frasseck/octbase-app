DROP TABLE IF EXISTS mfa_recovery_codes;
DROP TABLE IF EXISTS mfa_credentials;
ALTER TABLE users DROP COLUMN IF EXISTS mfa_enabled;
