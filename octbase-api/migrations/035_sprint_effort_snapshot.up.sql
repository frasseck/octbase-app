-- Effort snapshot of a sprint's board scope at completion time — the effort
-- twin of migration 015's committed_count/completed_count.
--
-- Velocity is a pure projection of what was captured when the sprint was
-- completed: completing a sprint unlinks its unfinished tasks and removes its
-- board, so the original scope can never be recomputed afterwards. Effort needs
-- the same treatment as the counts, plus one thing the counts do not: the unit.
--
-- estimate_unit is stored PER SPRINT rather than read live from the project,
-- because a project may switch POINTS -> HOURS later and a historical sprint's
-- numbers must keep meaning what they meant when they were taken. NULL in all
-- three columns means the sprint was completed while estimation was off
-- (estimation_unit = 'NONE'); NULL is a distinct state from a snapshot of 0.
ALTER TABLE sprints ADD COLUMN IF NOT EXISTS committed_estimate NUMERIC(10,2) NULL;
ALTER TABLE sprints ADD COLUMN IF NOT EXISTS completed_estimate NUMERIC(10,2) NULL;
ALTER TABLE sprints ADD COLUMN IF NOT EXISTS estimate_unit TEXT NULL;
