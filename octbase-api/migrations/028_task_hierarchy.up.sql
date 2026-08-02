-- Task hierarchy: EPIC → STORY → TASK → SUBTASK.
-- parent_id links a task to its parent one level up; the level rules
-- (subtask requires a TASK parent, epic never has one, …) are enforced in the
-- application layer. The plain FK (NO ACTION) backs the app-level guard that
-- a task with children cannot be deleted.
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS parent_id TEXT REFERENCES tasks(id);
CREATE INDEX IF NOT EXISTS idx_tasks_parent_id ON tasks(parent_id);

-- The BUG and CHORE task types are retired; existing rows become plain TASKs.
UPDATE tasks SET task_type='TASK' WHERE task_type IN ('BUG','CHORE');
UPDATE task_templates SET task_type='TASK' WHERE task_type IN ('BUG','CHORE');
