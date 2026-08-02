-- Drop the activity referential-integrity constraints and the columns that
-- carry the release/sprint reference and the greyed-out marker.
--
-- Two things the up migration did are not undone, because they cannot be: the
-- entries it deleted (their project was already gone) and the task references
-- it nulled (their task was already gone). Down restores the schema, not the
-- dangling ids.
DROP INDEX IF EXISTS idx_activity_sprint;
DROP INDEX IF EXISTS idx_activity_release;

ALTER TABLE activity_entries DROP CONSTRAINT IF EXISTS fk_activity_entries_sprint;
ALTER TABLE activity_entries DROP CONSTRAINT IF EXISTS fk_activity_entries_release;
ALTER TABLE activity_entries DROP CONSTRAINT IF EXISTS fk_activity_entries_task;
ALTER TABLE activity_entries DROP CONSTRAINT IF EXISTS fk_activity_entries_project;

ALTER TABLE activity_entries DROP COLUMN IF EXISTS target_deleted;
ALTER TABLE activity_entries DROP COLUMN IF EXISTS sprint_id;
ALTER TABLE activity_entries DROP COLUMN IF EXISTS release_id;
