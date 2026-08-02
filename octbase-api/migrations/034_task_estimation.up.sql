-- Task estimation: a per-project effort-estimation unit and the per-task
-- estimate expressed in it.
--
-- estimation_unit switches estimation on for a project: NONE (the default —
-- no estimation anywhere in the project's UI), POINTS or HOURS. Exactly one
-- unit is ever active, and the value set is enforced in the application layer
-- (see ValidEstimationUnit) rather than by a CHECK constraint, matching how
-- status/visibility/task_type are already stored as plain TEXT.
ALTER TABLE projects ADD COLUMN IF NOT EXISTS estimation_unit TEXT NOT NULL DEFAULT 'NONE';

-- The two units get two columns on purpose. Switching a project's unit is
-- non-destructive: the estimate in the unit that is no longer active stays
-- stored but dormant, and reappears unchanged if the project switches back.
-- A single polymorphic "estimate" column would silently reinterpret 5 points
-- as 5 hours. NULL means unestimated, which is a distinct state from 0.
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS story_points INT NULL;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS estimate_hours NUMERIC(7,2) NULL;
