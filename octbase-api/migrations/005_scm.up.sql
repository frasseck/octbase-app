ALTER TABLE branch_references ADD COLUMN IF NOT EXISTS pr_status  TEXT;
ALTER TABLE branch_references ADD COLUMN IF NOT EXISTS pr_url     TEXT;
ALTER TABLE branch_references ADD COLUMN IF NOT EXISTS pr_number  INTEGER;
ALTER TABLE repository_connections ADD COLUMN IF NOT EXISTS auto_close_on_merge BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS project_task_counters (
    project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    last_seq   INTEGER NOT NULL DEFAULT 0
);

ALTER TABLE tasks ADD COLUMN IF NOT EXISTS seq_number INTEGER;
