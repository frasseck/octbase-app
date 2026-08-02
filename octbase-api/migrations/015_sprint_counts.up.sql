-- Snapshot of a sprint's board scope at completion time. We record how many
-- tasks were on the board (committed) and how many were DONE, because completing
-- a sprint unlinks the unfinished tasks (they return to the backlog) and would
-- otherwise lose the original totals for the historical sprint report.
ALTER TABLE sprints ADD COLUMN IF NOT EXISTS committed_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sprints ADD COLUMN IF NOT EXISTS completed_count INTEGER NOT NULL DEFAULT 0;
