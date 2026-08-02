-- Octbase — provision a least-privilege runtime database role (security L10).
--
-- WHY. By default the API connects as the database-owning bootstrap role, which
-- is a superuser in the stock Postgres image, and runs both migrations (DDL) and
-- ordinary traffic (DML) as that role. SQL injection or an application
-- compromise then yields full control of the database server. This script
-- separates the two:
--
--   * the OWNER role keeps the schema and runs migrations  -> OCTBASE_MIGRATE_DATABASE_URL
--   * a restricted APP role serves traffic (DML only)      -> OCTBASE_DATABASE_URL
--
-- The API falls back to a single role when OCTBASE_MIGRATE_DATABASE_URL is
-- unset, so this is opt-in. It is intended for external/managed databases; the
-- bundled single-container Postgres can keep one role.
--
-- USAGE. Run as the owner/superuser against the Octbase database, substituting a
-- real password (or use \set / a managed-database console):
--
--   psql "$OWNER_DATABASE_URL" \
--     -v app_password="'"$(openssl rand -base64 24)"'" \
--     -f scripts/db-least-privilege.sql
--
-- Then point the API at both roles (see .env.example and docs/hosting-concept.md §8):
--
--   OCTBASE_DATABASE_URL=postgres://octbase_app:<app_password>@host:5432/octbase?sslmode=require
--   OCTBASE_MIGRATE_DATABASE_URL=postgres://octbase:<owner_password>@host:5432/octbase?sslmode=require
--
-- The script is idempotent and safe to re-run: it grants on tables that exist
-- today AND sets default privileges so tables created by future migrations are
-- covered automatically. Re-run it only if you change the owner role.

\set ON_ERROR_STOP on

-- The role that owns the schema and runs migrations. Change if your owner role
-- is not the default `octbase`.
\if :{?owner_role}
\else
  \set owner_role 'octbase'
\endif

-- Fail loudly (and with a non-zero exit, via ON_ERROR_STOP) rather than creating
-- a passwordless role: `\quit` alone would exit 0 and look like success.
\if :{?app_password}
\else
  \echo '---'
  \echo 'ERROR: app_password is required, as a quoted SQL literal. For example:'
  \echo '  psql "$OWNER_DATABASE_URL" -v app_password="''$(openssl rand -base64 24)''" -f scripts/db-least-privilege.sql'
  \echo '---'
  DO $$ BEGIN RAISE EXCEPTION 'app_password not set'; END $$;
\endif

BEGIN;

-- 1. The restricted runtime role. LOGIN only; no CREATEDB/CREATEROLE/SUPERUSER,
--    and NOINHERIT so it never picks up owner rights via role membership.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'octbase_app') THEN
    CREATE ROLE octbase_app LOGIN NOINHERIT;
  END IF;
END
$$;

ALTER ROLE octbase_app WITH PASSWORD :app_password;
ALTER ROLE octbase_app NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;

-- 2. Connect + read the schema, but never create objects in it. (Postgres 15+
--    already revokes CREATE on public from PUBLIC; this is explicit and also
--    correct on 13/14.)
GRANT CONNECT ON DATABASE :"DBNAME" TO octbase_app;
GRANT USAGE ON SCHEMA public TO octbase_app;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE CREATE ON SCHEMA public FROM octbase_app;

-- 3. DML on the tables that exist now — no DDL, no TRUNCATE, no REFERENCES.
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO octbase_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO octbase_app;

-- 4. The same for tables/sequences future migrations create. Without this, every
--    new migration would silently leave the app role without access to its new
--    table and the app would start failing after a deploy. Default privileges are
--    scoped to the creating role, hence FOR ROLE :owner_role — migrations must
--    therefore keep running as that role.
ALTER DEFAULT PRIVILEGES FOR ROLE :"owner_role" IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO octbase_app;
ALTER DEFAULT PRIVILEGES FOR ROLE :"owner_role" IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO octbase_app;

COMMIT;

-- 5. Verify: this must print `f` (the app role cannot create objects in public).
SELECT has_schema_privilege('octbase_app', 'public', 'CREATE') AS app_role_can_create_ddl;
