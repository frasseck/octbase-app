-- Project task settings: optional THEME/INITIATIVE hierarchy levels and
-- admin-defined additional priorities.
--
-- theme_enabled / initiative_enabled switch the top hierarchy levels on per
-- project (THEME → INITIATIVE → EPIC → STORY → TASK → SUBTASK); the level
-- rules stay enforced in the application layer (see TaskTypeChain).
ALTER TABLE projects ADD COLUMN IF NOT EXISTS theme_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE projects ADD COLUMN IF NOT EXISTS initiative_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- Additional per-project priority values on top of the built-in
-- LOW/MEDIUM/HIGH/CRITICAL/BLOCKER set. tasks.priority stores the name.
CREATE TABLE IF NOT EXISTS project_priorities (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id),
  name TEXT NOT NULL,
  rank INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE (project_id, name)
);
CREATE INDEX IF NOT EXISTS idx_project_priorities_project ON project_priorities(project_id);
