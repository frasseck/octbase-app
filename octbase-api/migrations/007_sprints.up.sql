CREATE TABLE IF NOT EXISTS sprints (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL REFERENCES projects(id),
    name         TEXT NOT NULL,
    goal         TEXT NOT NULL DEFAULT '',
    start_date   TEXT,
    end_date     TEXT,
    status       TEXT NOT NULL DEFAULT 'PLANNED',
    release_id TEXT REFERENCES releases(id) ON DELETE SET NULL,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    version      INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_sprints_project_id ON sprints(project_id);
CREATE INDEX IF NOT EXISTS idx_sprints_project_status ON sprints(project_id, status);

ALTER TABLE tasks ADD COLUMN IF NOT EXISTS sprint_id TEXT REFERENCES sprints(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_tasks_sprint_id ON tasks(sprint_id);
