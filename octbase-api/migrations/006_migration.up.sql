ALTER TABLE tasks ADD COLUMN IF NOT EXISTS external_ref TEXT;
CREATE INDEX IF NOT EXISTS idx_tasks_external_ref ON tasks(external_ref);

ALTER TABLE pages ADD COLUMN IF NOT EXISTS external_ref TEXT;
