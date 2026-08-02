-- Activity entries outlive what they describe, but stop pointing at it (OCT 8bf73df4).
--
-- activity_entries was created without foreign keys, so deleting a task left
-- its entries behind still carrying the dead task_id: the project activity view
-- kept rendering them as clickable rows that opened nothing. The log is
-- history, so the entry stays — it is the *link* that must go. Deleting a task,
-- release or sprint now nulls the reference and stamps target_deleted, which is
-- what the UI greys out. Only deleting the project removes the entries, because
-- then there is no activity feed left to read them in.
--
-- release_id and sprint_id are new: those entries only ever carried the name in
-- their payload, so a deleted release was indistinguishable from a live one.
-- Entries written before this migration keep a NULL reference and are therefore
-- never greyed — there is nothing left to resolve them against.
--
-- The constraints are plain (no ON DELETE action) on purpose. The app unlinks
-- inside the same delete transaction (cascadeDeleteTaskChildren for tasks, the
-- Delete methods for releases and sprints, the statement list for projects);
-- a plain FK makes any forgotten path fail loudly instead of either orphaning
-- silently or unlinking without the grey-out that explains it.
ALTER TABLE activity_entries ADD COLUMN IF NOT EXISTS release_id TEXT;
ALTER TABLE activity_entries ADD COLUMN IF NOT EXISTS sprint_id TEXT;
ALTER TABLE activity_entries ADD COLUMN IF NOT EXISTS target_deleted BOOLEAN NOT NULL DEFAULT FALSE;

-- Entries whose task is already gone: unlink and grey them, the same end state
-- a delete produces from now on.
UPDATE activity_entries a
   SET task_id = NULL, target_deleted = TRUE
 WHERE a.task_id IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.id = a.task_id);

-- Entries whose project is gone have no feed to appear in and no referent to
-- attach to; they are unreachable rows. Removing them is what the project
-- delete already does, applied retroactively.
DELETE FROM activity_entries a
 WHERE NOT EXISTS (SELECT 1 FROM projects p WHERE p.id = a.project_id);

ALTER TABLE activity_entries
  ADD CONSTRAINT fk_activity_entries_project FOREIGN KEY (project_id) REFERENCES projects(id);
ALTER TABLE activity_entries
  ADD CONSTRAINT fk_activity_entries_task FOREIGN KEY (task_id) REFERENCES tasks(id);
ALTER TABLE activity_entries
  ADD CONSTRAINT fk_activity_entries_release FOREIGN KEY (release_id) REFERENCES releases(id);
ALTER TABLE activity_entries
  ADD CONSTRAINT fk_activity_entries_sprint FOREIGN KEY (sprint_id) REFERENCES sprints(id);

-- The unlink UPDATEs run on every release and sprint delete; without these they
-- are sequential scans of the whole log. idx_activity_task already covers tasks.
CREATE INDEX IF NOT EXISTS idx_activity_release ON activity_entries(release_id);
CREATE INDEX IF NOT EXISTS idx_activity_sprint  ON activity_entries(sprint_id);
