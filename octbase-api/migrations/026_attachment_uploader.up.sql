-- Per-user storage accounting for uploaded attachments.
--
-- uploaded_by records which user stored the file, so the per-user storage
-- quota (OCTBASE_MAX_USER_STORAGE_MB) can be enforced by summing size_bytes
-- per uploader. Nullable: rows created before this migration (and external
-- links, which occupy no storage) have no uploader and count toward nobody.
-- References the users tombstone row after GDPR anonymization, like other
-- authored content.
ALTER TABLE task_attachments ADD COLUMN IF NOT EXISTS uploaded_by TEXT REFERENCES users(id);

CREATE INDEX IF NOT EXISTS idx_task_attachments_uploaded_by
  ON task_attachments (uploaded_by) WHERE uploaded_by IS NOT NULL;
