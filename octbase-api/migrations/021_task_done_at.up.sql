-- done_at records the moment a task entered the DONE status. It is the basis
-- for auto-archiving: DONE tasks are swept to ARCHIVED (hidden from the board)
-- 30 days after completion. updated_at is unsuitable because edits and board
-- reorders bump it. The column is NULL for any task not currently DONE; it is
-- set on the transition into DONE and cleared on any transition out (e.g. reopen).
-- Stored as TEXT (RFC3339 UTC, lexicographically ordered) to match the other
-- timestamp columns, so range comparisons are plain string comparisons.
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS done_at TEXT;

-- Backfill existing DONE tasks so the auto-archive sweep applies to them too.
-- updated_at is the best available approximation of when they were completed.
UPDATE tasks SET done_at = updated_at WHERE status = 'DONE' AND done_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_tasks_done_at ON tasks(done_at);
