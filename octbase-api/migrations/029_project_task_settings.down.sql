DROP TABLE IF EXISTS project_priorities;
ALTER TABLE projects DROP COLUMN IF EXISTS initiative_enabled;
ALTER TABLE projects DROP COLUMN IF EXISTS theme_enabled;
